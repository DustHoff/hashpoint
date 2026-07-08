package app

import (
	"io/fs"
	"strings"
	"testing"

	hashpoint "github.com/dusthoff/hashpoint"
)

// TestHelpPageOrderMatchesEmbeddedDocs guards the two-way consistency between
// the embedded docs/user/*.md files and helpPageOrder. helpPageOrder is the
// source of truth for the Help-tab sidebar: a markdown file that is not listed
// is invisible in-app, and a listed slug without a matching file makes
// ListUserDocs skip it and GetUserDoc reject it. Either drift is a bug — it is
// exactly how rufbereitschaft.md was once orphaned — so fail loudly instead of
// shipping a doc nobody can reach.
func TestHelpPageOrderMatchesEmbeddedDocs(t *testing.T) {
	entries, err := fs.ReadDir(hashpoint.UserDocs, "docs/user")
	if err != nil {
		t.Fatalf("read embedded docs/user: %v", err)
	}

	onDisk := make(map[string]bool, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") {
			continue
		}
		onDisk[strings.TrimSuffix(name, ".md")] = true
	}

	listed := make(map[string]bool, len(helpPageOrder))
	for _, slug := range helpPageOrder {
		if listed[slug] {
			t.Errorf("helpPageOrder lists %q more than once", slug)
		}
		listed[slug] = true
		if !onDisk[slug] {
			t.Errorf("helpPageOrder has %q but docs/user/%s.md does not exist", slug, slug)
		}
	}

	for slug := range onDisk {
		if !listed[slug] {
			t.Errorf("docs/user/%s.md is embedded but missing from helpPageOrder — it would be invisible in the Help tab", slug)
		}
	}
}
