package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// OnCallRepo is a SQL-backed OnCallRepository. The two tables it owns
// (oncall_documentations + oncall_submissions) are introduced in
// migration 0007.
type OnCallRepo struct {
	db *sql.DB
}

// NewOnCallRepo wires a repo around the given DB handle.
func NewOnCallRepo(db *sql.DB) *OnCallRepo {
	return &OnCallRepo{db: db}
}

const (
	oncallDocColumns = `id, block_id, tag_at_creation, stale, application,
		incident_type, solution, created_at, updated_at`

	insertOnCallDoc = `INSERT INTO oncall_documentations
		(block_id, tag_at_creation) VALUES (?, ?)`

	selectOnCallDocByID = `SELECT ` + oncallDocColumns + `
		FROM oncall_documentations WHERE id = ?`

	selectOnCallDocByBlock = `SELECT ` + oncallDocColumns + `
		FROM oncall_documentations WHERE block_id = ?`

	updateOnCallDocDraft = `UPDATE oncall_documentations
		SET application = ?, incident_type = ?, solution = ?,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`

	updateOnCallDocStale = `UPDATE oncall_documentations
		SET stale = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`

	deleteOnCallDocByID    = `DELETE FROM oncall_documentations WHERE id = ?`
	deleteOnCallDocByBlock = `DELETE FROM oncall_documentations WHERE block_id = ?`

	oncallSubmissionColumns = `id, doc_id, plugin_name, status, external_ref,
		external_url, last_error, created_at, updated_at, submitted_at`

	insertOnCallSubmission = `INSERT INTO oncall_submissions
		(doc_id, plugin_name) VALUES (?, ?)`

	selectOnCallSubmissionByDocAndPlugin = `SELECT ` + oncallSubmissionColumns + `
		FROM oncall_submissions WHERE doc_id = ? AND plugin_name = ?`

	selectOnCallSubmissionsByDoc = `SELECT ` + oncallSubmissionColumns + `
		FROM oncall_submissions WHERE doc_id = ?
		ORDER BY plugin_name ASC`

	updateOnCallSubmissionPending = `UPDATE oncall_submissions
		SET status = 'pending', last_error = NULL,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`

	updateOnCallSubmissionSubmitted = `UPDATE oncall_submissions
		SET status = 'submitted', external_ref = ?, external_url = ?,
		    last_error = NULL, submitted_at = ?,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`

	updateOnCallSubmissionFailed = `UPDATE oncall_submissions
		SET status = 'failed', last_error = ?,
		    submitted_at = ?,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`
)

