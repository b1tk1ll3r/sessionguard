package profile

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/example/sessionguard/internal/model"
)

func TestBackupRestoreAndExclude(t *testing.T) {
	root := t.TempDir()
	profileA := filepath.Join(root, "profile-a")
	store := filepath.Join(root, "store")
	profileB := filepath.Join(root, "profile-b")
	mustWrite(t, filepath.Join(profileA, "AppData", "Roaming", "Example", "settings.json"), "v1")
	mustWrite(t, filepath.Join(profileA, "AppData", "Roaming", "Example", "Cache", "skip.bin"), "skip")
	folders := []model.ProfileFolder{{Path: "AppData/Roaming/Example", ExcludeGlobs: []string{"Cache/**"}}}
	st, err := Backup(profileA, store, "S-1-5-21-test", `DOMAIN\user`, folders, 2)
	if err != nil {
		t.Fatal(err)
	}
	if st.Files != 1 {
		t.Fatalf("expected 1 file, got %+v", st)
	}
	st, found, err := Restore(profileB, store, "S-1-5-21-test", folders)
	if err != nil || !found || st.Files != 1 {
		t.Fatalf("restore: found=%v stats=%+v err=%v", found, st, err)
	}
	b, err := os.ReadFile(filepath.Join(profileB, "AppData", "Roaming", "Example", "settings.json"))
	if err != nil || string(b) != "v1" {
		t.Fatalf("restored content = %q, err=%v", b, err)
	}
	if _, err := os.Stat(filepath.Join(profileB, "AppData", "Roaming", "Example", "Cache", "skip.bin")); !os.IsNotExist(err) {
		t.Fatalf("excluded cache restored unexpectedly: %v", err)
	}
}

func TestCleanRelativeRejectsEscape(t *testing.T) {
	for _, v := range []string{"../x", `..\\x`, `/etc`, `C:\\Users\\x`, ""} {
		if _, err := cleanRelative(v); err == nil {
			t.Fatalf("expected %q to be rejected", v)
		}
	}
}

func mustWrite(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreOverwritesSnapshotFilesButKeepsLocalExtras(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	store := filepath.Join(root, "store")
	dest := filepath.Join(root, "dest")
	folders := []model.ProfileFolder{{Path: "Desktop"}}
	mustWrite(t, filepath.Join(source, "Desktop", "managed.txt"), "from-backup")
	if _, err := Backup(source, store, "S-1-5-21-merge", `DOMAIN\\user`, folders, 0); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dest, "Desktop", "managed.txt"), "local-old")
	mustWrite(t, filepath.Join(dest, "Desktop", "local-only.txt"), "keep-me")
	if _, found, err := Restore(dest, store, "S-1-5-21-merge", folders); err != nil || !found {
		t.Fatalf("restore found=%v err=%v", found, err)
	}
	b, err := os.ReadFile(filepath.Join(dest, "Desktop", "managed.txt"))
	if err != nil || string(b) != "from-backup" {
		t.Fatalf("managed content=%q err=%v", b, err)
	}
	b, err = os.ReadFile(filepath.Join(dest, "Desktop", "local-only.txt"))
	if err != nil || string(b) != "keep-me" {
		t.Fatalf("local-only content=%q err=%v", b, err)
	}
}

