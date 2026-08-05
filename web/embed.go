// Package web contains the static frontend embedded in the DELTA binary.
package web

import "embed"

// Files is the Vite output. dist is committed so `go install` builds embed
// the frontend; run `cd web && npm run build` after frontend changes so the
// current assets are embedded.
//
//go:embed all:dist
var Files embed.FS
