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

	// The profile is user-controlled: read it through an os.Root so junctions cannot
	// make SYSTEM copy files from outside the profile into the store.
	walk := folders
	src, err := os.OpenRoot(profileRoot)
	if errors.Is(err, os.ErrNotExist) {
		walk = nil
	} else if err != nil {
		return total, fmt.Errorf("open profile: %w", err)
	} else {
		defer src.Close()
	}
	for _, folder := range walk {
		rel, err := cleanRelative(folder.Path)
		if err != nil {
			return total, fmt.Errorf("profile folder %q: %w", folder.Path, err)
		}
		if err := checkNoLinksRel(src.Lstat, rel); err != nil {
			return total, fmt.Errorf("backup %q: %w", folder.Path, err)
		}
		st, err := copyTree(src.FS(), filepath.ToSlash(rel), dirFS(staging), rel, folder.ExcludeGlobs)
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
	// The destination profile is user-controlled: every write goes through an os.Root
	// and every path component is checked for links/reparse points.
	if err := os.MkdirAll(profileRoot, 0o700); err != nil {
		return total, true, err
	}
	dst, err := os.OpenRoot(profileRoot)
	if err != nil {
		return total, true, fmt.Errorf("open profile: %w", err)
	}
	defer dst.Close()
	src := os.DirFS(current)
	for _, folder := range folders {
		rel, err := cleanRelative(folder.Path)
		if err != nil {
			return total, true, fmt.Errorf("profile folder %q: %w", folder.Path, err)
		}
		st, err := copyTree(src, filepath.ToSlash(rel), dst, rel, folder.ExcludeGlobs)
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

// destFS is the write surface used by copyTree. Restores use an *os.Root opened on the
// user profile so a junction or symlink planted (or swapped in) by the user cannot
// redirect writes performed as SYSTEM outside the profile.
type destFS interface {
	MkdirAll(name string, perm os.FileMode) error
	OpenFile(name string, flag int, perm os.FileMode) (*os.File, error)
	Lstat(name string) (os.FileInfo, error)
	Rename(oldname, newname string) error
	Remove(name string) error
	Chtimes(name string, atime, mtime time.Time) error
}

// dirFS is a plain path based destFS for the admin-controlled profile store.
type dirFS string

func (d dirFS) p(n string) string                         { return filepath.Join(string(d), n) }
func (d dirFS) MkdirAll(n string, perm os.FileMode) error { return os.MkdirAll(d.p(n), perm) }
func (d dirFS) OpenFile(n string, flag int, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(d.p(n), flag, perm)
}
func (d dirFS) Lstat(n string) (os.FileInfo, error)    { return os.Lstat(d.p(n)) }
func (d dirFS) Rename(o, n string) error               { return os.Rename(d.p(o), d.p(n)) }
func (d dirFS) Remove(n string) error                  { return os.Remove(d.p(n)) }
func (d dirFS) Chtimes(n string, a, m time.Time) error { return os.Chtimes(d.p(n), a, m) }

// IsLink reports symbolic links and junctions/mount points (os.ModeIrregular on Windows).
func IsLink(m os.FileMode) bool { return m&(os.ModeSymlink|os.ModeIrregular) != 0 }

// CheckNoLinks walks every component below root down to target with Lstat and
// rejects symbolic links and reparse points. Missing trailing components are allowed;
// callers must re-check after creating them.
func CheckNoLinks(root, target string) error {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("path %s escapes %s", target, root)
	}
	return CheckNoLinksIn(func(n string) (os.FileInfo, error) { return os.Lstat(filepath.Join(root, n)) }, rel)
}

// CheckNoLinksIn is CheckNoLinks for a name relative to an opened root (e.g. os.Root.Lstat).
func CheckNoLinksIn(lstat func(string) (os.FileInfo, error), rel string) error {
	return checkNoLinksRel(lstat, rel)
}

func checkNoLinksRel(lstat func(string) (os.FileInfo, error), rel string) error {
	rel = filepath.Clean(rel)
	if rel == "." {
		return nil
	}
	cur := ""
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		cur = filepath.Join(cur, part)
		fi, err := lstat(cur)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if IsLink(fi.Mode()) {
			return fmt.Errorf("refusing symbolic link/reparse point %s", cur)
		}
	}
	return nil
}

