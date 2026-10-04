package templates

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf16"

	"github.com/example/sessionguard/internal/model"
	"github.com/example/sessionguard/internal/profile"
)

// Apply materializes item below the user profile. All writes go through an os.Root on
// the profile and refuse symlinks/junctions in any path component, because the agent
// runs as SYSTEM while the profile is user-controlled. checkSource vets item.Source
// before it is read; a nil checkSource rejects source based templates.
func Apply(profileDir string, item model.TemplateItem, checkSource func(string) error) (changed bool, err error) {
	if item.ID == "" {
		return false, errors.New("template item id is required")
	}
	target, rel, err := safeTarget(profileDir, item.Target)
	if err != nil {
		return false, err
	}
	root, err := os.OpenRoot(profileDir)
	if err != nil {
		return false, fmt.Errorf("open profile: %w", err)
	}
	defer root.Close()
	switch strings.ToLower(item.Kind) {
	case "directory":
		if err := profile.CheckNoLinksIn(root.Lstat, rel); err != nil {
			return false, err
		}
		if st, err := root.Lstat(rel); err == nil && st.IsDir() {
			return false, nil
		}
		return true, mkdirChecked(root, rel)
	case "file":
		data, err := sourceData(item, checkSource)
		if err != nil {
			return false, err
		}
		return ensureFile(root, rel, data, item.Overwrite)
	case "url":
		if item.URL == "" {
			return false, errors.New("url template requires url")
		}
		data := []byte("[InternetShortcut]\r\nURL=" + item.URL + "\r\n")
		return ensureFile(root, rel, data, item.Overwrite)
	case "shortcut":
		if item.Shortcut == nil || item.Shortcut.Target == "" {
			return false, errors.New("shortcut template requires shortcut.target")
		}
		if runtime.GOOS != "windows" {
			return false, errors.New("shortcut generation is Windows-only")
		}
		if err := profile.CheckNoLinksIn(root.Lstat, rel); err != nil {
			return false, err
		}
		if st, err := root.Lstat(rel); err == nil {
			if !st.Mode().IsRegular() {
				return false, fmt.Errorf("refusing to overwrite non-regular file %s", rel)
			}
			if !item.Overwrite {
				return false, nil
			}
		}
		if err := mkdirChecked(root, filepath.Dir(rel)); err != nil {
			return false, err
		}
		return ensureShortcut(root, target, rel, *item.Shortcut)
	default:
		return false, fmt.Errorf("unknown template kind %q", item.Kind)
	}
}

func safeTarget(profileDir, rel string) (string, string, error) {
	if rel == "" || filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" {
		return "", "", errors.New("template target must be relative to the user profile")
	}
	root, err := filepath.Abs(profileDir)
	if err != nil {
		return "", "", err
	}
	t, err := filepath.Abs(filepath.Join(root, rel))
	if err != nil {
		return "", "", err
	}
	rp := strings.ToLower(filepath.Clean(root)) + string(os.PathSeparator)
	tp := strings.ToLower(filepath.Clean(t))
	if tp == strings.TrimSuffix(rp, string(os.PathSeparator)) || !strings.HasPrefix(tp, rp) {
		return "", "", errors.New("template target escapes profile root")
	}
	r, err := filepath.Rel(root, t)
	if err != nil {
		return "", "", err
	}
	return t, r, nil
}

func sourceData(item model.TemplateItem, checkSource func(string) error) ([]byte, error) {
	if item.Source != "" {
		if checkSource == nil {
			return nil, errors.New("template source files are not allowed here")
		}
		if err := checkSource(item.Source); err != nil {
			return nil, fmt.Errorf("template source rejected: %w", err)
		}
		return os.ReadFile(item.Source)
	}
	if item.ContentBase64 != "" {
		return base64.StdEncoding.DecodeString(item.ContentBase64)
	}
	return []byte(item.Content), nil
}

func mkdirChecked(root *os.Root, rel string) error {
	if err := profile.CheckNoLinksIn(root.Lstat, rel); err != nil {
		return err
	}
	if err := root.MkdirAll(rel, 0o755); err != nil {
		return err
	}
	return profile.CheckNoLinksIn(root.Lstat, rel)
}

