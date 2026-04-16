// Package web serves the embedded frontend SPA bundle.
//
// At build time the Dockerfile (or `make frontend-build`) copies the Vite
// output from frontend/dist into server/web/dist, where //go:embed picks it
// up and bakes it into the Go binary. A placeholder .gitkeep is committed so
// the embed directive compiles even on a fresh checkout before the frontend
// has been built — in that case Open() returns only the placeholder file and
// the SPA-fallback handler serves index.html (missing) → 404, which is the
// expected dev behavior until `make frontend-build` runs.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// FS returns the embedded frontend/dist tree rooted at "/".
func FS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
