package master

import (
	"embed"
	"strings"
)

// The Master console lives in web/ (plain HTML/JS, no build step); shared
// styles and helpers are served by internal/webui below /ui/.
//
//go:embed web/index.html web/app.js
var webFS embed.FS

var (
	masterHTML = strings.ReplaceAll(mustReadWeb("web/index.html"), "{{VERSION}}", Version)
	masterJS   = mustReadWeb("web/app.js")
)

func mustReadWeb(name string) string {
	b, err := webFS.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return string(b)
}
