// Package hashpoint exposes the embedded frontend asset bundle. It lives at
// the module root because //go:embed paths cannot traverse upwards, so the
// directive must reside next to the frontend/ directory.
package hashpoint

import "embed"

// Frontend holds the built Wails frontend served by the asset server.
//
//go:embed all:frontend/dist
var Frontend embed.FS
