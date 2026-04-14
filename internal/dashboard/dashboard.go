package dashboard

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static/*
var content embed.FS

func Handler() http.Handler {
	sub, _ := fs.Sub(content, "static")
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		fileServer.ServeHTTP(w, r)
	})
}
