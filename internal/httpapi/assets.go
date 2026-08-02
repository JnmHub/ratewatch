package httpapi

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed webdist
var embeddedWebDist embed.FS

var embeddedFrontendFS = mustSubFS(embeddedWebDist, "webdist")
var embeddedFrontendHandler = http.FileServer(http.FS(embeddedFrontendFS))

func mustSubFS(source fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(source, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
