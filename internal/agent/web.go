package agent

import (
	"embed"
	"net/http"
	"strings"

	"github.com/example/sessionguard/internal/auth"
	"github.com/example/sessionguard/internal/httpx"
)

// The local Agent console lives in web/ (plain HTML/JS, no build step);
// shared styles and helpers are served by internal/webui below /ui/.
//
//go:embed web/index.html web/app.js
var webFS embed.FS

var (
	agentHTML = strings.ReplaceAll(mustReadWeb("web/index.html"), "{{VERSION}}", Version)
	agentJS   = mustReadWeb("web/app.js")
)

func mustReadWeb(name string) string {
	b, err := webFS.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// meAPI returns the signed-in local administrator for the console header.
func (a *App) meAPI(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r)
	httpx.JSON(w, 200, map[string]any{"user": u})
}
