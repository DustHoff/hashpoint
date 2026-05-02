package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// TagBlockRepo is a SQL-backed TagBlockRepository.
type TagBlockRepo struct {
	db *sql.DB
}

// NewTagBlockRepo wires a repo around the given DB handle.
func NewTagBlockRepo(db *sql.DB) *TagBlockRepo {
	return &TagBlockRepo{db: db}
}

const (
	tagBlockColumns = `id, tag_id, description, start_time, end_time, duration_sec,
		is_manual, personio_id, synced_at`

	insertTagBlock = `INSERT INTO tag_blocks (
		tag_id, description, start_time, end_time, duration_sec, is_manual,
		personio_id, synced_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

	closeTagBlock = `UPDATE tag_blocks
		SET end_time = ?, duration_sec = ?
		WHERE id = ? AND end_time IS NULL`

	setTagBlockEnd = `UPDATE tag_blocks
		SET end_time = ?, duration_sec = ?
		WHERE id = ?`

	setTagBlockStart = `UPDATE tag_blocks
		SET start_time = ?, duration_sec = ?
		WHERE id = ?`

	updateTagBlockTag = `UPDATE tag_blocks SET tag_id = ? WHERE id = ?`

	updateTagBlockDescription = `UPDATE tag_blocks SET description = ? WHERE id = ?`

	markTagBlockSynced = `UPDATE tag_blocks SET personio_id = ?, synced_at = ? WHERE id = ?`

	selectTagBlockByID = `SELECT ` + tagBlockColumns + ` FROM tag_blocks WHERE id = ?`

	selectTagBlockLastOpen = `SELECT ` + tagBlockColumns + ` FROM tag_blocks
		WHERE end_time IS NULL
		ORDER BY start_time DESC LIMIT 1`

	selectTagBlockAllOpen = `SELECT ` + tagBlockColumns + ` FROM tag_blocks
		WHERE end_time IS NULL
		ORDER BY start_time ASC`

	selectTagBlockOpenManual = `SELECT ` + tagBlockColumns + ` FROM tag_blocks
		WHERE end_time IS NULL AND is_manual = 1
		ORDER BY start_time ASC`

	selectTagBlockBetween = `SELECT ` + tagBlockColumns + ` FROM tag_blocks
		WHERE start_time >= ? AND start_time < ?
		ORDER BY start_time ASC`

	selectTagBlockOverlapping = `SELECT ` + tagBlockColumns + ` FROM tag_blocks
		WHERE start_time < ?
		  AND (end_time IS NULL OR end_time > ?)
		ORDER BY start_time ASC`

	selectTagBlockOverlap = `SELECT id FROM tag_blocks
		WHERE id != ?
		  AND start_time < ?
		  AND (end_time IS NULL OR end_time > ?)
		ORDER BY start_time ASC LIMIT 1`

	deleteTagBlockSQL = `DELETE FROM tag_blocks WHERE id = ?`

	selectRecentlyUsedTagIDs = `SELECT tag_id
		FROM tag_blocks
		WHERE start_time >= ?
		GROUP BY tag_id
		ORDER BY MAX(start_time) DESC
		LIMIT ?`
)

// Open inserts a new tag block.
func (r *TagBlockRepo) Open(ctx context.Context, b *TagBlock) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := assertNoTagOverlapTx(ctx, tx, 0, b.StartTime.UTC(), b.EndTime); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, insertTagBlock,
		b.TagID,
		nullableStringPtr(b.Description),
		b.StartTime.UTC(),
		nullableTime(b.EndTime),
		b.DurationSec,
		boolToInt(b.IsManual),
		nullableStringPtr(b.PersonioID),
		nullableTime(b.SyncedAt),
	)
	if err != nil {
		return fmt.Errorf("insert tag block: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	b.ID = id
	return nil
}

// Close finalizes an open tag block. Refuses if the close would re-introduce
// an overlap with another block opened in the meantime.
func (r *TagBlockRepo) Close(ctx context.Context, id int64, end time.Time) error {
	return r.finalize(ctx, closeTagBlock, id, end, true)
}

// SetEnd updates the end time on any tag block (open or closed).
func (r *TagBlockRepo) SetEnd(ctx context.Context, id int64, end time.Time) error {
	return r.finalize(ctx, setTagBlockEnd, id, end, false)
}

func (r *TagBlockRepo) finalize(ctx context.Context, query string, id int64, end time.Time, requireOpen bool) error {
	end = end.UTC()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRowContext(ctx, selectTagBlockByID, id)
	b, err := scanTagBlockRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if b == nil {
		return ErrNotFound
	}
	if requireOpen && b.EndTime != nil {
		return fmt.Errorf("tag block %d already closed", id)
	}
	if end.Before(b.StartTime) {
		end = b.StartTime
	}
	if err := assertNoTagOverlapTx(ctx, tx, id, b.StartTime, &end); err != nil {
		return err
	}
	dur := int64(end.Sub(b.StartTime).Round(time.Second).Seconds())
	if dur < 0 {
		dur = 0
	}
	if _, err := tx.ExecContext(ctx, query, end, dur, id); err != nil {
		return fmt.Errorf("finalize tag block: %w", err)
	}
	return tx.Commit()
}

// SetStart shrinks a tag block by moving its start_time forward.
func (r *TagBlockRepo) SetStart(ctx context.Context, id int64, start time.Time) error {
	start = start.UTC()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRowContext(ctx, selectTagBlockByID, id)
	b, err := scanTagBlockRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if b == nil {
		return ErrNotFound
	}
	if b.EndTime != nil && !start.Before(*b.EndTime) {
		return fmt.Errorf("start must be before end_time")
	}
	if err := assertNoTagOverlapTx(ctx, tx, id, start, b.EndTime); err != nil {
		return err
	}
	dur := int64(0)
	if b.EndTime != nil {
		dur = int64(b.EndTime.Sub(start).Round(time.Second).Seconds())
		if dur < 0 {
			dur = 0
		}
	}
	if _, err := tx.ExecContext(ctx, setTagBlockStart, start, dur, id); err != nil {
		return fmt.Errorf("set tag start: %w", err)
	}
	return tx.Commit()
}

// SetTag re-points a tag block to a different tag.
func (r *TagBlockRepo) SetTag(ctx context.Context, id, tagID int64) error {
	if _, err := r.db.ExecContext(ctx, updateTagBlockTag, tagID, id); err != nil {
		return fmt.Errorf("update tag: %w", err)
	}
	return nil
}

// SetDescription writes the activity description.
func (r *TagBlockRepo) SetDescription(ctx context.Context, id int64, description *string) error {
	_, err := r.db.ExecContext(ctx, updateTagBlockDescription, nullableStringPtr(description), id)
	return err
}

// MarkSynced records the Personio attendance ID.
func (r *TagBlockRepo) MarkSynced(ctx context.Context, id int64, personioID string, at time.Time) error {
	_, err := r.db.ExecContext(ctx, markTagBlockSynced, personioID, at.UTC(), id)
	return err
}

// LastOpen returns the most recently started open tag block.
func (r *TagBlockRepo) LastOpen(ctx context.Context) (*TagBlock, error) {
	row := r.db.QueryRowContext(ctx, selectTagBlockLastOpen)
	b, err := scanTagBlockRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return b, err
}

// ListOpen returns every open tag block.
func (r *TagBlockRepo) ListOpen(ctx context.Context) ([]TagBlock, error) {
	return r.queryList(ctx, selectTagBlockAllOpen)
}

// ListOpenManual returns every open-ended manual tag block.
func (r *TagBlockRepo) ListOpenManual(ctx context.Context) ([]TagBlock, error) {
	return r.queryList(ctx, selectTagBlockOpenManual)
}

// ListByDay returns tag blocks whose start_time falls on the given UTC day.
func (r *TagBlockRepo) ListByDay(ctx context.Context, day time.Time) ([]TagBlock, error) {
	from := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	return r.ListBetween(ctx, from, to)
}

// ListBetween returns tag blocks in [from, to).
func (r *TagBlockRepo) ListBetween(ctx context.Context, from, to time.Time) ([]TagBlock, error) {
	return r.queryList(ctx, selectTagBlockBetween, from.UTC(), to.UTC())
}

// ListOverlapping returns tag blocks intersecting [from, to).
func (r *TagBlockRepo) ListOverlapping(ctx context.Context, from, to time.Time) ([]TagBlock, error) {
	return r.queryList(ctx, selectTagBlockOverlapping, to.UTC(), from.UTC())
}

func (r *TagBlockRepo) queryList(ctx context.Context, query string, args ...any) ([]TagBlock, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TagBlock
	for rows.Next() {
		b, err := scanTagBlockRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

// Get fetches a single tag block.
func (r *TagBlockRepo) Get(ctx context.Context, id int64) (*TagBlock, error) {
	row := r.db.QueryRowContext(ctx, selectTagBlockByID, id)
	b, err := scanTagBlockRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return b, err
}

// Delete removes a tag block.
func (r *TagBlockRepo) Delete(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, deleteTagBlockSQL, id)
	return err
}

// RecentlyUsedTagIDs returns up to `limit` tag IDs ordered by the most
// recent block start time, restricted to blocks at or after `since`.
func (r *TagBlockRepo) RecentlyUsedTagIDs(ctx context.Context, since time.Time, limit int) ([]int64, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx, selectRecentlyUsedTagIDs, since.UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("query recently-used tags: %w", err)
	}
	defer rows.Close()
	out := make([]int64, 0, limit)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func scanTagBlockRow(row *sql.Row) (*TagBlock, error) {
	var b TagBlock
	var (
		description sql.NullString
		end         sql.NullTime
		isManual    int64
		personioID  sql.NullString
		syncedAt    sql.NullTime
	)
	if err := row.Scan(&b.ID, &b.TagID, &description, &b.StartTime, &end,
		&b.DurationSec, &isManual, &personioID, &syncedAt); err != nil {
		return nil, err
	}
	hydrateTagBlock(&b, description, end, isManual, personioID, syncedAt)
	return &b, nil
}

func scanTagBlockRows(rows *sql.Rows) (*TagBlock, error) {
	var b TagBlock
	var (
		description sql.NullString
		end         sql.NullTime
		isManual    int64
		personioID  sql.NullString
		syncedAt    sql.NullTime
	)
	if err := rows.Scan(&b.ID, &b.TagID, &description, &b.StartTime, &end,
		&b.DurationSec, &isManual, &personioID, &syncedAt); err != nil {
		return nil, err
	}
	hydrateTagBlock(&b, description, end, isManual, personioID, syncedAt)
	return &b, nil
}

func hydrateTagBlock(b *TagBlock, description sql.NullString, end sql.NullTime,
	isManual int64, personioID sql.NullString, syncedAt sql.NullTime) {
	if description.Valid {
		v := description.String
		b.Description = &v
	}
	if end.Valid {
		t := end.Time.UTC()
		b.EndTime = &t
	}
	b.IsManual = isManual != 0
	if personioID.Valid {
		v := personioID.String
		b.PersonioID = &v
	}
	if syncedAt.Valid {
		t := syncedAt.Time.UTC()
		b.SyncedAt = &t
	}
	b.StartTime = b.StartTime.UTC()
}

// assertNoTagOverlapTx returns ErrOverlap if any tag block other than
// excludeID intersects [start, end). end may be nil to denote a still-open
// block (treated as +infinity).
func assertNoTagOverlapTx(ctx context.Context, q txQuerier, excludeID int64, start time.Time, end *time.Time) error {
	probeEnd := farFuture
	if end != nil {
		probeEnd = end.UTC()
	}
	row := q.QueryRowContext(ctx, selectTagBlockOverlap, excludeID, probeEnd, start.UTC())
	var otherID int64
	if err := row.Scan(&otherID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	return fmt.Errorf("%w: id=%d", ErrOverlap, otherID)
}
