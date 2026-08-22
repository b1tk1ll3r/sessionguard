package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/example/sessionguard/internal/model"
)

type Stats struct {
	Files int   `json:"files"`
	Dirs  int   `json:"dirs"`
	Bytes int64 `json:"bytes"`
}

type Manifest struct {
	Version   int                   `json:"version"`
	SID       string                `json:"sid"`
	User      string                `json:"user"`
	CreatedAt time.Time             `json:"created_at"`
	Folders   []model.ProfileFolder `json:"folders"`
	Stats     Stats                 `json:"stats"`
}

func Backup(profileRoot, storeRoot, sid, user string, folders []model.ProfileFolder, keepVersions int) (Stats, error) {
	return BackupGuarded(profileRoot, storeRoot, sid, user, folders, keepVersions, nil)
}

// BackupGuarded copies a complete staging snapshot and calls activationGuard immediately
// before replacing current. A guard failure leaves the previous current snapshot untouched.
func BackupGuarded(profileRoot, storeRoot, sid, user string, folders []model.ProfileFolder, keepVersions int, activationGuard func() error) (Stats, error) {
	var total Stats
	if strings.TrimSpace(storeRoot) == "" {
		return total, errors.New("profile store_root is empty")
	}
	if strings.TrimSpace(sid) == "" {
		return total, errors.New("profile SID is empty")
	}
	if len(folders) == 0 {
		return total, errors.New("no profile folders configured")
	}
	userRoot := filepath.Join(storeRoot, safeSID(sid))
	if err := os.MkdirAll(userRoot, 0o700); err != nil {
		return total, fmt.Errorf("create profile store: %w", err)
	}
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	staging := filepath.Join(userRoot, ".staging-"+stamp)
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return total, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(staging)
		}
	}()

	for _, folder := range folders {
		rel, err := cleanRelative(folder.Path)
		if err != nil {
			return total, fmt.Errorf("profile folder %q: %w", folder.Path, err)
		}
		src := filepath.Join(profileRoot, rel)
		dst := filepath.Join(staging, rel)
		st, err := copyTree(src, dst, folder.ExcludeGlobs)
		if errors.Is(err, os.ErrNotExist) {
			// A configured application folder may legitimately not exist for every user.
			continue
		}
		if err != nil {
			return total, fmt.Errorf("backup %q: %w", folder.Path, err)
		}
		total.Files += st.Files
		total.Dirs += st.Dirs
		total.Bytes += st.Bytes
	}
	manifest := Manifest{Version: 1, SID: sid, User: user, CreatedAt: time.Now().UTC(), Folders: folders, Stats: total}
	b, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return total, err
	}
	if err := os.WriteFile(filepath.Join(staging, ".sessionguard-manifest.json"), b, 0o600); err != nil {
		return total, fmt.Errorf("write manifest: %w", err)
	}

	if activationGuard != nil {
		if err := activationGuard(); err != nil {
			return total, fmt.Errorf("snapshot activation guard: %w", err)
		}
	}

	current := filepath.Join(userRoot, "current")
	archive := ""
	if _, err := os.Stat(current); err == nil {
		if keepVersions > 0 {
			history := filepath.Join(userRoot, "history")
			if err := os.MkdirAll(history, 0o700); err != nil {
				return total, err
			}
			archive = filepath.Join(history, stamp)
			if err := os.Rename(current, archive); err != nil {
				return total, fmt.Errorf("archive previous profile snapshot: %w", err)
			}
		} else {
			// Keep a temporary rollback copy until the new snapshot is active.
			archive = filepath.Join(userRoot, ".previous-"+stamp)
			if err := os.Rename(current, archive); err != nil {
				return total, fmt.Errorf("stage previous profile snapshot: %w", err)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return total, err
	}
	if err := os.Rename(staging, current); err != nil {
		if archive != "" {
			_ = os.Rename(archive, current)
		}
		return total, fmt.Errorf("activate profile snapshot: %w", err)
	}
	if keepVersions == 0 && archive != "" {
		_ = os.RemoveAll(archive)
	}
	ok = true
	if keepVersions > 0 {
		if err := pruneHistory(filepath.Join(userRoot, "history"), keepVersions); err != nil {
			return total, fmt.Errorf("prune profile history: %w", err)
		}
	}
	return total, nil
}

func Restore(profileRoot, storeRoot, sid string, folders []model.ProfileFolder) (Stats, bool, error) {
	var total Stats
	if strings.TrimSpace(storeRoot) == "" {
		return total, false, errors.New("profile store_root is empty")
	}
	current := filepath.Join(storeRoot, safeSID(sid), "current")
	if _, err := os.Stat(current); errors.Is(err, os.ErrNotExist) {
		return total, false, nil
	} else if err != nil {
		return total, false, err
	}
	for _, folder := range folders {
		rel, err := cleanRelative(folder.Path)
		if err != nil {
			return total, true, fmt.Errorf("profile folder %q: %w", folder.Path, err)
		}
		src := filepath.Join(current, rel)
		dst := filepath.Join(profileRoot, rel)
		st, err := copyTree(src, dst, folder.ExcludeGlobs)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return total, true, fmt.Errorf("restore %q: %w", folder.Path, err)
		}
		total.Files += st.Files
		total.Dirs += st.Dirs
		total.Bytes += st.Bytes
	}
	return total, true, nil
}

