package oncall

import (
	"context"
	"errors"
	"fmt"

	"github.com/dusthoff/hashpoint/internal/config"
	"github.com/dusthoff/hashpoint/internal/storage"
)

// Recheck reconciles the OnCall doc for a single block against the current
// qualification state. Called from every code path that mutates a tag
// block (orchestrator block-close, App.SetTagBlockTag, App.ResizeTagBlock).
//
// State machine:
//
//   - no doc     + qualifies     → EnsureForBlock (status defaults to draft)
//   - draft doc  + qualifies     → ClearStale if previously marked stale
//   - draft doc  + !qualifies    → MarkStale (the user is asked via banner
//     before the doc is actually deleted —
//     see App.OnCallDocDismiss)
//   - submitted/partial/failed   → never touched (it's history; the remote
//     ticket still exists even if Hashpoint's
//     view drifts)
//
// Returns nil on success, including the "nothing to do" cases. The caller
// is expected to log failures but not abort the surrounding operation.
func Recheck(ctx context.Context, block storage.TagBlock, ws config.WorkScheduleConfig, onCallTagIDs []int64, tags TagAncestry, repo storage.OnCallRepository) error {
	qualifies, err := Qualifies(ctx, block, ws, onCallTagIDs, tags)
	if err != nil {
		return fmt.Errorf("oncall recheck qualify: %w", err)
	}
	existing, err := repo.GetByBlock(ctx, block.ID)
	hasDoc := err == nil
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return fmt.Errorf("oncall recheck get: %w", err)
	}

	switch {
	case !hasDoc && qualifies:
		// First time this block qualifies — create the draft.
		if _, err := repo.EnsureForBlock(ctx, block.ID, block.TagID); err != nil {
			return fmt.Errorf("oncall recheck ensure: %w", err)
		}
	case hasDoc && !qualifies:
		// Block drifted out (re-tag, resize). Never delete — let the user
		// dismiss via the UI. Skip rows that are already past draft state.
		if existing.Status() == storage.OnCallStatusDraft && !existing.Stale {
			if err := repo.MarkStale(ctx, existing.ID); err != nil {
				return fmt.Errorf("oncall recheck mark stale: %w", err)
			}
		}
	case hasDoc && qualifies:
		// Block drifted back in. Clear the stale banner if it was set.
		if existing.Stale {
			if err := repo.ClearStale(ctx, existing.ID); err != nil {
				return fmt.Errorf("oncall recheck clear stale: %w", err)
			}
		}
	}
	return nil
}
