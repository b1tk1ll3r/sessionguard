package agent

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/example/sessionguard/internal/config"
	"github.com/example/sessionguard/internal/model"
)

func TestPathWithin(t *testing.T) {
	cases := []struct {
		p, root string
		want    bool
	}{
		{`C:\Users\alice`, `C:\Users`, true},
		{`c:\users`, `C:\Users\`, true},
		{`C:\UsersEvil\x`, `C:\Users`, false},
		{`C:\Users\..\Windows`, `C:\Users`, false},
		{`\\FileServer\Share\x`, `\\fileserver\share`, true},
		{`\\fileserver\shareX`, `\\fileserver\share`, false},
		{`\\?\C:\ProgramData\SessionGuard`, `C:\ProgramData`, false},
	}
	for _, c := range cases {
		if got := pathWithin(c.p, c.root); got != c.want {
			t.Errorf("pathWithin(%q, %q)=%v want %v", c.p, c.root, got, c.want)
		}
	}
}

func TestTemplateSourceGuard(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Agent{ConfigPath: filepath.Join(dir, "agent.json"), DataDir: dir}
	other := filepath.Join(t.TempDir(), "defaults.json")
	if err := checkTemplateSource(cfg, filepath.Join(dir, "agent.json")); err == nil {
		t.Fatal("expected config dir source to be rejected")
	}
	if err := checkTemplateSource(cfg, filepath.Join(dir, "sub", "..", "state.json")); err == nil {
		t.Fatal("expected config dir source to be rejected after cleaning")
	}
	if err := checkTemplateSource(cfg, other); err != nil {
		t.Fatalf("local source rejected: %v", err)
	}
	if err := checkTemplateSource(cfg, "relative.json"); err == nil {
		t.Fatal("expected relative source to be rejected")
	}
	if err := checkTemplateSource(cfg, `\\fileserver\templates\x.json`); err == nil {
		t.Fatal("expected UNC source to be rejected by default")
	}
	cfg.LocalGuard.AllowedTemplateSourceRoots = []string{`\\fileserver\templates`}
	if err := checkTemplateSource(cfg, `\\FILESERVER\templates\x.json`); err != nil {
		t.Fatalf("allowed UNC source rejected: %v", err)
	}
	if err := checkTemplateSource(cfg, other); err == nil {
		t.Fatal("expected source outside allowed roots to be rejected")
	}
	cfg.LocalGuard.AllowedTemplateSourceRoots = []string{filepath.Dir(dir)}
	if err := checkTemplateSource(cfg, filepath.Join(dir, "agent.json")); err == nil {
		t.Fatal("config dir must stay denied even inside an allowed root")
	}
}

func TestTemplateSourceResolvedRejectsLinkIntoConfigDir(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Agent{ConfigPath: filepath.Join(dir, "agent.json"), DataDir: dir}
	if err := os.WriteFile(cfg.ConfigPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	l := filepath.Join(t.TempDir(), "innocent.json")
	if err := os.Symlink(cfg.ConfigPath, l); err != nil {
		if runtime.GOOS != "windows" {
			t.Skipf("symlink: %v", err)
		}
		j := filepath.Join(t.TempDir(), "j")
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", j, dir).CombinedOutput(); err != nil {
			t.Skipf("mklink /J: %v %s", err, out)
		}
		l = filepath.Join(j, "agent.json")
	}
	if err := checkTemplateSourceResolved(cfg, l); err == nil {
		t.Fatal("expected symlink into config dir to be rejected")
	}
}

func TestPolicyGuard(t *testing.T) {
	cfg := config.Agent{LocalGuard: config.LocalGuard{AllowedStoreRoots: []string{`\\fileserver\profiles`}, AllowedProfileRoots: []string{`C:\Users`}}}
	p := model.Policy{Cleanup: model.CleanupPolicy{AllowedProfileRoots: []string{`C:\Users`}}, Profiles: model.ProfilePolicy{StoreRoot: `\\fileserver\profiles\ts01`}}
	if err := checkPolicyGuard(cfg, p); err != nil {
		t.Fatalf("valid policy rejected: %v", err)
	}
	p.Profiles.StoreRoot = `\\attacker\share`
	if err := checkPolicyGuard(cfg, p); err == nil {
		t.Fatal("expected foreign store_root to be rejected")
	}
	p.Profiles.StoreRoot = `\\fileserver\profiles`
	p.Cleanup.AllowedProfileRoots = []string{`C:\`}
	if err := checkPolicyGuard(cfg, p); err == nil {
		t.Fatal("expected widened profile root to be rejected")
	}
	p.Cleanup.AllowedProfileRoots = []string{`C:\Users`}
	p.Templates = []model.TemplateItem{{ID: "t", Kind: "file", Target: "x", Source: `\\fileserver\x`}}
	if err := checkPolicyGuard(cfg, p); err == nil {
		t.Fatal("expected UNC template source to be rejected")
	}
	if err := checkPolicyGuard(config.Agent{}, model.Policy{Profiles: model.ProfilePolicy{StoreRoot: `\\any\share`}}); err != nil {
		t.Fatalf("empty guard must keep previous behavior: %v", err)
	}
}

func TestProtectedUser(t *testing.T) {
	cfg := config.Agent{LocalGuard: config.LocalGuard{ProtectedUsers: []string{"svc-backup"}}}
	if !protectedUser(cfg, `DOMAIN\svc-backup`, "S-1-5-21-1") || protectedUser(cfg, `DOMAIN\alice`, "S-1-5-21-2") {
		t.Fatal("protected user matching is wrong")
	}
}

func TestRestartServerRespectsControlEnabled(t *testing.T) {
	a := &App{state: State{Policy: model.Policy{}}}
	res := a.executeSessionCommand(model.SessionCommand{ID: "x", Action: "restart_server"})
	if res.Success || !strings.Contains(res.Error, "disabled") {
		t.Fatalf("restart_server ran with control disabled: %+v", res)
	}
}

func TestLocalUIRejectsPrivilegedActions(t *testing.T) {
	a := &App{state: State{Policy: model.Policy{Sessions: model.SessionPolicy{ControlEnabled: true}}}}
	for _, action := range []string{"restart_server", "kill_process", "bogus"} {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/2/action", strings.NewReader(`{"action":"`+action+`"}`))
		r.SetPathValue("id", "2")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		a.sessionActionAPI(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s: status=%d body=%s", action, w.Code, w.Body.String())
		}
	}
}

func TestLoadAgentMasterURLScheme(t *testing.T) {
	cases := []struct {
		body string
		ok   bool
	}{
		{`{"master_url":"https://sg.example.org"}`, true},
		{`{"master_url":"http://127.0.0.1:8080"}`, true},
		{`{"master_url":"http://localhost:8080"}`, true},
		{`{"master_url":"http://10.202.0.10:8080"}`, false},
		{`{"master_url":"http://10.202.0.10:8080","insecure_master_url":true}`, true},
		{`{"master_url":"ftp://x"}`, false},
	}
	for _, c := range cases {
		p := filepath.Join(t.TempDir(), "agent.json")
		if err := os.WriteFile(p, []byte(c.body), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("SESSIONGUARD_MASTER_URL", "")
		cfg, err := config.LoadAgent(p)
		if (err == nil) != c.ok {
			t.Fatalf("%s: err=%v", c.body, err)
		}
		if err == nil && cfg.ConfigPath == "" {
			t.Fatal("ConfigPath not set")
		}
	}
}
