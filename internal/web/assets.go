package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var assets embed.FS

// FS returns the embedded frontend file system rooted at "dist/".
// Mount this with http.FileServer(http.FS(FS())) at /dashboard/.
// Run `cd my-llm-ui && npm run build` to populate dist/ before building
// the binary with a real UI.
func FS() fs.FS {
	sub, err := fs.Sub(assets, "dist")
	if err != nil {
		panic("web: failed to create sub-FS from embedded assets: " + err.Error())
	}
	return sub
}
