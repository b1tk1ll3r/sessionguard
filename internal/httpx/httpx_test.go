package httpx

import (
	"net/http/httptest"
	"testing"
)

func TestSameOrigin(t *testing.T) {
	cases := []struct {
		origin, fetchSite string
		want              bool
	}{
		{"https://sessionguard.example.org", "", true},
		{"https://evil.example.org", "", false},
		{"https://evil.example.org://sessionguard.example.org", "", false},
		{"null", "", false},
		{"", "same-origin", true},
		{"", "same-site", false},
		{"", "cross-site", false},
		{"", "", true},
	}
	for _, c := range cases {
		r := httptest.NewRequest("POST", "https://sessionguard.example.org/api/v1/farms", nil)
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		if c.fetchSite != "" {
			r.Header.Set("Sec-Fetch-Site", c.fetchSite)
		}
		if got := SameOrigin(r); got != c.want {
			t.Errorf("origin=%q site=%q: got %v want %v", c.origin, c.fetchSite, got, c.want)
		}
	}
}
