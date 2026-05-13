package tagging

import "github.com/dusthoff/hashpoint/internal/storage"

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
