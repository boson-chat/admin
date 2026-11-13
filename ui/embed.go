// Package ui embeds the built SvelteKit app (ui/build, made by `make ui`).
package ui

import (
	"embed"
	"io/fs"
)

//go:embed all:build
var files embed.FS

// FS returns the built app rooted at its index.html.
func FS() fs.FS {
	sub, err := fs.Sub(files, "build")
	if err != nil {
		panic(err)
	}
	return sub
}
