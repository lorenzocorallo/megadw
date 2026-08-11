// Package webui exposes the embedded frontend distribution.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// dist contains the production frontend copied by the root build.
//
//go:embed dist/*
var dist embed.FS

// Handler serves static assets and falls back to index.html for client-side
// routes owned by TanStack Router.
func Handler() (http.Handler, error) {
	files, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, err
	}

	return spaHandler{files: files}, nil
}

type spaHandler struct {
	files fs.FS
}

func (h spaHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	requested := strings.TrimPrefix(path.Clean("/"+request.URL.Path), "/")
	if requested == "" || requested == "." {
		requested = "index.html"
	}
	if _, err := fs.Stat(h.files, requested); err != nil {
		if strings.HasPrefix(requested, "assets/") {
			http.NotFound(writer, request)
			return
		}
		requested = "index.html"
	}
	if requested == "index.html" {
		writer.Header().Set("Cache-Control", "no-cache")
	} else if strings.HasPrefix(requested, "assets/") {
		writer.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}

	clone := request.Clone(request.Context())
	if requested == "index.html" {
		clone.URL.Path = "/"
	} else {
		clone.URL.Path = "/" + requested
	}
	http.FileServer(http.FS(h.files)).ServeHTTP(writer, clone)
}
