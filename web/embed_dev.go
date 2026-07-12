//go:build !embed

package webassets

import "io/fs"

// Assets returns nil in a dev build: the frontend is served from disk (the
// -static flag) rather than embedded, so `go build`/`go run` work without a
// prebuilt web/dist. Build with `-tags embed` to bundle the frontend instead.
func Assets() fs.FS { return nil }
