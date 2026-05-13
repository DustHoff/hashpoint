package oncall

import (
	"context"

	"github.com/onesi/hashpoint/internal/storage"
)

// TagRepoAncestry adapts a storage.TagRepository to TagAncestry by walking
// each tag's parent chain. Wrapping the repo here (rather than adding an
// AncestorsOf method to TagRepository) keeps the plugin package free of
// new methods on the shared storage interface — only this adapter knows
// about ancestor walks today.
//
// Loops in the parent chain are defended against with a depth cap; a
// healthy hierarchy is small, so 32 is plenty.
type TagRepoAncestry struct {
	Tags storage.TagRepository
}

// AncestorsOf returns tagID itself plus every parent walking up the tree.
// Stops at the root (ParentID nil) or after maxDepth steps as a guard
// against pathological data.
func (a TagRepoAncestry) AncestorsOf(ctx context.Context, tagID int64) ([]int64, error) {
	const maxDepth = 32
	out := []int64{tagID}
	current := tagID
	for i := 0; i < maxDepth; i++ {
		t, err := a.Tags.Get(ctx, current)
		if err != nil {
			return out, err
		}
		if t == nil || t.ParentID == nil {
			return out, nil
		}
		out = append(out, *t.ParentID)
		current = *t.ParentID
	}
	return out, nil
}
