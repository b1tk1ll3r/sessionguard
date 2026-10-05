package master

import (
	"regexp"
	"strings"
	"testing"

	"github.com/example/sessionguard/internal/webui"
)

// The console HTML must have the version substituted, reference only assets
// that exist and contain no inline script (CSP script-src 'self').
func TestMasterPageAssets(t *testing.T) {
	if strings.Contains(masterHTML, "{{VERSION}}") || !strings.Contains(masterHTML, Version) {
		t.Fatal("version placeholder not substituted")
	}
	if regexp.MustCompile(`<script>`).MatchString(masterHTML) || strings.Contains(masterHTML, " onclick=") {
		t.Fatal("inline script found")
	}
	for _, m := range regexp.MustCompile(`(?:src|href)="/ui/([^"?]+)`).FindAllStringSubmatch(masterHTML, -1) {
		if webui.ContentType(m[1]) == "application/octet-stream" {
			t.Fatalf("unexpected asset type %s", m[1])
		}
	}
	if len(masterJS) < 1000 {
		t.Fatal("app.js not embedded")
	}
}
