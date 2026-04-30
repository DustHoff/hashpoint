package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// RuleRepo is a SQL-backed RuleRepository.
type RuleRepo struct {
	db *sql.DB
}

// NewRuleRepo wires a repo around the given DB.
func NewRuleRepo(db *sql.DB) *RuleRepo {
	return &RuleRepo{db: db}
}

const (
	ruleColumns = `id, match_field, match_type, pattern, tag_id, description, priority, enabled, created_at`

	insertRule = `INSERT INTO tagging_rules (
		match_field, match_type, pattern, tag_id, description, priority, enabled
	) VALUES (?, ?, ?, ?, ?, ?, ?)`

	updateRuleSQL = `UPDATE tagging_rules SET
		match_field = ?, match_type = ?, pattern = ?, tag_id = ?,
		description = ?, priority = ?, enabled = ?
		WHERE id = ?`

	deleteRuleSQL = `DELETE FROM tagging_rules WHERE id = ?`

	selectRuleByID    = `SELECT ` + ruleColumns + ` FROM tagging_rules WHERE id = ?`
	selectAllRules    = `SELECT ` + ruleColumns + ` FROM tagging_rules ORDER BY priority DESC, id ASC`
	selectActiveRules = `SELECT ` + ruleColumns + ` FROM tagging_rules WHERE enabled = 1 ORDER BY priority DESC, id ASC`
)

// Create inserts a new rule.
func (r *RuleRepo) Create(ctx context.Context, rule *Rule) error {
	res, err := r.db.ExecContext(ctx, insertRule,
		string(rule.MatchField),
		string(rule.MatchType),
		rule.Pattern,
		rule.TagID,
		nullableStringPtr(rule.Description),
		rule.Priority,
		boolToInt(rule.Enabled),
	)
	if err != nil {
		return fmt.Errorf("insert rule: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	rule.ID = id
	stored, err := r.Get(ctx, id)
	if err == nil && stored != nil {
		rule.CreatedAt = stored.CreatedAt
	}
	return nil
}

// Update modifies an existing rule.
func (r *RuleRepo) Update(ctx context.Context, rule *Rule) error {
	_, err := r.db.ExecContext(ctx, updateRuleSQL,
		string(rule.MatchField),
		string(rule.MatchType),
		rule.Pattern,
		rule.TagID,
		nullableStringPtr(rule.Description),
		rule.Priority,
		boolToInt(rule.Enabled),
		rule.ID,
	)
	return err
}

// Delete removes a rule.
func (r *RuleRepo) Delete(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, deleteRuleSQL, id)
	return err
}

// Get fetches a rule by ID.
func (r *RuleRepo) Get(ctx context.Context, id int64) (*Rule, error) {
	row := r.db.QueryRowContext(ctx, selectRuleByID, id)
	rule, err := scanRule(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return rule, err
}

// ListEnabled returns enabled rules sorted by priority DESC.
func (r *RuleRepo) ListEnabled(ctx context.Context) ([]Rule, error) {
	return r.queryRules(ctx, selectActiveRules)
}

// List returns all rules.
func (r *RuleRepo) List(ctx context.Context) ([]Rule, error) {
	return r.queryRules(ctx, selectAllRules)
}

func (r *RuleRepo) queryRules(ctx context.Context, query string) ([]Rule, error) {
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Rule
	for rows.Next() {
		rule, err := scanRuleRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rule)
	}
	return out, rows.Err()
}

func scanRule(row *sql.Row) (*Rule, error) {
	var rule Rule
	var enabled int64
	var matchField, matchType string
	var description sql.NullString
	if err := row.Scan(&rule.ID, &matchField, &matchType, &rule.Pattern,
		&rule.TagID, &description, &rule.Priority, &enabled, &rule.CreatedAt); err != nil {
		return nil, err
	}
	rule.MatchField = MatchField(matchField)
	rule.MatchType = MatchType(matchType)
	rule.Description = nsToString(description)
	rule.Enabled = enabled != 0
	rule.CreatedAt = rule.CreatedAt.UTC()
	return &rule, nil
}

func scanRuleRows(rows *sql.Rows) (*Rule, error) {
	var rule Rule
	var enabled int64
	var matchField, matchType string
	var description sql.NullString
	if err := rows.Scan(&rule.ID, &matchField, &matchType, &rule.Pattern,
		&rule.TagID, &description, &rule.Priority, &enabled, &rule.CreatedAt); err != nil {
		return nil, err
	}
	rule.MatchField = MatchField(matchField)
	rule.MatchType = MatchType(matchType)
	rule.Description = nsToString(description)
	rule.Enabled = enabled != 0
	rule.CreatedAt = rule.CreatedAt.UTC()
	return &rule, nil
}
