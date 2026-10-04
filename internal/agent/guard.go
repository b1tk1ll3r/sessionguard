package agent

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/example/sessionguard/internal/config"
	"github.com/example/sessionguard/internal/model"
)

// localUIActions are the session actions the local WebUI may trigger. Process kills and
// server restarts are reserved for the master.
var localUIActions = map[string]bool{"logoff": true, "disconnect": true, "message": true}

// guardPath normalizes a Windows or POSIX path for case-insensitive prefix comparison.
// UNC and device paths keep their leading "//".
func guardPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if !isUNC(p) && !hasDrive(p) && !filepath.IsAbs(p) {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
	}
	s := strings.ToLower(strings.ReplaceAll(p, `\`, "/"))
	unc := strings.HasPrefix(s, "//")
	s = path.Clean(s)
	if unc && !strings.HasPrefix(s, "//") {
		s = "/" + s
	}
	return strings.TrimSuffix(s, "/")
}

func isUNC(p string) bool {
	p = strings.ReplaceAll(strings.TrimSpace(p), `\`, "/")
	return strings.HasPrefix(p, "//")
}

func hasDrive(p string) bool {
	return len(p) >= 2 && p[1] == ':' && ((p[0] >= 'a' && p[0] <= 'z') || (p[0] >= 'A' && p[0] <= 'Z'))
}

// pathWithin reports whether p equals or lies below root on a path boundary.
func pathWithin(p, root string) bool {
	p, root = guardPath(p), guardPath(root)
	if p == "" || root == "" {
		return false
	}
	return p == root || strings.HasPrefix(p, root+"/")
}

func withinAny(p string, roots []string) bool {
	for _, r := range roots {
		if strings.TrimSpace(r) != "" && pathWithin(p, r) {
			return true
		}
	}
	return false
}

// protectedDirs are the agent's own config and state directories.
func protectedDirs(cfg config.Agent) []string {
	var dirs []string
	if cfg.ConfigPath != "" {
		dirs = append(dirs, filepath.Dir(cfg.ConfigPath))
	}
	if cfg.DataDir != "" {
		dirs = append(dirs, cfg.DataDir)
	}
	return dirs
}

// checkTemplateSource vets a template source path lexically.
func checkTemplateSource(cfg config.Agent, src string) error {
	g := cfg.LocalGuard
	if !isUNC(src) && !hasDrive(src) && !filepath.IsAbs(src) {
		return fmt.Errorf("template source %q must be an absolute path", src)
	}
	for _, d := range protectedDirs(cfg) {
		if pathWithin(src, d) {
			return fmt.Errorf("template source %q is inside the agent config/state directory", src)
		}
	}
	if len(g.AllowedTemplateSourceRoots) > 0 {
		if !withinAny(src, g.AllowedTemplateSourceRoots) {
			return fmt.Errorf("template source %q is outside local_guard.allowed_template_source_roots", src)
		}
		return nil
	}
	if isUNC(src) {
		return fmt.Errorf("UNC template source %q requires local_guard.allowed_template_source_roots", src)
	}
	return nil
}

// checkTemplateSourceResolved runs before a source is read: it repeats the lexical
// check on the resolved path so symlinks, junctions or 8.3 short names cannot point
// into the protected directories.
func checkTemplateSourceResolved(cfg config.Agent, src string) error {
	if err := checkTemplateSource(cfg, src); err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(src)
	if err != nil {
		if isUNC(src) {
			// Remote shares may not support resolution; the lexical check above applies.
			return nil
		}
		return fmt.Errorf("resolve template source: %w", err)
	}
	for _, d := range protectedDirs(cfg) {
		if rd, err := filepath.EvalSymlinks(d); err == nil && pathWithin(resolved, rd) {
			return fmt.Errorf("template source %q resolves into the agent config/state directory", src)
		}
	}
	return checkTemplateSource(cfg, resolved)
}

// checkPolicyGuard validates a policy received from the master or the local WebUI
// against the agent's local_guard.
func checkPolicyGuard(cfg config.Agent, p model.Policy) error {
	g := cfg.LocalGuard
	if len(g.AllowedStoreRoots) > 0 && strings.TrimSpace(p.Profiles.StoreRoot) != "" && !withinAny(p.Profiles.StoreRoot, g.AllowedStoreRoots) {
		return fmt.Errorf("profiles.store_root %q is outside local_guard.allowed_store_roots", p.Profiles.StoreRoot)
	}
	if len(g.AllowedProfileRoots) > 0 {
		for _, r := range p.Cleanup.AllowedProfileRoots {
			if !withinAny(r, g.AllowedProfileRoots) {
				return fmt.Errorf("cleanup.allowed_profile_roots entry %q is outside local_guard.allowed_profile_roots", r)
			}
		}
	}
	var errs []error
	for _, t := range p.Templates {
		if t.Source != "" {
			if err := checkTemplateSource(cfg, t.Source); err != nil {
				errs = append(errs, fmt.Errorf("template %s: %w", t.ID, err))
			}
		}
	}
	return errors.Join(errs...)
}

func protectedUser(cfg config.Agent, user, sid string) bool {
	return len(cfg.LocalGuard.ProtectedUsers) > 0 && excludedIdentity(user, sid, cfg.LocalGuard.ProtectedUsers, nil)
}

func (a *App) protected(s model.Session) bool { return protectedUser(a.cfg, displayUser(s), s.SID) }
