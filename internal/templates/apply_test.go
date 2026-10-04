package templates

import (
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/example/sessionguard/internal/model"
)

func TestFileTemplateIsIdempotent(t *testing.T) {
	root := t.TempDir()
	item := model.TemplateItem{ID: "x", Kind: "file", Target: filepath.Join("Desktop", "x.txt"), Content: "hello", Overwrite: true}
	changed, err := Apply(root, item, nil)
	if err != nil || !changed {
		t.Fatalf("first apply: changed=%v err=%v", changed, err)
	}
	changed, err = Apply(root, item, nil)
	if err != nil || changed {
		t.Fatalf("second apply: changed=%v err=%v", changed, err)
	}
	b, _ := os.ReadFile(filepath.Join(root, "Desktop", "x.txt"))
	if string(b) != "hello" {
		t.Fatalf("unexpected content %q", b)
	}
}

func TestTargetCannotEscapeProfile(t *testing.T) {
	_, err := Apply(t.TempDir(), model.TemplateItem{ID: "x", Kind: "file", Target: filepath.Join("..", "escape.txt"), Content: "x", Overwrite: true}, nil)
	if err == nil {
		t.Fatal("expected traversal rejection")
	}
}

func dirLink(t *testing.T, target, name string) {
	t.Helper()
	if err := os.Symlink(target, name); err == nil {
		return
	}
	if runtime.GOOS == "windows" {
		if err := exec.Command("cmd", "/c", "mklink", "/J", name, target).Run(); err == nil {
			return
		}
	}
	t.Skip("cannot create symlinks or junctions on this platform")
}

func TestTemplateRefusesLinkedComponents(t *testing.T) {
	root := t.TempDir()
	profileDir := filepath.Join(root, "profile")
	outside := filepath.Join(root, "outside")
	for _, d := range []string{profileDir, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	dirLink(t, outside, filepath.Join(profileDir, "Desktop"))
	for _, kind := range []string{"file", "directory"} {
		item := model.TemplateItem{ID: "x", Kind: kind, Target: filepath.Join("Desktop", "x"), Content: "x", Overwrite: true}
		if _, err := Apply(profileDir, item, nil); err == nil {
			t.Fatalf("%s: expected link rejection", kind)
		}
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("template wrote through link: %v", entries)
	}
}

func TestTemplateOverwriteAndSourceGuard(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(t.TempDir(), "src.txt")
	if err := os.WriteFile(src, []byte("from-source"), 0o644); err != nil {
		t.Fatal(err)
	}
	item := model.TemplateItem{ID: "x", Kind: "file", Target: "x.txt", Source: src, Overwrite: true}
	if _, err := Apply(root, item, nil); err == nil {
		t.Fatal("expected source rejection without guard")
	}
	if _, err := Apply(root, item, func(string) error { return errors.New("denied") }); err == nil {
		t.Fatal("expected source rejection by guard")
	}
	if err := os.WriteFile(filepath.Join(root, "x.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if changed, err := Apply(root, item, func(string) error { return nil }); err != nil || !changed {
		t.Fatalf("apply: changed=%v err=%v", changed, err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "x.txt")); string(b) != "from-source" {
		t.Fatalf("content=%q", b)
	}
}

func TestPSQuoteDoublesAllSingleQuotes(t *testing.T) {
	q1, q2, q3, q4 := string(rune(0x2018)), string(rune(0x2019)), string(rune(0x201a)), string(rune(0x201b))
	in := "a'b" + q1 + "c" + q2 + "d" + q3 + "e" + q4 + "f"
	want := "'a''b" + q1 + q1 + "c" + q2 + q2 + "d" + q3 + q3 + "e" + q4 + q4 + "f'"
	if got := psQuote(in); got != want {
		t.Fatalf("psQuote=%q want %q", got, want)
	}
	if runtime.GOOS != "windows" {
		return
	}
	// Round-trip through PowerShell: an injection attempt must come back verbatim.
	payload := "x" + q2 + ";Write-Output INJECTED;" + q1 + "'"
	script := "[Console]::OutputEncoding=[Text.Encoding]::UTF8;Write-Output " + psQuote(payload)
	u16 := utf16.Encode([]rune(script))
	b := make([]byte, len(u16)*2)
	for i, v := range u16 {
		b[i*2], b[i*2+1] = byte(v), byte(v>>8)
	}
	out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(b)).Output()
	if err != nil {
		t.Skipf("powershell unavailable: %v", err)
	}
	if got := strings.TrimSpace(strings.TrimPrefix(string(out), string(rune(0xfeff)))); got != payload {
		t.Fatalf("powershell returned %q, want %q", got, payload)
	}
}
