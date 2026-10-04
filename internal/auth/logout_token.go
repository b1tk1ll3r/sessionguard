package auth

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

type logoutClaims struct {
	SID string
	Sub string
	JTI string
}

// validateLogoutToken checks the OIDC Back-Channel Logout 1.0 specific claims
// of an already signature/issuer/audience-verified logout token.
func validateLogoutToken(tok *oidc.IDToken, now time.Time) (logoutClaims, error) {
	var claims struct {
		SID    string                     `json:"sid"`
		Sub    string                     `json:"sub"`
		Nonce  string                     `json:"nonce"`
		JTI    string                     `json:"jti"`
		IAT    int64                      `json:"iat"`
		Events map[string]json.RawMessage `json:"events"`
	}
	if err := tok.Claims(&claims); err != nil {
		return logoutClaims{}, errors.New("invalid logout_token claims")
	}
	if claims.Nonce != "" || claims.Events == nil {
		return logoutClaims{}, errors.New("invalid logout_token claims")
	}
	if _, ok := claims.Events[backchannelLogoutEvent]; !ok {
		return logoutClaims{}, errors.New("missing backchannel logout event")
	}
	if claims.SID == "" && claims.Sub == "" {
		return logoutClaims{}, errors.New("logout_token has neither sid nor sub")
	}
	if strings.TrimSpace(claims.JTI) == "" {
		return logoutClaims{}, errors.New("logout_token has no jti")
	}
	iat := time.Unix(claims.IAT, 0)
	if claims.IAT == 0 || now.Sub(iat) > 10*time.Minute || iat.Sub(now) > 5*time.Minute {
		return logoutClaims{}, errors.New("logout_token iat outside allowed window")
	}
	return logoutClaims{SID: claims.SID, Sub: claims.Sub, JTI: claims.JTI}, nil
}

// acceptJTI records a logout-token ID and reports whether it was unseen.
// Callers must hold the lock protecting seen.
func acceptJTI(seen map[string]time.Time, jti string, now time.Time) bool {
	for k, exp := range seen {
		if now.After(exp) {
			delete(seen, k)
		}
	}
	if _, ok := seen[jti]; ok {
		return false
	}
	if len(seen) >= maxPendingLogins {
		for k := range seen {
			delete(seen, k)
			break
		}
	}
	seen[jti] = now.Add(15 * time.Minute)
	return true
}
