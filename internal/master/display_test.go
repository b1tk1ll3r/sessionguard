package master

import (
	"testing"

	"github.com/example/sessionguard/internal/model"
)

func TestNormalizeMultiMonitor(t *testing.T) {
	x := model.Resource{MultiMonitor: true, MultiMonitorGroups: []string{" RDS-Power ", "rds-power", ""}}
	if err := normalizeMultiMonitor(&x); err != nil {
		t.Fatal(err)
	}
	if x.MaxMonitors != defaultMaxMonitors || len(x.MultiMonitorGroups) != 1 || x.MultiMonitorGroups[0] != "RDS-Power" {
		t.Fatalf("unexpected normalization: %+v", x)
	}
	x = model.Resource{MultiMonitor: true, MaxMonitors: 9}
	if err := normalizeMultiMonitor(&x); err == nil {
		t.Fatal("max_monitors > 4 must be rejected")
	}
	x = model.Resource{MaxMonitors: 3, MultiMonitorGroups: []string{"a"}}
	if err := normalizeMultiMonitor(&x); err != nil || x.MaxMonitors != 0 || x.MultiMonitorGroups != nil {
		t.Fatalf("disabled multi-monitor must clear settings: %+v %v", x, err)
	}
}

func TestDisplayPolicyFor(t *testing.T) {
	a := brokerTestApp()
	a.store.data.Resources["desk"] = model.Resource{ID: "desk", Name: "Desktop", Kind: "desktop", FarmID: "office", Enabled: true,
		GuacamoleConnectionID: "12", MultiMonitor: true, MaxMonitors: 3, MultiMonitorGroups: []string{"rds-multimon"}}
	a.store.data.Resources["app"] = model.Resource{ID: "app", Name: "App", Kind: "desktop", FarmID: "office", Enabled: true,
		GuacamoleConnectionName: "Office"}

	member := model.AuthSession{Username: "max", Groups: []string{"RDS-MultiMon"}}
	other := model.AuthSession{Username: "erika", Groups: []string{"staff"}}

	if p := a.displayPolicyFor(member, model.BrokerRequest{ConnectionID: "12"}); !p.MultiMonitor || p.MaxMonitors != 3 || p.ResourceID != "desk" {
		t.Fatalf("group member should get multi-monitor: %+v", p)
	}
	if p := a.displayPolicyFor(other, model.BrokerRequest{ConnectionID: "12"}); p.MultiMonitor || p.ResourceID != "" {
		t.Fatalf("non-member must not get multi-monitor: %+v", p)
	}
	if p := a.displayPolicyFor(member, model.BrokerRequest{ConnectionName: "office"}); p.MultiMonitor {
		t.Fatalf("resource without multi-monitor must stay disabled: %+v", p)
	}
	if p := a.displayPolicyFor(member, model.BrokerRequest{ConnectionID: "999"}); p.MultiMonitor || p.MaxMonitors != 1 {
		t.Fatalf("unmapped connection must be disabled: %+v", p)
	}
}
