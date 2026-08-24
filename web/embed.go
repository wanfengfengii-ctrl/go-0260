// Package web serves the embedded joint-inspection console assets. The
// directory also carries the browser console source (index.html, app.js,
// styles.css), its package.json, package-lock.json and deterministic build
// script so the frontend is a real, buildable npm project.
package web

import (
	"embed"
	"net/http"
)

// Assets holds the compiled frontend source files. The npm build (npm ci &&
// npm run build) is deterministic and produces these assets; the Go binary
// serves them directly so no separate static host is required.
//
//go:embed index.html app.js styles.css
var Assets embed.FS

// Handler returns an http.Handler serving the embedded frontend.
func Handler() http.Handler {
	return http.FileServer(http.FS(Assets))
}
