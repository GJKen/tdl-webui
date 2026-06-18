package web

import (
	"embed"
	"io/fs"
)

//go:embed ui
var uiEmbed embed.FS

// uiFS returns the embedded static UI rooted at the ui directory.
func uiFS() fs.FS {
	sub, err := fs.Sub(uiEmbed, "ui")
	if err != nil {
		panic(err) // embed path is a compile-time constant; this never fails
	}
	return sub
}
