package master

import (
	"testing"

	"github.com/example/sessionguard/internal/model"
)

func TestEffectivePolicySource(t *testing.T) {
	a := brokerTestApp()
	rec := testAgent("rds01", "rds01", "online", 100)
	if src, p := a.effectivePolicySourceLocked(rec); src != "none" || p != nil {
		t.Fatalf("no policy: %s %v", src, p)
	}
	a.store.data.GlobalPolicy = &model.Policy{Revision: "g1"}
	if src, _ := a.effectivePolicySourceLocked(rec); src != "global" {
		t.Fatalf("global expected, got %s", src)
	}
	f := a.store.data.Farms["office"]
	f.Policy = &model.Policy{Revision: "f1"}
	a.store.data.Farms["office"] = f
	if src, p := a.effectivePolicySourceLocked(rec); src != "farm:office" || p.Revision != "f1" {
		t.Fatalf("farm expected, got %s", src)
	}
	rec.DesiredPolicy = &model.Policy{Revision: "a1"}
	if src, p := a.effectivePolicySourceLocked(rec); src != "agent" || p.Revision != "a1" {
		t.Fatalf("agent override expected, got %s", src)
	}
	if got := a.effectivePolicyLocked(rec); got.Revision != "a1" {
		t.Fatalf("effectivePolicyLocked must keep precedence, got %s", got.Revision)
	}
}
