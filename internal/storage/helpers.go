package storage

import (
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
