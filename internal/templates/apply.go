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
)

func Apply(profile string, item model.TemplateItem) (changed bool, err error) {
	if item.ID == "" {
		return false, errors.New("template item id is required")
	}
	target, err := safeTarget(profile, item.Target)
	if err != nil {
		return false, err
	}
	switch strings.ToLower(item.Kind) {
	case "directory":
		if st, err := os.Stat(target); err == nil && st.IsDir() {
			return false, nil
		}
		return true, os.MkdirAll(target, 0o755)
	case "file":
		data, err := sourceData(item)
		if err != nil {
			return false, err
		}
		return ensureFile(target, data, item.Overwrite)
	case "url":
		if item.URL == "" {
			return false, errors.New("url template requires url")
		}
		data := []byte("[InternetShortcut]\r\nURL=" + item.URL + "\r\n")
		return ensureFile(target, data, item.Overwrite)
	case "shortcut":
		if item.Shortcut == nil || item.Shortcut.Target == "" {
			return false, errors.New("shortcut template requires shortcut.target")
		}
		if runtime.GOOS != "windows" {
			return false, errors.New("shortcut generation is Windows-only")
		}
		if _, err := os.Stat(target); err == nil && !item.Overwrite {
			return false, nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return false, err
		}
		return ensureShortcut(target, *item.Shortcut)
	default:
		return false, fmt.Errorf("unknown template kind %q", item.Kind)
	}
}

func safeTarget(profile, rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) {
		return "", errors.New("template target must be relative to the user profile")
	}
	root, err := filepath.Abs(profile)
	if err != nil {
		return "", err
	}
	t, err := filepath.Abs(filepath.Join(root, rel))
	if err != nil {
		return "", err
	}
	rp := strings.ToLower(filepath.Clean(root)) + string(os.PathSeparator)
	tp := strings.ToLower(filepath.Clean(t))
	if tp != strings.TrimSuffix(rp, string(os.PathSeparator)) && !strings.HasPrefix(tp, rp) {
		return "", errors.New("template target escapes profile root")
	}
	return t, nil
}

func sourceData(item model.TemplateItem) ([]byte, error) {
	if item.Source != "" {
		return os.ReadFile(item.Source)
	}
	if item.ContentBase64 != "" {
		return base64.StdEncoding.DecodeString(item.ContentBase64)
	}
	return []byte(item.Content), nil
}

func ensureFile(path string, data []byte, overwrite bool) (bool, error) {
	if old, err := os.ReadFile(path); err == nil {
		if hash(old) == hash(data) {
			return false, nil
		}
		if !overwrite {
			return false, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	tmp := path + ".sessionguard.tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return false, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	return true, nil
}

func hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func ensureShortcut(path string, s model.ShortcutSpec) (bool, error) {
	script := "$w=New-Object -ComObject WScript.Shell;" +
		"$p=" + psQuote(path) + ";" +
		"if(Test-Path -LiteralPath $p){$x=$w.CreateShortcut($p);" +
		"if(($x.TargetPath -eq " + psQuote(s.Target) + ") -and ($x.Arguments -eq " + psQuote(s.Arguments) + ") -and ($x.WorkingDirectory -eq " + psQuote(s.WorkingDirectory) + ") -and ($x.IconLocation -eq " + psQuote(s.IconLocation) + ") -and ($x.Description -eq " + psQuote(s.Description) + ")){Write-Output 'UNCHANGED';exit 0}};" +
		"$l=$w.CreateShortcut($p);" +
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
	return strings.Contains(string(out), "CHANGED"), nil
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
