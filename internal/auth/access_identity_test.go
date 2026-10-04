package auth

import (
	"testing"

	"github.com/example/sessionguard/internal/model"
)

func TestCheckUsername(t *testing.T) {
	m := &AccessManager{cfg: model.AccessAuthConfig{ReservedUsernames: []string{"guacadmin", "root"}}}
	for _, ok := range []string{"max.mustermann", "m.muster@example.org", "user_1", "A-b"} {
		if err := m.checkUsername(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"guacadmin", "GuacAdmin", "root", "", " max", "max ", "guаcadmin", "max\x00", "-max", "a/b", "max\nmustermann"} {
		if err := m.checkUsername(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
