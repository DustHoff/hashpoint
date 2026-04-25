package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
