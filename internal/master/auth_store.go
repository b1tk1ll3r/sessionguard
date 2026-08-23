package master

import (
	"strings"
	"time"

	"github.com/example/sessionguard/internal/model"
)

// authSessionStore adapts the Master's existing persistent control-plane store
// to the auth package. Auth session mutations are rare (login/logout/revoke), so
// persisting the small map alongside control-plane state avoids a separate
// database dependency while verification itself stays read-only and cheap.
type authSessionStore struct{ s *store }

func (r authSessionStore) PutAuthSession(sess model.AuthSession) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if r.s.data.AuthSessions == nil {
		r.s.data.AuthSessions = map[string]model.AuthSession{}
	}
	r.s.data.AuthSessions[sess.TokenHash] = sess
	return r.s.saveLocked()
}

func (r authSessionStore) GetAuthSession(hash string) (model.AuthSession, bool) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	sess, ok := r.s.data.AuthSessions[hash]
	if !ok || (!sess.ExpiresAt.IsZero() && time.Now().UTC().After(sess.ExpiresAt)) {
		return model.AuthSession{}, false
	}
	return sess, true
}

func (r authSessionStore) DeleteAuthSession(hash string) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if _, ok := r.s.data.AuthSessions[hash]; !ok {
		return nil
	}
	delete(r.s.data.AuthSessions, hash)
	return r.s.saveLocked()
}

func (r authSessionStore) RevokeAuthSessions(sid, sub string) (int, error) {
	sid = strings.TrimSpace(sid)
	sub = strings.TrimSpace(sub)
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	n := 0
	for h, sess := range r.s.data.AuthSessions {
		if (sid != "" && sess.SID == sid) || (sub != "" && sess.Subject == sub) {
			delete(r.s.data.AuthSessions, h)
			n++
		}
	}
	if n == 0 {
		return 0, nil
	}
	return n, r.s.saveLocked()
}

func (r authSessionStore) CleanupAuthSessions(now time.Time) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	changed := false
	for h, sess := range r.s.data.AuthSessions {
		if !sess.ExpiresAt.IsZero() && !now.Before(sess.ExpiresAt) {
			delete(r.s.data.AuthSessions, h)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return r.s.saveLocked()
}
