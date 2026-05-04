package hashpoint

import "embed"

// UserDocs holds the German user manual served by the in-app Help tab. The
// embed lives at module root because //go:embed paths cannot traverse
// upwards, so it must reside next to the docs/ directory.
//
//go:embed docs/user/*.md
var UserDocs embed.FS
