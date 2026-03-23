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
	return http.FileServer(http.FS(sub))
}
