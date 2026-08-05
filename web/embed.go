// Package web contains the static frontend embedded in the DELTA binary.
package web

import "embed"

// Files is the Vite output. Run `cd web && npm run build` before building the
// Go binary so the generated assets are embedded; dist/.gitkeep keeps this
// package buildable in a fresh checkout.
//
//go:embed all:dist
var Files embed.FS
