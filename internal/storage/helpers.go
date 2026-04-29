package storage

import (
	"context"
	"database/sql"
	"time"
)

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableStringPtr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func nullableInt64(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func nsToString(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	v := s.String
	return &v
}

// txQuerier is the subset of *sql.Tx the overlap probe needs — kept narrow
// so test fakes can satisfy it without re-implementing all of *sql.Tx.
type txQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// farFuture is the +infinity sentinel used when probing overlap for a still-
// open block (end_time IS NULL). Chosen well past any plausible block while
// still fitting in SQLite's TEXT/Julian time representation.
var farFuture = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