func ensureFile(root *os.Root, rel string, data []byte, overwrite bool) (bool, error) {
	if err := profile.CheckNoLinksIn(root.Lstat, rel); err != nil {
		return false, err
	}
	if st, err := root.Lstat(rel); err == nil {
		if !st.Mode().IsRegular() {
			return false, fmt.Errorf("refusing to overwrite non-regular file %s", rel)
		}
		if old, err := root.ReadFile(rel); err == nil {
			if hash(old) == hash(data) {
				return false, nil
			}
			if !overwrite {
				return false, nil
			}
		}
	}
	if err := mkdirChecked(root, filepath.Dir(rel)); err != nil {
		return false, err
	}
	tmp := rel + ".sessionguard.tmp"
	// Remove a stale or planted entry (links are removed, not followed); O_EXCL then
	// guarantees a freshly created regular file.
	if err := root.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	f, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return false, err
	}
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = profile.CheckNoLinksIn(root.Lstat, rel)
	}
	if werr == nil {
		werr = root.Rename(tmp, rel)
	}
	if werr != nil {
		_ = root.Remove(tmp)
		return false, werr
	}
	return true, nil
}

func hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// psQuoter doubles every character PowerShell treats as a single quote inside a
// single-quoted literal: ASCII ' plus U+2018, U+2019, U+201A and U+201B.
var psQuoter = func() *strings.Replacer {
	pairs := []string{"'", "''"}
	for r := rune(0x2018); r <= 0x201b; r++ {
		q := string(r)
		pairs = append(pairs, q, q+q)
	}
	return strings.NewReplacer(pairs...)
}()

func psQuote(s string) string { return "'" + psQuoter.Replace(s) + "'" }

// ensureShortcut compares an existing .lnk via WScript.Shell and, if it differs, saves
// the new shortcut in a private temp dir; the bytes are then written through root.
func ensureShortcut(root *os.Root, path, rel string, s model.ShortcutSpec) (bool, error) {
	dir, err := os.MkdirTemp("", "sessionguard-lnk-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(dir)
	tmp := filepath.Join(dir, "shortcut.lnk")
	script := "$w=New-Object -ComObject WScript.Shell;" +
		"$p=" + psQuote(path) + ";" +
		"if(Test-Path -LiteralPath $p){$x=$w.CreateShortcut($p);" +
		"if(($x.TargetPath -eq " + psQuote(s.Target) + ") -and ($x.Arguments -eq " + psQuote(s.Arguments) + ") -and ($x.WorkingDirectory -eq " + psQuote(s.WorkingDirectory) + ") -and ($x.IconLocation -eq " + psQuote(s.IconLocation) + ") -and ($x.Description -eq " + psQuote(s.Description) + ")){Write-Output 'UNCHANGED';exit 0}};" +
		"$l=$w.CreateShortcut(" + psQuote(tmp) + ");" +
		"$l.TargetPath=" + psQuote(s.Target) + ";" +
		"$l.Arguments=" + psQuote(s.Arguments) + ";" +
		"$l.WorkingDirectory=" + psQuote(s.WorkingDirectory) + ";" +
		"$l.IconLocation=" + psQuote(s.IconLocation) + ";" +
		"$l.Description=" + psQuote(s.Description) + ";$l.Save();Write-Output 'CHANGED'"
	u16 := utf16.Encode([]rune(script))
	bytes := make([]byte, len(u16)*2)
	for i, v := range u16 {
		bytes[i*2] = byte(v)
		bytes[i*2+1] = byte(v >> 8)
	}
	enc := base64.StdEncoding.EncodeToString(bytes)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", enc)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("ensure shortcut: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), "CHANGED") || strings.Contains(string(out), "UNCHANGED") {
		return false, nil
	}
	data, err := os.ReadFile(tmp)
	if err != nil {
		return false, fmt.Errorf("ensure shortcut: %w", err)
	}
	return ensureFile(root, rel, data, true)
}

func CopyFile(dst, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, cpErr := io.Copy(out, in)
	closeErr := out.Close()
	if cpErr != nil {
		return cpErr
	}
	return closeErr
}
