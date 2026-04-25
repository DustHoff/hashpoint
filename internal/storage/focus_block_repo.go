package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// FocusBlockRepo is a SQL-backed FocusBlockRepository.
type FocusBlockRepo struct {
	db *sql.DB
}

// NewFocusBlockRepo wires a repo around the given DB handle.
func NewFocusBlockRepo(db *sql.DB) *FocusBlockRepo {
	return &FocusBlockRepo{db: db}
}

const (
	focusColumns = `id, process_name, process_path, window_title, start_time, end_time,
		duration_sec, is_idle, tag_id, auto_tagged, description, personio_id, synced_at,
		is_placeholder`

	insertFocusBlock = `INSERT INTO focus_blocks (
		process_name, process_path, window_title, start_time, end_time,
		duration_sec, is_idle, tag_id, auto_tagged, description, is_placeholder
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	closeFocusBlock = `UPDATE focus_blocks
		SET end_time = ?, duration_sec = ?
		WHERE id = ? AND end_time IS NULL`

	markIdleFocusBlock = `UPDATE focus_blocks
		SET end_time = ?, duration_sec = ?, is_idle = 1
		WHERE id = ? AND end_time IS NULL`

	selectLastOpen = `SELECT ` + focusColumns + ` FROM focus_blocks
		WHERE end_time IS NULL
		ORDER BY start_time DESC LIMIT 1`

	selectByID = `SELECT ` + focusColumns + ` FROM focus_blocks WHERE id = ?`

	selectBetween = `SELECT ` + focusColumns + ` FROM focus_blocks
		WHERE start_time >= ? AND start_time < ?
		ORDER BY start_time ASC`

	updateTag = `UPDATE focus_blocks SET tag_id = ?, auto_tagged = ? WHERE id = ?`

	updateDescription = `UPDATE focus_blocks SET description = ? WHERE id = ?`

	markSynced = `UPDATE focus_blocks SET personio_id = ?, synced_at = ? WHERE id = ?`

	deleteBlock = `DELETE FROM focus_blocks WHERE id = ?`

	updateBlock = `UPDATE focus_blocks
		SET process_name = ?, process_path = ?, window_title = ?,
		    start_time = ?, end_time = ?, duration_sec = ?,
		    is_idle = ?, tag_id = ?, auto_tagged = ?, description = ?,
		    is_placeholder = ?
		WHERE id = ?`
)

// Open starts a new focus block.
func (r *FocusBlockRepo) Open(ctx context.Context, b *FocusBlock) error {
	res, err := r.db.ExecContext(ctx, insertFocusBlock,
		b.ProcessName,
		nullableString(b.ProcessPath),
		b.WindowTitle,
		b.StartTime.UTC(),
		nullableTime(b.EndTime),
		b.DurationSec,
		boolToInt(b.IsIdle),
		nullableInt64(b.TagID),
		boolToInt(b.AutoTagged),
		nullableStringPtr(b.Description),
		boolToInt(b.IsPlaceholder),
	)
	if err != nil {
		return fmt.Errorf("insert block: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	b.ID = id
	return nil
}

// Close finalizes the open block.
func (r *FocusBlockRepo) Close(ctx context.Context, id int64, end time.Time) error {
	return r.finalize(ctx, closeFocusBlock, id, end)
}

// MarkIdle finalizes the block as an idle block.
func (r *FocusBlockRepo) MarkIdle(ctx context.Context, id int64, end time.Time) error {
	return r.finalize(ctx, markIdleFocusBlock, id, end)
}

func (r *FocusBlockRepo) finalize(ctx context.Context, query string, id int64, end time.Time) error {
	end = end.UTC()
	b, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	if b == nil {
		return ErrNotFound
	}
	dur := int64(end.Sub(b.StartTime).Round(time.Second).Seconds())
	if dur < 0 {
		dur = 0
	}
	res, err := r.db.ExecContext(ctx, query, end, dur, id)
	if err != nil {
		return fmt.Errorf("finalize block: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		// Already closed; not a hard error.
		return nil
	}
	return nil
}

// LastOpen returns the most recently started open block, or nil if none.
func (r *FocusBlockRepo) LastOpen(ctx context.Context) (*FocusBlock, error) {
	row := r.db.QueryRowContext(ctx, selectLastOpen)
	b, err := scanFocusBlock(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return b, nil
}

// ListByDay returns blocks whose start_time falls on day (UTC).
func (r *FocusBlockRepo) ListByDay(ctx context.Context, day time.Time) ([]FocusBlock, error) {
	from := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	return r.ListBetween(ctx, from, to)
}

// ListBetween returns blocks in [from, to).
func (r *FocusBlockRepo) ListBetween(ctx context.Context, from, to time.Time) ([]FocusBlock, error) {
	rows, err := r.db.QueryContext(ctx, selectBetween, from.UTC(), to.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FocusBlock
	for rows.Next() {
		b, err := scanFocusBlockRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

// SetTag assigns or clears the tag for a block.
func (r *FocusBlockRepo) SetTag(ctx context.Context, id int64, tagID *int64, autoTagged bool) error {
	_, err := r.db.ExecContext(ctx, updateTag, nullableInt64(tagID), boolToInt(autoTagged), id)
	return err
}

// SetDescription writes the per-block activity description (nil/empty = clear).
func (r *FocusBlockRepo) SetDescription(ctx context.Context, id int64, description *string) error {
	_, err := r.db.ExecContext(ctx, updateDescription, nullableStringPtr(description), id)
	return err
}

// MarkSynced records the Personio attendance ID for the block.
func (r *FocusBlockRepo) MarkSynced(ctx context.Context, id int64, personioID string, at time.Time) error {
	_, err := r.db.ExecContext(ctx, markSynced, personioID, at.UTC(), id)
	return err
}

// Split splits the block into two at the given time. The original is closed
// at `at`, a new block with the same process/title is opened from `at` to the
// original end time. Returns the new (right) block.
func (r *FocusBlockRepo) Split(ctx context.Context, id int64, at time.Time) (*FocusBlock, error) {
	at = at.UTC()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRowContext(ctx, selectByID, id)
	b, err := scanFocusBlock(row)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, ErrNotFound
	}
	if !at.After(b.StartTime) {
		return nil, errors.New("split point must be after start_time")
	}
	if b.EndTime != nil && !at.Before(*b.EndTime) {
		return nil, errors.New("split point must be before end_time")
	}

	leftDur := int64(at.Sub(b.StartTime).Round(time.Second).Seconds())
	if _, err := tx.ExecContext(ctx, `UPDATE focus_blocks SET end_time = ?, duration_sec = ? WHERE id = ?`, at, leftDur, id); err != nil {
		return nil, err
	}

	right := *b
	right.ID = 0
	right.StartTime = at
	if b.EndTime != nil {
		end := b.EndTime.UTC()
		right.EndTime = &end
		right.DurationSec = int64(end.Sub(at).Round(time.Second).Seconds())
	} else {
		right.EndTime = nil
		right.DurationSec = 0
	}
	res, err := tx.ExecContext(ctx, insertFocusBlock,
		right.ProcessName,
		nullableString(right.ProcessPath),
		right.WindowTitle,
		right.StartTime,
		nullableTime(right.EndTime),
		right.DurationSec,
		boolToInt(right.IsIdle),
		nullableInt64(right.TagID),
		boolToInt(right.AutoTagged),
		nullableStringPtr(right.Description),
		boolToInt(right.IsPlaceholder),
	)
	if err != nil {
		return nil, err
	}
	newID, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	right.ID = newID

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &right, nil
}

// Update writes the editable fields of a block back.
func (r *FocusBlockRepo) Update(ctx context.Context, b *FocusBlock) error {
	_, err := r.db.ExecContext(ctx, updateBlock,
		b.ProcessName,
		nullableString(b.ProcessPath),
		b.WindowTitle,
		b.StartTime.UTC(),
		nullableTime(b.EndTime),
		b.DurationSec,
		boolToInt(b.IsIdle),
		nullableInt64(b.TagID),
		boolToInt(b.AutoTagged),
		nullableStringPtr(b.Description),
		boolToInt(b.IsPlaceholder),
		b.ID,
	)
	return err
}

// Delete removes a block.
func (r *FocusBlockRepo) Delete(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, deleteBlock, id)
	return err
}

// Get fetches a single block.
func (r *FocusBlockRepo) Get(ctx context.Context, id int64) (*FocusBlock, error) {
	row := r.db.QueryRowContext(ctx, selectByID, id)
	b, err := scanFocusBlock(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return b, err
}

func scanFocusBlock(row *sql.Row) (*FocusBlock, error) {
	var b FocusBlock
	var (
		processPath   sql.NullString
		end           sql.NullTime
		tagID         sql.NullInt64
		description   sql.NullString
		personioID    sql.NullString
		syncedAt      sql.NullTime
		isIdle        int64
		autoTagged    int64
		isPlaceholder int64
	)
	err := row.Scan(
		&b.ID, &b.ProcessName, &processPath, &b.WindowTitle, &b.StartTime, &end,
		&b.DurationSec, &isIdle, &tagID, &autoTagged, &description, &personioID, &syncedAt,
		&isPlaceholder,
	)
	if err != nil {
		return nil, err
	}
	hydrate(&b, processPath, end, tagID, description, personioID, syncedAt, isIdle, autoTagged, isPlaceholder)
	return &b, nil
}

func scanFocusBlockRows(rows *sql.Rows) (*FocusBlock, error) {
	var b FocusBlock
	var (
		processPath   sql.NullString
		end           sql.NullTime
		tagID         sql.NullInt64
		description   sql.NullString
		personioID    sql.NullString
		syncedAt      sql.NullTime
		isIdle        int64
		autoTagged    int64
		isPlaceholder int64
	)
	err := rows.Scan(
		&b.ID, &b.ProcessName, &processPath, &b.WindowTitle, &b.StartTime, &end,
		&b.DurationSec, &isIdle, &tagID, &autoTagged, &description, &personioID, &syncedAt,
		&isPlaceholder,
	)
	if err != nil {
		return nil, err
	}
	hydrate(&b, processPath, end, tagID, description, personioID, syncedAt, isIdle, autoTagged, isPlaceholder)
	return &b, nil
}

func hydrate(b *FocusBlock, processPath sql.NullString, end sql.NullTime, tagID sql.NullInt64,
	description, personioID sql.NullString, syncedAt sql.NullTime, isIdle, autoTagged, isPlaceholder int64) {
	if processPath.Valid {
		b.ProcessPath = processPath.String
	}
	if end.Valid {
		t := end.Time.UTC()
		b.EndTime = &t
	}
	if tagID.Valid {
		v := tagID.Int64
		b.TagID = &v
	}
	if description.Valid {
		v := description.String
		b.Description = &v
	}
	if personioID.Valid {
		v := personioID.String
		b.PersonioID = &v
	}
	if syncedAt.Valid {
		t := syncedAt.Time.UTC()
		b.SyncedAt = &t
	}
	b.IsIdle = isIdle != 0
	b.AutoTagged = autoTagged != 0
	b.IsPlaceholder = isPlaceholder != 0
	b.StartTime = b.StartTime.UTC()
}
