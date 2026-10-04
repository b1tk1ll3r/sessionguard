package httpx

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func Error(w http.ResponseWriter, status int, msg string) {
	JSON(w, status, map[string]any{"error": msg})
}

// Fail logs the internal error together with a random reference and returns
// only a generic message plus that reference to the client. Database, file
// system and network errors must not be disclosed to (possibly anonymous)
// HTTP clients.
func Fail(w http.ResponseWriter, r *http.Request, status int, public string, err error) {
	ref := Ref()
	log.Printf("http %s %s -> %d ref=%s: %v", r.Method, r.URL.Path, status, ref, err)
	JSON(w, status, map[string]any{"error": public, "ref": ref})
}

// InternalError is Fail with status 500 and a generic message.
func InternalError(w http.ResponseWriter, r *http.Request, err error) {
	Fail(w, r, http.StatusInternalServerError, "internal error", err)
}

// Ref returns a short random reference used to correlate client-visible
// errors with server logs.
func Ref() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func DecodeJSON(r *http.Request, dst any, max int64) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, max))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

// SameOrigin reports whether a browser request originates from the host it
// is sent to. Modern browsers send Origin on every unsafe request and
// Sec-Fetch-Site on all requests; requests carrying neither header are not
// browser-initiated cross-site requests (CLI/API clients) and are accepted.
func SameOrigin(r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" {
		if origin == "null" {
			return false
		}
		u, err := url.Parse(origin)
		return err == nil && u.Host != "" && strings.EqualFold(u.Host, r.Host)
	}
	switch strings.ToLower(r.Header.Get("Sec-Fetch-Site")) {
	case "", "same-origin", "none":
		return true
	default:
		return false
	}
}

// IsJSON reports whether the request declares a JSON body.
func IsJSON(r *http.Request) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mt == "application/json"
}
