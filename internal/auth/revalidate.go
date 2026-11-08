package auth

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Session revalidation with refresh tokens.
//
// Some IdPs (e.g. Caddy Sentinel) offer no OIDC back-channel logout. Without
// it a SessionGuard session would outlive a locked account, a sign-out at the
// IdP or a removed group until its absolute expiry. With revalidate_minutes
// the Master renews every session at the IdP after that interval; the IdP
// re-checks account, login policy and application rules on every refresh and
// refuses the grant when access ended.

var (
	// errSessionEnded: the IdP refused the refresh – the session must end.
	errSessionEnded = errors.New("session ended at the identity provider")
	// errRefreshUnavailable: the IdP could not be asked (network, 5xx, a
	// refresh already running); the caller decides with its grace period.
	errRefreshUnavailable = errors.New("identity provider temporarily unavailable")
)

const (
	refreshTimeout    = 8 * time.Second
	refreshRetryAfter = 30 * time.Second
)

// refreshResult is the outcome of a successful refresh.
type refreshResult struct {
	RefreshToken string
	IDToken      string
	Claims       map[string]any
}

type revalidator struct {
	interval time.Duration
	// refresh exchanges a refresh token; verify checks a refreshed ID token.
	refresh func(ctx context.Context, refreshToken string) (*oauth2.Token, error)
	verify  func(ctx context.Context, rawIDToken string) (*oidc.IDToken, error)

	mu       sync.Mutex
	inflight map[string]bool
	lastFail map[string]time.Time
}

func newRevalidator(minutes int, cfg oauth2.Config, verifier *oidc.IDTokenVerifier, client *http.Client) *revalidator {
	if minutes <= 0 {
		return nil
	}
	return &revalidator{
		interval: time.Duration(minutes) * time.Minute,
		refresh: func(ctx context.Context, rt string) (*oauth2.Token, error) {
			if client != nil {
				ctx = context.WithValue(ctx, oauth2.HTTPClient, client)
			}
			// An already expired token forces the TokenSource to refresh.
			return cfg.TokenSource(ctx, &oauth2.Token{RefreshToken: rt, Expiry: time.Unix(1, 0)}).Token()
		},
		verify:   verifier.Verify,
		inflight: map[string]bool{},
		lastFail: map[string]time.Time{},
	}
}

// due reports whether a session validated at validatedAt must be renewed.
func (v *revalidator) due(validatedAt, now time.Time) bool {
	return v != nil && now.Sub(validatedAt) >= v.interval
}

// grace reports whether a session may still be used although the IdP could
// not be asked: up to twice the interval after the last successful check.
func (v *revalidator) grace(validatedAt, now time.Time) bool {
	return v != nil && now.Sub(validatedAt) < 2*v.interval
}

// acquire reserves the refresh of one session (key). Only one refresh per
// session may be in flight: IdPs with refresh-token rotation (Sentinel) end
// the whole grant when an already used refresh token comes back. Callers
// must re-read the session after acquire – a concurrent request may just
// have rotated its refresh token – and call release afterwards.
func (v *revalidator) acquire(key string) bool {
	now := time.Now()
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.inflight[key] || now.Sub(v.lastFail[key]) < refreshRetryAfter {
		return false
	}
	v.inflight[key] = true
	for k, t := range v.lastFail {
		if now.Sub(t) > refreshRetryAfter {
			delete(v.lastFail, k)
		}
	}
	return true
}

// release ends a refresh started with acquire; err is its result.
func (v *revalidator) release(key string, err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.inflight, key)
	if errors.Is(err, errRefreshUnavailable) {
		v.lastFail[key] = time.Now()
	} else {
		delete(v.lastFail, key)
	}
}

// exchange refreshes a grant and checks that the refreshed ID token belongs
// to subject.
func (v *revalidator) exchange(ctx context.Context, refreshToken, subject string) (refreshResult, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return refreshResult{}, errSessionEnded
	}
	// Detached from the browser request: a client closing its connection
	// must not abort a rotation half-way and lose the new refresh token.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refreshTimeout)
	defer cancel()
	tok, err := v.refresh(ctx, refreshToken)
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) && re.Response != nil && re.Response.StatusCode >= 400 && re.Response.StatusCode < 500 {
			log.Printf("OIDC refresh refused (HTTP %d %s); ending session", re.Response.StatusCode, re.ErrorCode)
			return refreshResult{}, errSessionEnded
		}
		log.Printf("OIDC refresh failed: %v", err)
		return refreshResult{}, errRefreshUnavailable
	}
	raw, _ := tok.Extra("id_token").(string)
	if strings.TrimSpace(raw) == "" {
		// Without a fresh ID token the claims cannot be re-checked.
		log.Printf("OIDC refresh returned no id_token; ending session")
		return refreshResult{}, errSessionEnded
	}
	idt, err := v.verify(ctx, raw)
	if err != nil {
		log.Printf("OIDC refresh returned an invalid id_token: %v", err)
		return refreshResult{}, errSessionEnded
	}
	if idt.Subject != subject {
		log.Printf("OIDC refresh returned subject %q for session of %q; ending session", idt.Subject, subject)
		return refreshResult{}, errSessionEnded
	}
	var claims map[string]any
	if err := idt.Claims(&claims); err != nil {
		return refreshResult{}, errSessionEnded
	}
	rt := tok.RefreshToken
	if rt == "" {
		rt = refreshToken
	}
	return refreshResult{RefreshToken: rt, IDToken: raw, Claims: claims}, nil
}
