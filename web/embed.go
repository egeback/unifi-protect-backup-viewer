// Package web embeds the static frontend (HTML/CSS/JS) into the Go binary
// so the app ships as a single container with no separate build step.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var embedded embed.FS

// Static returns the embedded frontend, rooted at what was "static/" on disk.
func Static() fs.FS {
	sub, err := fs.Sub(embedded, "static")
	if err != nil {
		panic(err)
	}
	return sub
}