func TestBackupHistoryRetention(t *testing.T) {
	root := t.TempDir()
	profileRoot := filepath.Join(root, "profile")
	store := filepath.Join(root, "store")
	folders := []model.ProfileFolder{{Path: "Desktop"}}
	for i, content := range []string{"one", "two", "three", "four"} {
		mustWrite(t, filepath.Join(profileRoot, "Desktop", "version.txt"), content)
		if _, err := Backup(profileRoot, store, "S-1-5-21-history", `DOMAIN\\user`, folders, 2); err != nil {
			t.Fatalf("backup %d: %v", i, err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	history := filepath.Join(store, "S-1-5-21-history", "history")
	entries, err := os.ReadDir(history)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("history entries=%d, want 2", len(entries))
	}
	b, err := os.ReadFile(filepath.Join(store, "S-1-5-21-history", "current", "Desktop", "version.txt"))
	if err != nil || string(b) != "four" {
		t.Fatalf("current content=%q err=%v", b, err)
	}
}

func TestBackupActivationGuardKeepsPreviousCurrent(t *testing.T) {
	root := t.TempDir()
	profileRoot := filepath.Join(root, "profile")
	store := filepath.Join(root, "store")
	folders := []model.ProfileFolder{{Path: "Desktop"}}
	mustWrite(t, filepath.Join(profileRoot, "Desktop", "version.txt"), "old")
	if _, err := Backup(profileRoot, store, "S-1-5-21-guard", `DOMAIN\\user`, folders, 1); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(profileRoot, "Desktop", "version.txt"), "new")
	if _, err := BackupGuarded(profileRoot, store, "S-1-5-21-guard", `DOMAIN\\user`, folders, 1, func() error {
		return errors.New("user became active")
	}); err == nil {
		t.Fatal("expected activation guard error")
	}
	b, err := os.ReadFile(filepath.Join(store, "S-1-5-21-guard", "current", "Desktop", "version.txt"))
	if err != nil || string(b) != "old" {
		t.Fatalf("current snapshot changed despite guard: %q err=%v", b, err)
	}
}

// link creates a directory link (symlink, or a junction on Windows when symlinks
// need privileges) or skips the test.
func link(t *testing.T, target, name string) {
	t.Helper()
	if err := os.Symlink(target, name); err == nil {
		return
	}
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", name, target).CombinedOutput(); err == nil {
			return
		} else {
			t.Logf("mklink /J: %v %s", err, out)
		}
	}
	t.Skip("cannot create symlinks or junctions on this platform")
}

func TestRestoreRefusesLinkedProfileComponents(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	store := filepath.Join(root, "store")
	dest := filepath.Join(root, "dest")
	outside := filepath.Join(root, "outside")
	folders := []model.ProfileFolder{{Path: "AppData/Roaming/Example"}}
	mustWrite(t, filepath.Join(source, "AppData", "Roaming", "Example", "evil.dll"), "payload")
	if _, err := Backup(source, store, "S-1-5-21-link", `DOMAIN\user`, folders, 0); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dest, "AppData"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	link(t, outside, filepath.Join(dest, "AppData", "Roaming"))
	if _, _, err := Restore(dest, store, "S-1-5-21-link", folders); err == nil {
		t.Fatal("expected restore through a linked component to fail")
	}
	if _, err := os.Stat(filepath.Join(outside, "Example", "evil.dll")); !os.IsNotExist(err) {
		t.Fatalf("restore wrote through link: %v", err)
	}
}

func TestBackupRefusesLinkedProfileComponents(t *testing.T) {
	root := t.TempDir()
	profileRoot := filepath.Join(root, "profile")
	secret := filepath.Join(root, "secret")
	mustWrite(t, filepath.Join(secret, "Example", "secret.txt"), "secret")
	if err := os.MkdirAll(filepath.Join(profileRoot, "AppData"), 0o755); err != nil {
		t.Fatal(err)
	}
	link(t, secret, filepath.Join(profileRoot, "AppData", "Roaming"))
	folders := []model.ProfileFolder{{Path: "AppData/Roaming/Example"}}
	if _, err := Backup(profileRoot, filepath.Join(root, "store"), "S-1-5-21-blink", `DOMAIN\user`, folders, 0); err == nil {
		t.Fatal("expected backup through a linked component to fail")
	}
}

func TestCheckNoLinks(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CheckNoLinks(root, filepath.Join(root, "a", "b", "missing", "x.txt")); err != nil {
		t.Fatalf("plain path rejected: %v", err)
	}
	if err := CheckNoLinks(root, filepath.Join(root, "..", "x")); err == nil {
		t.Fatal("expected escape rejection")
	}
	link(t, filepath.Join(root, "a"), filepath.Join(root, "l"))
	if err := CheckNoLinks(root, filepath.Join(root, "l", "b", "x.txt")); err == nil {
		t.Fatal("expected link rejection")
	}
}

func TestJunctionIsRejected(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junctions are Windows-only")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "target"), 0o755); err != nil {
		t.Fatal(err)
	}
	j := filepath.Join(root, "j")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", j, filepath.Join(root, "target")).CombinedOutput(); err != nil {
		t.Skipf("mklink /J: %v %s", err, out)
	}
	if err := CheckNoLinks(root, filepath.Join(j, "x.txt")); err == nil {
		t.Fatal("expected junction rejection")
	}
}