// copyTree copies srcRel (slash separated, relative to src) to dstRel (relative to dst).
func copyTree(src fs.FS, srcRel string, dst destFS, dstRel string, excludes []string) (Stats, error) {
	var stats Stats
	info, err := fs.Lstat(src, srcRel)
	if err != nil {
		return stats, err
	}
	if IsLink(info.Mode()) {
		return stats, fmt.Errorf("refusing symbolic link/reparse-point root %s", srcRel)
	}
	if !info.IsDir() {
		if err := copyFile(src, srcRel, dst, dstRel, info); err != nil {
			return stats, err
		}
		stats.Files = 1
		stats.Bytes = info.Size()
		return stats, nil
	}
	err = fs.WalkDir(src, srcRel, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relSlash := strings.TrimPrefix(strings.TrimPrefix(p, srcRel), "/")
		if relSlash == "" {
			if err := mkdirChecked(dst, dstRel, info.Mode().Perm()); err != nil {
				return err
			}
			stats.Dirs++
			return nil
		}
		if excluded(relSlash, excludes) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if IsLink(d.Type()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dstRel, filepath.FromSlash(relSlash))
		fi, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			if err := mkdirChecked(dst, target, fi.Mode().Perm()); err != nil {
				return err
			}
			stats.Dirs++
			return nil
		}
		if !fi.Mode().IsRegular() {
			return nil
		}
		if err := copyFile(src, p, dst, target, fi); err != nil {
			return err
		}
		stats.Files++
		stats.Bytes += fi.Size()
		return nil
	})
	return stats, err
}

// mkdirChecked refuses links/reparse points before and after creating the chain.
func mkdirChecked(dst destFS, name string, perm os.FileMode) error {
	if err := checkNoLinksRel(dst.Lstat, name); err != nil {
		return err
	}
	if err := dst.MkdirAll(name, perm|0o700); err != nil {
		return err
	}
	return checkNoLinksRel(dst.Lstat, name)
}

func copyFile(src fs.FS, srcName string, dst destFS, dstName string, info os.FileInfo) error {
	if err := mkdirChecked(dst, filepath.Dir(dstName), 0o700); err != nil {
		return err
	}
	in, err := src.Open(srcName)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dstName + ".sessionguard-tmp"
	// A stale or planted tmp entry is removed (a link is removed, not followed);
	// O_EXCL then guarantees a freshly created regular file.
	if err := dst.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	out, err := dst.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm()|0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = dst.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = dst.Remove(tmp)
		return closeErr
	}
	_ = dst.Chtimes(tmp, info.ModTime(), info.ModTime())
	if err := replaceFile(dst, tmp, dstName); err != nil {
		_ = dst.Remove(tmp)
		return err
	}
	return nil
}

// replaceFile avoids the destructive "remove destination and hope Rename works"
// pattern that is especially risky on Windows. If a destination exists it is first
// moved aside; a failed activation restores the previous file. Existing links or
// reparse points are never replaced.
func replaceFile(dst destFS, tmp, name string) error {
	if err := checkNoLinksRel(dst.Lstat, filepath.Dir(name)); err != nil {
		return err
	}
	if fi, err := dst.Lstat(name); errors.Is(err, os.ErrNotExist) {
		return dst.Rename(tmp, name)
	} else if err != nil {
		return err
	} else if IsLink(fi.Mode()) || fi.IsDir() {
		return fmt.Errorf("refusing to overwrite link/reparse point or directory %s", name)
	}
	old := name + ".sessionguard-old"
	_ = dst.Remove(old)
	if err := dst.Rename(name, old); err != nil {
		return fmt.Errorf("stage existing destination: %w", err)
	}
	if err := dst.Rename(tmp, name); err != nil {
		if rollbackErr := dst.Rename(old, name); rollbackErr != nil {
			return fmt.Errorf("activate replacement: %v; rollback failed: %w", err, rollbackErr)
		}
		return fmt.Errorf("activate replacement: %w", err)
	}
	if err := dst.Remove(old); err != nil && !errors.Is(err, os.ErrNotExist) {
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
