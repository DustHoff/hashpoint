package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ProcessTrackRepo is a SQL-backed ProcessTrackRepository.
type ProcessTrackRepo struct {
	db *sql.DB
}

// NewProcessTrackRepo wires a repo around the given DB handle.
func NewProcessTrackRepo(db *sql.DB) *ProcessTrackRepo {
	return &ProcessTrackRepo{db: db}
}

const (
	processColumns = `id, process_name, process_path, window_title, start_time, end_time,
		duration_sec, is_idle, is_communication, last_seen`

	insertProcess = `INSERT INTO process_tracks (
		process_name, process_path, window_title, start_time, end_time,
		duration_sec, is_idle, is_communication
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

	closeProcess = `UPDATE process_tracks
		SET end_time = ?, duration_sec = ?
		WHERE id = ? AND end_time IS NULL`

	markIdleProcess = `UPDATE process_tracks
		SET end_time = ?, duration_sec = ?, is_idle = 1
		WHERE id = ? AND end_time IS NULL`

	// Last-open / all-open queries restrict to focused (is_communication = 0)
	// tracks because the tracker keeps focused-track state via these calls and
	// must not see comm tracks (they are reconciled separately by HWND).
	selectProcessLastOpen = `SELECT ` + processColumns + ` FROM process_tracks
		WHERE end_time IS NULL AND is_communication = 0
		ORDER BY start_time DESC LIMIT 1`

	selectProcessAllOpen = `SELECT ` + processColumns + ` FROM process_tracks
		WHERE end_time IS NULL AND is_communication = 0
		ORDER BY start_time ASC`

	selectCommAllOpen = `SELECT ` + processColumns + ` FROM process_tracks
		WHERE end_time IS NULL AND is_communication = 1
		ORDER BY start_time ASC`

	selectProcessByID = `SELECT ` + processColumns + ` FROM process_tracks WHERE id = ?`

	selectProcessBetween = `SELECT ` + processColumns + ` FROM process_tracks
		WHERE start_time >= ? AND start_time < ?
		ORDER BY start_time ASC`

	// Selecting the column directly (rather than MAX) lets the SQLite driver
	// apply its DATETIME → time.Time conversion; aggregate results otherwise
	// come back as raw strings and fail the Scan. LastEnd is consumed by
	// startup-cleanup paths that look for the freshest "last activity"
	// timestamp; both flavours of track count.
	selectProcessLastEnd = `SELECT end_time FROM process_tracks
		WHERE end_time IS NOT NULL
		ORDER BY end_time DESC LIMIT 1`

	// touchProcess advances last_seen on an open track. The end_time IS NULL
	// guard prevents resurrecting a closed row; the last_seen comparison keeps
	// the heartbeat strictly monotonic so a stale clock can never move it back.
	touchProcess = `UPDATE process_tracks
		SET last_seen = ?
		WHERE id = ? AND end_time IS NULL AND (last_seen IS NULL OR last_seen < ?)`
)

// Open starts a new process track.
func (r *ProcessTrackRepo) Open(ctx context.Context, p *ProcessTrack) error {
	res, err := r.db.ExecContext(ctx, insertProcess,
		p.ProcessName,
		nullableString(p.ProcessPath),
		p.WindowTitle,
		p.StartTime.UTC(),
		nullableTime(p.EndTime),
		p.DurationSec,
		boolToInt(p.IsIdle),
		boolToInt(p.IsCommunication),
	)
	if err != nil {
		return fmt.Errorf("insert process track: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	p.ID = id
	return nil
}

// Close finalizes the open track.
func (r *ProcessTrackRepo) Close(ctx context.Context, id int64, end time.Time) error {
	return r.finalize(ctx, closeProcess, id, end)
}

// MarkIdle finalizes the track as idle.
func (r *ProcessTrackRepo) MarkIdle(ctx context.Context, id int64, end time.Time) error {
	return r.finalize(ctx, markIdleProcess, id, end)
}

// Touch advances the open track's last_seen heartbeat to ts. It is a no-op
// when the track is already closed or when ts is not strictly newer than the
// stored value, keeping the heartbeat monotonic. Recovery uses last_seen as
// the close time of a track left open by a crash, bounding data loss to one
// poll interval instead of the idle-threshold fallback.
func (r *ProcessTrackRepo) Touch(ctx context.Context, id int64, ts time.Time) error {
	ts = ts.UTC()
	if _, err := r.db.ExecContext(ctx, touchProcess, ts, id, ts); err != nil {
		return fmt.Errorf("touch process track: %w", err)
	}
	return nil
}

func (r *ProcessTrackRepo) finalize(ctx context.Context, query string, id int64, end time.Time) error {
	end = end.UTC()
	row := r.db.QueryRowContext(ctx, selectProcessByID, id)
	p, err := scanProcessRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if p == nil {
		return ErrNotFound
	}
	if end.Before(p.StartTime) {
		end = p.StartTime
	}
	dur := int64(end.Sub(p.StartTime).Round(time.Second).Seconds())
	if dur < 0 {
		dur = 0
	}
	if _, err := r.db.ExecContext(ctx, query, end, dur, id); err != nil {
		return fmt.Errorf("finalize process track: %w", err)
	}
	return nil
}

// LastOpen returns the most recently started open track.
func (r *ProcessTrackRepo) LastOpen(ctx context.Context) (*ProcessTrack, error) {
	row := r.db.QueryRowContext(ctx, selectProcessLastOpen)
	p, err := scanProcessRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

// ListOpen returns every focused track that is still open, ordered by
// start_time. Communication tracks are excluded — use ListOpenCommunication.
func (r *ProcessTrackRepo) ListOpen(ctx context.Context) ([]ProcessTrack, error) {
	rows, err := r.db.QueryContext(ctx, selectProcessAllOpen)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProcessTrack
	for rows.Next() {
		p, err := scanProcessRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// ListOpenCommunication returns every communication track that is still open,
// ordered by start_time. Used by tracker recovery on startup to close any
// dangling comm tracks left over from a previous run.
func (r *ProcessTrackRepo) ListOpenCommunication(ctx context.Context) ([]ProcessTrack, error) {
	rows, err := r.db.QueryContext(ctx, selectCommAllOpen)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProcessTrack
	for rows.Next() {
		p, err := scanProcessRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// ListByDay returns tracks whose start_time falls on the given UTC day.
func (r *ProcessTrackRepo) ListByDay(ctx context.Context, day time.Time) ([]ProcessTrack, error) {
	from := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	return r.ListBetween(ctx, from, to)
}

// ListBetween returns tracks in [from, to).
func (r *ProcessTrackRepo) ListBetween(ctx context.Context, from, to time.Time) ([]ProcessTrack, error) {
	rows, err := r.db.QueryContext(ctx, selectProcessBetween, from.UTC(), to.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProcessTrack
	for rows.Next() {
		p, err := scanProcessRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// LastEnd returns the end_time of the most recently closed track.
func (r *ProcessTrackRepo) LastEnd(ctx context.Context) (time.Time, error) {
	var t sql.NullTime
	if err := r.db.QueryRowContext(ctx, selectProcessLastEnd).Scan(&t); err != nil {
		return time.Time{}, err
	}
	if !t.Valid {
		return time.Time{}, nil
	}
	return t.Time.UTC(), nil
}

// Get fetches a single track.
func (r *ProcessTrackRepo) Get(ctx context.Context, id int64) (*ProcessTrack, error) {
	row := r.db.QueryRowContext(ctx, selectProcessByID, id)
	p, err := scanProcessRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

func scanProcessRow(row *sql.Row) (*ProcessTrack, error) {
	var p ProcessTrack
	var (
		processPath     sql.NullString
		end             sql.NullTime
		isIdle          int64
		isCommunication int64
		lastSeen        sql.NullTime
	)
	if err := row.Scan(&p.ID, &p.ProcessName, &processPath, &p.WindowTitle, &p.StartTime, &end,
		&p.DurationSec, &isIdle, &isCommunication, &lastSeen); err != nil {
		return nil, err
	}
	hydrateProcess(&p, processPath, end, isIdle, isCommunication, lastSeen)
	return &p, nil
}

func scanProcessRows(rows *sql.Rows) (*ProcessTrack, error) {
	var p ProcessTrack
	var (
		processPath     sql.NullString
		end             sql.NullTime
		isIdle          int64
		isCommunication int64
		lastSeen        sql.NullTime
	)
	if err := rows.Scan(&p.ID, &p.ProcessName, &processPath, &p.WindowTitle, &p.StartTime, &end,
		&p.DurationSec, &isIdle, &isCommunication, &lastSeen); err != nil {
		return nil, err
	}
	hydrateProcess(&p, processPath, end, isIdle, isCommunication, lastSeen)
	return &p, nil
}

func hydrateProcess(p *ProcessTrack, processPath sql.NullString, end sql.NullTime, isIdle, isCommunication int64, lastSeen sql.NullTime) {
	if processPath.Valid {
		p.ProcessPath = processPath.String
	}
	if end.Valid {
		t := end.Time.UTC()
		p.EndTime = &t
	}
	if lastSeen.Valid {
		t := lastSeen.Time.UTC()
		p.LastSeen = &t
	}
	p.IsIdle = isIdle != 0
	p.IsCommunication = isCommunication != 0
	p.StartTime = p.StartTime.UTC()
}
