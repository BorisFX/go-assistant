package httpapi

import (
	"io/fs"
	"net/http"
	"strings"
)

func SPAHandler(staticFS fs.FS) http.HandlerFunc {
	fileServer := http.FileServer(http.FS(staticFS))

	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		if strings.HasPrefix(path, "/api/") {
			http.NotFound(w, r)
			return
		}

		f, err := staticFS.Open(strings.TrimPrefix(path, "/"))
		if err != nil {
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
			return
		}
		f.Close()

		fileServer.ServeHTTP(w, r)
	}
}