// EnsureForBlock idempotently inserts a doc row for blockID. The unique
// constraint on block_id guarantees at most one doc per block; if a row
// already exists we return it (TagAtCreation is captured on first insert
// only — later calls do NOT overwrite it).
func (r *OnCallRepo) EnsureForBlock(ctx context.Context, blockID, tagAtCreation int64) (*OnCallDoc, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	existing, err := scanOnCallDocRow(tx.QueryRowContext(ctx, selectOnCallDocByBlock, blockID))
	switch {
	case err == nil:
		// already present — caller treats this as success.
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return r.loadSubmissions(ctx, existing)
	case errors.Is(err, sql.ErrNoRows):
		// fall through to insert
	default:
		return nil, err
	}

	res, err := tx.ExecContext(ctx, insertOnCallDoc, blockID, tagAtCreation)
	if err != nil {
		return nil, fmt.Errorf("insert oncall doc: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	inserted, err := scanOnCallDocRow(tx.QueryRowContext(ctx, selectOnCallDocByID, id))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	inserted.Submissions = nil
	return inserted, nil
}

// GetByBlock returns the doc + its submissions, or ErrNotFound.
func (r *OnCallRepo) GetByBlock(ctx context.Context, blockID int64) (*OnCallDoc, error) {
	doc, err := scanOnCallDocRow(r.db.QueryRowContext(ctx, selectOnCallDocByBlock, blockID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return r.loadSubmissions(ctx, doc)
}

// Get returns the doc by primary key, with its submissions.
func (r *OnCallRepo) Get(ctx context.Context, id int64) (*OnCallDoc, error) {
	doc, err := scanOnCallDocRow(r.db.QueryRowContext(ctx, selectOnCallDocByID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return r.loadSubmissions(ctx, doc)
}

// List returns docs joined to the tag block start_time so we can order
// newest-incident-first. The submission rows are fetched in a single
// follow-up query and zipped in Go to avoid the LEFT JOIN row-explosion.
func (r *OnCallRepo) List(ctx context.Context, filter OnCallFilter) ([]OnCallDoc, error) {
	var (
		conds = []string{}
		args  []any
	)

	if filter.From != nil {
		conds = append(conds, "tb.start_time >= ?")
		args = append(args, filter.From.UTC())
	}
	if filter.To != nil {
		conds = append(conds, "tb.start_time < ?")
		args = append(args, filter.To.UTC())
	}
	if !filter.IncludeStale {
		conds = append(conds, "od.stale = 0")
	}

	q := `SELECT ` + prefixColumns("od", oncallDocColumns) + `
		FROM oncall_documentations od
		JOIN tag_blocks tb ON tb.id = od.block_id`
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += " ORDER BY tb.start_time DESC, od.id DESC"

	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var docs []OnCallDoc
	for rows.Next() {
		d, err := scanOnCallDocCols(rows)
		if err != nil {
			return nil, err
		}
		docs = append(docs, *d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Filter on rolled-up status in Go (it's not stored) once submissions
	// are loaded. Cheaper than a complex GROUP BY HAVING clause and keeps
	// the status definition in one place.
	if len(docs) == 0 {
		return docs, nil
	}
	subsByDoc, err := r.loadSubmissionsForDocs(ctx, docs)
	if err != nil {
		return nil, err
	}
	for i := range docs {
		docs[i].Submissions = subsByDoc[docs[i].ID]
	}

	if filter.Status != nil {
		want := *filter.Status
		out := docs[:0]
		for _, d := range docs {
			if d.Status() == want {
				out = append(out, d)
			}
		}
		docs = out
	}
	return docs, nil
}

// UpdateDraft writes the user's form input.
func (r *OnCallRepo) UpdateDraft(ctx context.Context, id int64, application string, incidentType OnCallIncidentType, solution string) error {
	res, err := r.db.ExecContext(ctx, updateOnCallDocDraft, application, string(incidentType), solution, id)
	if err != nil {
		return fmt.Errorf("update oncall draft: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkStale sets stale=1.
func (r *OnCallRepo) MarkStale(ctx context.Context, id int64) error {
	return r.setStale(ctx, id, true)
}

// ClearStale sets stale=0.
func (r *OnCallRepo) ClearStale(ctx context.Context, id int64) error {
	return r.setStale(ctx, id, false)
}

func (r *OnCallRepo) setStale(ctx context.Context, id int64, stale bool) error {
	res, err := r.db.ExecContext(ctx, updateOnCallDocStale, boolToInt(stale), id)
	if err != nil {
		return fmt.Errorf("update oncall stale: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Dismiss deletes the doc + submissions (FK cascade).
// Callers are expected to enforce the "no non-pending submission" rule —
// the storage layer is permissive so tests don't have to build elaborate
// state to delete a row.
func (r *OnCallRepo) Dismiss(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, deleteOnCallDocByID, id)
	if err != nil {
		return fmt.Errorf("delete oncall doc: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteByBlock removes the doc tied to blockID, if any. A missing row is
// not an error — the orchestrator calls this defensively.
func (r *OnCallRepo) DeleteByBlock(ctx context.Context, blockID int64) error {
	if _, err := r.db.ExecContext(ctx, deleteOnCallDocByBlock, blockID); err != nil {
		return fmt.Errorf("delete oncall doc by block: %w", err)
	}
	return nil
}

// EnsureSubmission inserts the (doc, plugin) row if missing; otherwise
// returns the existing one untouched.
func (r *OnCallRepo) EnsureSubmission(ctx context.Context, docID int64, pluginName string) (*OnCallSubmission, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	existing, err := scanOnCallSubmissionRow(tx.QueryRowContext(ctx, selectOnCallSubmissionByDocAndPlugin, docID, pluginName))
	switch {
	case err == nil:
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return existing, nil
	case errors.Is(err, sql.ErrNoRows):
		// fall through
	default:
		return nil, err
	}

	if _, err := tx.ExecContext(ctx, insertOnCallSubmission, docID, pluginName); err != nil {
		return nil, fmt.Errorf("insert oncall submission: %w", err)
	}
	row := tx.QueryRowContext(ctx, selectOnCallSubmissionByDocAndPlugin, docID, pluginName)
	sub, err := scanOnCallSubmissionRow(row)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return sub, nil
}

// MarkSubmissionPending flips a row back to 'pending' for retry.
func (r *OnCallRepo) MarkSubmissionPending(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, updateOnCallSubmissionPending, id)
	if err != nil {
		return fmt.Errorf("mark submission pending: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkSubmissionSubmitted records a successful plugin response.
func (r *OnCallRepo) MarkSubmissionSubmitted(ctx context.Context, id int64, externalRef, externalURL string, at time.Time) error {
	res, err := r.db.ExecContext(ctx, updateOnCallSubmissionSubmitted,
		nullableString(externalRef),
		nullableString(externalURL),
		at.UTC(),
		id,
	)
	if err != nil {
		return fmt.Errorf("mark submission submitted: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkSubmissionFailed records a plugin error.
func (r *OnCallRepo) MarkSubmissionFailed(ctx context.Context, id int64, errMsg string, at time.Time) error {
	res, err := r.db.ExecContext(ctx, updateOnCallSubmissionFailed,
		errMsg,
		at.UTC(),
		id,
	)
	if err != nil {
		return fmt.Errorf("mark submission failed: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListSubmissionsByDoc returns all per-plugin attempts for docID.
func (r *OnCallRepo) ListSubmissionsByDoc(ctx context.Context, docID int64) ([]OnCallSubmission, error) {
	rows, err := r.db.QueryContext(ctx, selectOnCallSubmissionsByDoc, docID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var subs []OnCallSubmission
	for rows.Next() {
		s, err := scanOnCallSubmissionCols(rows)
		if err != nil {
			return nil, err
		}
		subs = append(subs, *s)
	}
	return subs, rows.Err()
}

// loadSubmissions populates doc.Submissions in one extra query.
func (r *OnCallRepo) loadSubmissions(ctx context.Context, doc *OnCallDoc) (*OnCallDoc, error) {
	subs, err := r.ListSubmissionsByDoc(ctx, doc.ID)
	if err != nil {
		return nil, err
	}
	doc.Submissions = subs
	return doc, nil
}

func (r *OnCallRepo) loadSubmissionsForDocs(ctx context.Context, docs []OnCallDoc) (map[int64][]OnCallSubmission, error) {
	if len(docs) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(docs))
	args := make([]any, len(docs))
	for i, d := range docs {
		placeholders[i] = "?"
		args[i] = d.ID
	}
	q := `SELECT ` + oncallSubmissionColumns + `
		FROM oncall_submissions
		WHERE doc_id IN (` + strings.Join(placeholders, ",") + `)
		ORDER BY plugin_name ASC`
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := map[int64][]OnCallSubmission{}
	for rows.Next() {
		s, err := scanOnCallSubmissionCols(rows)
		if err != nil {
			return nil, err
		}
		out[s.DocID] = append(out[s.DocID], *s)
	}
	return out, rows.Err()
}

// --- row scanning -------------------------------------------------------

// rowScanner unifies *sql.Row and *sql.Rows for the column scanners below.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanOnCallDocRow(row rowScanner) (*OnCallDoc, error) {
	return scanOnCallDocCols(row)
}

func scanOnCallDocCols(row rowScanner) (*OnCallDoc, error) {
	var (
		d            OnCallDoc
		stale        int64
		incidentType string
	)
	if err := row.Scan(
		&d.ID,
		&d.BlockID,
		&d.TagAtCreation,
		&stale,
		&d.Application,
		&incidentType,
		&d.Solution,
		&d.CreatedAt,
		&d.UpdatedAt,
	); err != nil {
		return nil, err
	}
	d.Stale = stale != 0
	d.IncidentType = OnCallIncidentType(incidentType)
	d.CreatedAt = d.CreatedAt.UTC()
	d.UpdatedAt = d.UpdatedAt.UTC()
	return &d, nil
}

func scanOnCallSubmissionRow(row rowScanner) (*OnCallSubmission, error) {
	return scanOnCallSubmissionCols(row)
}

func scanOnCallSubmissionCols(row rowScanner) (*OnCallSubmission, error) {
	var (
		s           OnCallSubmission
		externalRef sql.NullString
		externalURL sql.NullString
		lastError   sql.NullString
		submittedAt sql.NullTime
	)
	if err := row.Scan(
		&s.ID,
		&s.DocID,
		&s.PluginName,
		&s.Status,
		&externalRef,
		&externalURL,
		&lastError,
		&s.CreatedAt,
		&s.UpdatedAt,
		&submittedAt,
	); err != nil {
		return nil, err
	}
	s.ExternalRef = nsToString(externalRef)
	s.ExternalURL = nsToString(externalURL)
	s.LastError = nsToString(lastError)
	if submittedAt.Valid {
		t := submittedAt.Time.UTC()
		s.SubmittedAt = &t
	}
	s.CreatedAt = s.CreatedAt.UTC()
	s.UpdatedAt = s.UpdatedAt.UTC()
	return &s, nil
}

// prefixColumns rewrites "a, b, c" → "tbl.a, tbl.b, tbl.c" so a column list
// shared across two queries can be reused in a join context.
func prefixColumns(prefix, cols string) string {
	parts := strings.Split(cols, ",")
	for i, p := range parts {
		parts[i] = prefix + "." + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}
