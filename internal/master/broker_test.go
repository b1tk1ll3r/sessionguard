package master

import (
	"context"
	"testing"
	"time"

	"github.com/example/sessionguard/internal/config"
	"github.com/example/sessionguard/internal/model"
)

type memoryPersistence struct{}

func (memoryPersistence) Load(context.Context, *data) error                        { return nil }
func (memoryPersistence) Save(context.Context, data) error                         { return nil }
func (memoryPersistence) AppendAudit(context.Context, model.AuditEntry, int) error { return nil }
func (memoryPersistence) AppendHistory(context.Context, model.SessionHistoryEvent, int) error {
	return nil
}
func (memoryPersistence) Close() error { return nil }
func (memoryPersistence) Kind() string { return "memory" }

func brokerTestApp() *App {
	d := emptyData()
	d.Farms["office"] = model.Farm{ID: "office", Name: "Office", Enabled: true}
	return &App{
		cfg: config.Master{
			OfflineAfterSeconds: 60,
			Broker: model.BrokerConfig{
				Enabled: true, LeaseSeconds: 900, MinHealthScore: 60,
				ReconnectExisting: true, SingleSession: true, DefaultFarmID: "office",
			},
		},
		store: &store{data: d, backend: memoryPersistence{}},
	}
}

func testAgent(id, host, mode string, health int, sessions ...model.Session) model.AgentRecord {
	return model.AgentRecord{
		ID: id, Name: host, LastSeen: time.Now().UTC(), MaintenanceMode: mode,
		FarmIDs: []string{"office"},
		Snapshot: model.AgentSnapshot{
			Server: model.ServerInfo{Hostname: host, CPUPercent: 20, MemoryTotal: 100, MemoryAvailable: 70},
			Health: model.HealthStatus{Score: health}, Sessions: sessions,
		},
	}
}

func TestBrokerReconnectsExistingSessionOnDrain(t *testing.T) {
	a := brokerTestApp()
	a.store.data.Agents["rds01"] = testAgent("rds01", "rds01.example.test", "drain", 90,
		model.Session{ID: 7, User: "Max", Domain: "EXAMPLE", State: "Disconnected"})
	a.store.data.Agents["rds02"] = testAgent("rds02", "rds02.example.test", "online", 100)

	got, err := a.resolveBroker(model.BrokerRequest{Username: `EXAMPLE\Max`, FarmID: "office"})
	if err != nil {
		t.Fatal(err)
	}
	if got.AgentID != "rds01" || got.Reason != "existing-session" || !got.Reconnect {
		t.Fatalf("unexpected broker result: %+v", got)
	}
}

func TestBrokerDoesNotUseDrainForNewSession(t *testing.T) {
	a := brokerTestApp()
	a.store.data.Agents["rds01"] = testAgent("rds01", "rds01.example.test", "drain", 100)
	a.store.data.Agents["rds02"] = testAgent("rds02", "rds02.example.test", "online", 80)

	got, err := a.resolveBroker(model.BrokerRequest{Username: `EXAMPLE\NewUser`, FarmID: "office"})
	if err != nil {
		t.Fatal(err)
	}
	if got.AgentID != "rds02" || got.Reason != "load-balance" {
		t.Fatalf("unexpected broker result: %+v", got)
	}
}

func TestBrokerKeepsFarmIsolation(t *testing.T) {
	a := brokerTestApp()
	a.store.data.Farms["erp"] = model.Farm{ID: "erp", Name: "ERP", Enabled: true}
	a.store.data.Agents["office01"] = testAgent("office01", "office01.example.test", "online", 80)
	erp := testAgent("erp01", "erp01.example.test", "online", 100,
		model.Session{ID: 3, User: "Max", Domain: "EXAMPLE", State: "Disconnected"})
	erp.FarmIDs = []string{"erp"}
	a.store.data.Agents["erp01"] = erp

	got, err := a.resolveBroker(model.BrokerRequest{Username: `EXAMPLE\Max`, FarmID: "office"})
	if err != nil {
		t.Fatal(err)
	}
	if got.AgentID != "office01" {
		t.Fatalf("broker crossed farm boundary: %+v", got)
	}
}

func TestBrokerRejectsUnknownFarm(t *testing.T) {
	a := brokerTestApp()
	_, err := a.resolveBroker(model.BrokerRequest{Username: `EXAMPLE\Max`, FarmID: "missing"})
	if err == nil {
		t.Fatal("expected unknown farm to fail closed")
	}
}

func TestEffectivePolicyIncludesTagSelectedFarm(t *testing.T) {
	a := brokerTestApp()
	p := model.Policy{Revision: "farm-policy"}
	f := a.store.data.Farms["office"]
	f.RequiredTags = map[string]string{"role": "office"}
	f.Policy = &p
	a.store.data.Farms["office"] = f
	rec := model.AgentRecord{ID: "rds01", Tags: map[string]string{"role": "office"}}

	got := a.effectivePolicyLocked(rec)
	if got == nil || got.Revision != "farm-policy" {
		t.Fatalf("tag-selected farm policy not applied: %+v", got)
	}
}

func TestGuacamoleTokenBridgeOnlyBrokersMappedResources(t *testing.T) {
	a := brokerTestApp()
	a.store.data.Resources["office-desktop"] = model.Resource{
		ID: "office-desktop", Name: "Office Desktop", Kind: "desktop", FarmID: "office",
		GuacamoleConnectionName: "Office Desktop", Enabled: true,
	}
	if !a.hasMappedResource(model.BrokerRequest{ConnectionName: "office desktop"}) {
		t.Fatal("expected case-insensitive configured Guacamole connection to be mapped")
	}
	if a.hasMappedResource(model.BrokerRequest{ConnectionName: "Static Admin RDP"}) {
		t.Fatal("unmapped Guacamole connection must remain untouched by broker token bridge")
	}
}

func TestDomainQualifiedBrokerIdentityDoesNotCrossDomain(t *testing.T) {
	s := model.Session{User: "Max", Domain: "DOMAINA", State: "Disconnected"}
	if sessionMatchesUser(s, normalizeUser(`DOMAINB\Max`)) {
		t.Fatal("domain-qualified identity matched a session from a different domain")
	}
	if !sessionMatchesUser(s, normalizeUser(`DOMAINA\Max`)) {
		t.Fatal("matching domain-qualified identity did not match")
	}
}
