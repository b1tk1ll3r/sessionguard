package config

import (
	"strings"
	"testing"
)

// The shipped Caddy Sentinel example must stay loadable once the secrets
// come from the environment, as in deploy/docker-compose.yml.
func TestSentinelMasterExampleLoads(t *testing.T) {
	for _, k := range []string{"SESSIONGUARD_ENROLLMENT_TOKEN", "SESSIONGUARD_BROKER_API_KEY", "SESSIONGUARD_OIDC_CLIENT_SECRET", "SESSIONGUARD_ACCESS_OIDC_CLIENT_SECRET"} {
		t.Setenv(k, strings.Repeat("a1", 16)+k)
	}
	c, err := LoadMaster("../../deploy/master.json.example")
	if err != nil {
		t.Fatal(err)
	}
	if !c.AccessAuth.Enabled || c.AccessAuth.Issuer != c.OIDC.Issuer {
		t.Fatalf("access auth must use the Sentinel issuer: %+v", c.AccessAuth)
	}
	if c.OIDC.RevalidateMinutes <= 0 || c.AccessAuth.RevalidateMinutes <= 0 {
		t.Fatal("revalidation must be enabled for Sentinel (no back-channel logout)")
	}
	if len(c.OIDC.AdminGroups) == 0 {
		t.Fatal("admin groups must be derived from rbac.groups")
	}
}

func TestRevalidateMinutesRange(t *testing.T) {
	for _, k := range []string{"SESSIONGUARD_ENROLLMENT_TOKEN", "SESSIONGUARD_BROKER_API_KEY", "SESSIONGUARD_OIDC_CLIENT_SECRET", "SESSIONGUARD_ACCESS_OIDC_CLIENT_SECRET"} {
		t.Setenv(k, strings.Repeat("b2", 16)+k)
	}
	dir := t.TempDir()
	c, err := LoadMaster("../../deploy/master.json.example")
	if err != nil {
		t.Fatal(err)
	}
	c.AccessAuth.RevalidateMinutes = -1
	if err := SaveJSON(dir+"/m.json", c); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadMaster(dir + "/m.json"); err == nil || !strings.Contains(err.Error(), "revalidate_minutes") {
		t.Fatalf("negative revalidate_minutes accepted: %v", err)
	}
}
