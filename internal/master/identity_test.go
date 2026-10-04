package master

import (
	"errors"
	"testing"
	"time"

	"github.com/example/sessionguard/internal/auth"
	"github.com/example/sessionguard/internal/model"
)

func TestBindIdentityPreventsRenameTakeover(t *testing.T) {
	a := brokerTestApp()
	st := authSessionStore{s: a.store}
	now := time.Now().UTC()
	if err := st.BindIdentity("Chef", "sub-chef", "https://idp.example", now); err != nil {
		t.Fatal(err)
	}
	if err := st.BindIdentity("chef", "sub-chef", "https://idp.example", now); err != nil {
		t.Fatalf("same subject must be accepted case-insensitively: %v", err)
	}
	if err := st.BindIdentity("CHEF", "sub-attacker", "https://idp.example", now); !errors.Is(err, auth.ErrIdentityConflict) {
		t.Fatalf("other subject claiming a bound username must fail, got %v", err)
	}
	if err := st.BindIdentity("intern", "sub-intern", "https://idp.example", now); err != nil {
		t.Fatal(err)
	}
	if err := st.BindIdentity("new-name", "sub-intern", "https://idp.example", now); !errors.Is(err, auth.ErrIdentityConflict) {
		t.Fatalf("bound subject switching username must fail, got %v", err)
	}
}

func TestBindIdentityAfterIssuerChangeNeedsReset(t *testing.T) {
	a := brokerTestApp()
	st := authSessionStore{s: a.store}
	now := time.Now().UTC()
	if err := st.BindIdentity("max", "pocket-sub", "https://pocketid.example", now); err != nil {
		t.Fatal(err)
	}
	if err := st.BindIdentity("max", "keycloak-sub", "https://kc.example/realms/x", now); !errors.Is(err, auth.ErrIdentityConflict) {
		t.Fatalf("a new issuer must not silently take over a binding, got %v", err)
	}
	a.store.data.IdentityBindings = map[string]model.IdentityBinding{} // admin reset
	if err := st.BindIdentity("max", "keycloak-sub", "https://kc.example/realms/x", now); err != nil {
		t.Fatalf("after reset the new IdP must bind: %v", err)
	}
}

func TestSeedIdentityBindingsFromSessions(t *testing.T) {
	d := emptyData()
	t0 := time.Now().Add(-time.Hour)
	d.AuthSessions["h1"] = model.AuthSession{Username: "Max", Subject: "s1", CreatedAt: t0}
	d.AuthSessions["h2"] = model.AuthSession{Username: "max", Subject: "s2", CreatedAt: t0.Add(time.Minute)}
	seedIdentityBindings(&d)
	if b := d.IdentityBindings["max"]; b.Subject != "s1" || len(d.IdentityBindings) != 1 {
		t.Fatalf("oldest session must win: %+v", d.IdentityBindings)
	}
}
