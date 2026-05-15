package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// TagRepo is a SQL-backed TagRepository.
type TagRepo struct {
	db *sql.DB
}

// NewTagRepo wires a repo around the given DB.
func NewTagRepo(db *sql.DB) *TagRepo {
	return &TagRepo{db: db}
}

const (
	tagColumns = `id, parent_id, name, description, color,
		personio_project_id, personio_activity_id, sync_to_personio, created_at`

	insertTag = `INSERT INTO tags (
		parent_id, name, description, color,
		personio_project_id, personio_activity_id, sync_to_personio
	) VALUES (?, ?, ?, ?, ?, ?, ?)`

	updateTagSQL = `UPDATE tags SET
		parent_id = ?, name = ?, description = ?, color = ?,
		personio_project_id = ?, personio_activity_id = ?, sync_to_personio = ?
		WHERE id = ?`

	deleteTagSQL = `DELETE FROM tags WHERE id = ?`

	selectTagByID  = `SELECT ` + tagColumns + ` FROM tags WHERE id = ?`
	selectAllTags  = `SELECT ` + tagColumns + ` FROM tags ORDER BY parent_id, name`
	selectChildren = `SELECT ` + tagColumns + ` FROM tags WHERE parent_id = ? ORDER BY name`
)

// Create inserts a new tag.
func (r *TagRepo) Create(ctx context.Context, t *Tag) error {
	res, err := r.db.ExecContext(ctx, insertTag,
		nullableInt64(t.ParentID),
		t.Name,
		nullableStringPtr(t.Description),
		nullableStringPtr(t.Color),
		nullableStringPtr(t.PersonioProjectID),
		nullableStringPtr(t.PersonioActivityID),
		boolToInt(t.SyncToPersonio),
	)
	if err != nil {
		return fmt.Errorf("insert tag: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	t.ID = id
	stored, err := r.Get(ctx, id)
	if err == nil && stored != nil {
		t.CreatedAt = stored.CreatedAt
	}
	return nil
}

// Update modifies an existing tag.
func (r *TagRepo) Update(ctx context.Context, t *Tag) error {
	_, err := r.db.ExecContext(ctx, updateTagSQL,
		nullableInt64(t.ParentID),
		t.Name,
		nullableStringPtr(t.Description),
		nullableStringPtr(t.Color),
		nullableStringPtr(t.PersonioProjectID),
		nullableStringPtr(t.PersonioActivityID),
		boolToInt(t.SyncToPersonio),
		t.ID,
	)
	return err
}

// Delete removes a tag (and its children via FK CASCADE).
func (r *TagRepo) Delete(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, deleteTagSQL, id)
	return err
}

// Get fetches a tag by ID.
func (r *TagRepo) Get(ctx context.Context, id int64) (*Tag, error) {
	row := r.db.QueryRowContext(ctx, selectTagByID, id)
	t, err := scanTag(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

// List returns all tags ordered by (parent_id, name).
func (r *TagRepo) List(ctx context.Context) ([]Tag, error) {
	rows, err := r.db.QueryContext(ctx, selectAllTags)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Tag
	for rows.Next() {
		t, err := scanTagRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// EnsureByPath resolves a slash-separated tag-hierarchy path, creating
// any missing nodes along the way. See TagRepository.EnsureByPath for
// the contract. The walk runs in a single transaction so that two
// concurrent callers cannot race and double-create the same node.
func (r *TagRepo) EnsureByPath(ctx context.Context, path string) (*Tag, error) {
	leaf, _, err := r.ensureByPath(ctx, path, TagMetadata{})
	return leaf, err
}

// EnsureByPathWithMetadata is like EnsureByPath but persists Description
// and Color on the leaf when this call is the one that creates it.
// Intermediate nodes are always created bare even on a metadata import;
// only the user-named leaf gets the plugin's hint values.
func (r *TagRepo) EnsureByPathWithMetadata(ctx context.Context, path string, meta TagMetadata) (*Tag, bool, error) {
	return r.ensureByPath(ctx, path, meta)
}

// ensureByPath is the shared implementation. leafMeta is applied to the
// leaf row when (and only when) this call creates the leaf. Returns
// (leaf, createdLeaf, err).
func (r *TagRepo) ensureByPath(ctx context.Context, path string, leafMeta TagMetadata) (*Tag, bool, error) {
	segments, err := normalizeTagPath(path)
	if err != nil {
		return nil, false, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const findChild = `SELECT ` + tagColumns + ` FROM tags
		WHERE name = ? COLLATE NOCASE
		  AND ((? IS NULL AND parent_id IS NULL) OR parent_id = ?)`
	const insertChildBare = `INSERT INTO tags (parent_id, name) VALUES (?, ?)`
	const insertLeafWithMeta = `INSERT INTO tags (parent_id, name, description, color) VALUES (?, ?, ?, ?)`
	const fetchByID = `SELECT ` + tagColumns + ` FROM tags WHERE id = ?`

	var (
		current     *Tag
		createdLeaf bool
	)
	for i, seg := range segments {
		isLeaf := i == len(segments)-1
		var parentArg interface{}
		if current != nil {
			parentArg = current.ID
		}
		row := tx.QueryRowContext(ctx, findChild, seg, parentArg, parentArg)
		t, err := scanTag(row)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, false, fmt.Errorf("lookup segment %q: %w", seg, err)
		}
		if t != nil {
			current = t
			continue
		}

		var parentInsert interface{}
		if current != nil {
			parentInsert = current.ID
		}
		var res sql.Result
		if isLeaf && (leafMeta.Description != "" || leafMeta.Color != "") {
			res, err = tx.ExecContext(ctx, insertLeafWithMeta,
				parentInsert, seg,
				nullableStringPtr(stringPtrOrNil(leafMeta.Description)),
				nullableStringPtr(stringPtrOrNil(leafMeta.Color)),
			)
		} else {
			res, err = tx.ExecContext(ctx, insertChildBare, parentInsert, seg)
		}
		if err != nil {
			return nil, false, fmt.Errorf("insert segment %q: %w", seg, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return nil, false, fmt.Errorf("inserted id: %w", err)
		}
		created, err := scanTag(tx.QueryRowContext(ctx, fetchByID, id))
		if err != nil {
			return nil, false, fmt.Errorf("read created tag %d: %w", id, err)
		}
		current = created
		if isLeaf {
			createdLeaf = true
		}
	}
	if current == nil {
		return nil, false, ErrInvalidTagPath
	}
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit: %w", err)
	}
	return current, createdLeaf, nil
}

// stringPtrOrNil returns &s when s is non-empty, nil otherwise — a tiny
// helper to keep the nil-vs-pointer dance out of the call sites above.
func stringPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// normalizeTagPath splits path on "/" and reduces each segment to the
// form the tags.name CHECK constraint accepts: strip a leading "#",
// drop any non-alphanumeric character, then prefix "#". Empty segments
// (after normalisation) are dropped. Returns ErrInvalidTagPath when no
// usable segments remain.
func normalizeTagPath(path string) ([]string, error) {
	raw := strings.Split(path, "/")
	out := make([]string, 0, len(raw))
	for _, seg := range raw {
		s := strings.TrimSpace(seg)
		s = strings.TrimPrefix(s, "#")
		var sb strings.Builder
		for _, r := range s {
			switch {
			case r >= 'A' && r <= 'Z',
				r >= 'a' && r <= 'z',
				r >= '0' && r <= '9':
				sb.WriteRune(r)
			}
		}
		if sb.Len() == 0 {
			continue
		}
		out = append(out, "#"+sb.String())
	}
	if len(out) == 0 {
		return nil, ErrInvalidTagPath
	}
	return out, nil
}

// Children returns the direct children of the given tag.
func (r *TagRepo) Children(ctx context.Context, parentID int64) ([]Tag, error) {
	rows, err := r.db.QueryContext(ctx, selectChildren, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Tag
	for rows.Next() {
		t, err := scanTagRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func scanTag(row *sql.Row) (*Tag, error) {
	var (
		t        Tag
		parent   sql.NullInt64
		desc     sql.NullString
		color    sql.NullString
		project  sql.NullString
		activity sql.NullString
		sync     int64
	)
	err := row.Scan(&t.ID, &parent, &t.Name, &desc, &color, &project, &activity, &sync, &t.CreatedAt)
	if err != nil {
		return nil, err
	}
	hydrateTag(&t, parent, desc, color, project, activity, sync)
	return &t, nil
}

func scanTagRows(rows *sql.Rows) (*Tag, error) {
	var (
		t        Tag
		parent   sql.NullInt64
		desc     sql.NullString
		color    sql.NullString
		project  sql.NullString
		activity sql.NullString
		sync     int64
	)
	err := rows.Scan(&t.ID, &parent, &t.Name, &desc, &color, &project, &activity, &sync, &t.CreatedAt)
	if err != nil {
		return nil, err
	}
	hydrateTag(&t, parent, desc, color, project, activity, sync)
	return &t, nil
}

func hydrateTag(t *Tag, parent sql.NullInt64, desc, color, project, activity sql.NullString, sync int64) {
	if parent.Valid {
		v := parent.Int64
		t.ParentID = &v
	}
	t.Description = nsToString(desc)
	t.Color = nsToString(color)
	t.PersonioProjectID = nsToString(project)
	t.PersonioActivityID = nsToString(activity)
	t.SyncToPersonio = sync != 0
	t.CreatedAt = t.CreatedAt.UTC()
}