func cleanRelative(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", errors.New("path is empty")
	}
	// Normalize both separators so validation is identical on Windows and in tests.
	normalized := strings.ReplaceAll(v, `\`, "/")
	if strings.HasPrefix(normalized, "/") || strings.Contains(normalized, ":") {
		return "", errors.New("path must be relative to the user profile")
	}
	cleanSlash := path.Clean(normalized)
	if cleanSlash == "." || cleanSlash == ".." || strings.HasPrefix(cleanSlash, "../") {
		return "", errors.New("path escapes the user profile")
	}
	return filepath.FromSlash(cleanSlash), nil
}

func safeSID(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func copyTree(src, dst string, excludes []string) (Stats, error) {
	var stats Stats
	info, err := os.Lstat(src)
	if err != nil {
		return stats, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return stats, fmt.Errorf("refusing symbolic link/reparse-point root %s", src)
	}
	if !info.IsDir() {
		if err := copyFile(src, dst, info); err != nil {
			return stats, err
		}
		stats.Files = 1
		stats.Bytes = info.Size()
		return stats, nil
	}
	root := src
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
				return err
			}
			stats.Dirs++
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if excluded(relSlash, excludes) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			fi, err := d.Info()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(target, fi.Mode().Perm()); err != nil {
				return err
			}
			stats.Dirs++
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		if !fi.Mode().IsRegular() {
			return nil
		}
		if err := copyFile(p, target, fi); err != nil {
			return err
		}
		stats.Files++
		stats.Bytes += fi.Size()
		return nil
	})
	return stats, err
}

func copyFile(src, dst string, info os.FileInfo) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".sessionguard-tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	_ = os.Chtimes(tmp, info.ModTime(), info.ModTime())
	if err := replaceFile(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// replaceFile avoids the destructive "remove destination and hope Rename works"
// pattern that is especially risky on Windows. If a destination exists it is first
// moved aside; a failed activation restores the previous file.
func replaceFile(tmp, dst string) error {
	if _, err := os.Lstat(dst); errors.Is(err, os.ErrNotExist) {
		return os.Rename(tmp, dst)
	} else if err != nil {
		return err
	}
	old := dst + ".sessionguard-old"
	_ = os.Remove(old)
	if err := os.Rename(dst, old); err != nil {
		return fmt.Errorf("stage existing destination: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		if rollbackErr := os.Rename(old, dst); rollbackErr != nil {
			return fmt.Errorf("activate replacement: %v; rollback failed: %w", err, rollbackErr)
		}
		return fmt.Errorf("activate replacement: %w", err)
	}
	if err := os.Remove(old); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove replaced destination: %w", err)
	}
	return nil
}

func excluded(rel string, patterns []string) bool {
	rel = strings.TrimPrefix(filepath.ToSlash(rel), "./")
	for _, raw := range patterns {
		p := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(raw)), "./")
		if p == "" {
			continue
		}
		if strings.HasSuffix(p, "/**") {
			prefix := strings.TrimSuffix(p, "/**")
			if rel == prefix || strings.HasPrefix(rel, prefix+"/") {
				return true
			}
			continue
		}
		if ok, _ := path.Match(p, rel); ok {
			return true
		}
	}
	return false
}

func pruneHistory(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	if keep < 0 {
		keep = 0
	}
	if keep >= len(names) {
		return nil
	}
	for _, name := range names[keep:] {
		if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	return nil
}
