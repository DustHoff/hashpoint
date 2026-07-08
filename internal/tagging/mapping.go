package tagging

import (
	"strings"

	"github.com/dusthoff/hashpoint/internal/storage"
)

// EffectiveMapping is the resolved Personio mapping for a tag — the sub-tag's
// project/activity if set, otherwise inherited from the parent.
type EffectiveMapping struct {
	ParentName     string
	SubName        string // empty if the tag itself is a parent.
	SubDescription string
	ProjectID      string
	ActivityID     string
	SyncToPersonio bool
}

// Resolve walks the parent chain and returns the effective Personio mapping
// for the given tag. It is safe to call on parent-only tags.
//
// Inheritance rules (per spec §2.4):
//   - Sub-tag mapping overrides parent mapping per field — a sub-tag with
//     project_id set but activity_id empty inherits the parent's activity_id.
//   - sync_to_personio falls back to parent if not explicitly set on sub-tag.
func Resolve(tag storage.Tag, byID map[int64]storage.Tag) EffectiveMapping {
	if tag.ParentID == nil {
		return EffectiveMapping{
			ParentName:     tag.Name,
			ProjectID:      strDeref(tag.PersonioProjectID),
			ActivityID:     strDeref(tag.PersonioActivityID),
			SyncToPersonio: tag.SyncToPersonio,
		}
	}
	parent, ok := byID[*tag.ParentID]
	if !ok {
		return EffectiveMapping{
			ParentName:     "",
			SubName:        tag.Name,
			SubDescription: strDeref(tag.Description),
			ProjectID:      strDeref(tag.PersonioProjectID),
			ActivityID:     strDeref(tag.PersonioActivityID),
			SyncToPersonio: tag.SyncToPersonio,
		}
	}
	mapping := EffectiveMapping{
		ParentName:     parent.Name,
		SubName:        tag.Name,
		SubDescription: strDeref(tag.Description),
		ProjectID:      strDeref(tag.PersonioProjectID),
		ActivityID:     strDeref(tag.PersonioActivityID),
		SyncToPersonio: tag.SyncToPersonio,
	}
	if mapping.ProjectID == "" {
		mapping.ProjectID = strDeref(parent.PersonioProjectID)
	}
	if mapping.ActivityID == "" {
		mapping.ActivityID = strDeref(parent.PersonioActivityID)
	}
	return mapping
}

// BuildComment composes the Personio comment per spec §2.5:
//
//	"<parent_name> <sub_name> <sub_description>"
//
// Empty parts are skipped (no double spaces, no trailing separator).
func (m EffectiveMapping) BuildComment() string {
	parts := make([]string, 0, 3)
	if m.ParentName != "" {
		parts = append(parts, m.ParentName)
	}
	if m.SubName != "" {
		parts = append(parts, m.SubName)
	}
	if m.SubDescription != "" {
		parts = append(parts, m.SubDescription)
	}
	return joinNonEmpty(parts, " ")
}

// ParseComment is the inverse of BuildComment: it splits a Personio comment
// into its parent tag, sub tag and the remaining free-text description.
//
// Leading whitespace-separated tokens that satisfy the hashtag schema
// (^#[A-Za-z0-9]+$) are read as the parent (first) and sub (second) tag;
// scanning stops at the first non-hashtag token or after two tags. A single
// standalone separator token ("—" or "-") immediately following the tags is
// dropped, and the remainder is the description. When the comment has no
// leading hashtag, parent and sub are empty and description is the trimmed
// comment — so free-text or manually entered comments survive verbatim.
//
// Note: a sub-tag's own Description, which BuildComment appends before the
// block description, is not distinguished from the block description here; it
// falls into description and is reconstructed from the resolved tag on
// re-export.
func ParseComment(comment string) (parent, sub, description string) {
	fields := strings.Fields(comment)
	i := 0
	for i < len(fields) && i < 2 && IsValidName(fields[i]) {
		i++
	}
	if i > 0 {
		parent = fields[0]
	}
	if i > 1 {
		sub = fields[1]
	}
	rest := fields[i:]
	if len(rest) > 0 && (rest[0] == "—" || rest[0] == "-") {
		rest = rest[1:]
	}
	description = strings.Join(rest, " ")
	return parent, sub, description
}

func joinNonEmpty(parts []string, sep string) string {
	out := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if out == "" {
			out = p
		} else {
			out += sep + p
		}
	}
	return out
}

func strDeref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
