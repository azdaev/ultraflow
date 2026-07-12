//go:build embed

// Package webassets embeds the built React frontend (web/dist) into the binary,
// so a release build is a single self-contained file with no web/dist folder
// alongside it. Build it with `-tags embed` AFTER `npm run build`; without the
// tag the dev stub in embed_dev.go is used and the daemon serves the frontend
// from disk via the -static flag instead.
package webassets

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Assets returns the embedded frontend rooted at dist/, or nil if it couldn't be
// mounted (so the caller falls back to disk / API-only).
func Assets() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil
	}
	return sub
}
