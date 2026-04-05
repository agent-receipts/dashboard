// Package store provides read-only access to Agent Receipt SQLite databases.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/agent-receipts/ar/sdk/go/receipt"

	_ "modernc.org/sqlite"
)

// Reader provides read-only access to an existing receipt SQLite database.
type Reader struct {
	db *sql.DB
}

// ReceiptRow holds the indexed columns from a receipt row for list views.
type ReceiptRow struct {
	ID                  string `json:"id"`
	ChainID             string `json:"chain_id"`
	Sequence            int    `json:"sequence"`
	ActionType          string `json:"action_type"`
	RiskLevel           string `json:"risk_level"`
	Status              string `json:"status"`
	Timestamp           string `json:"timestamp"`
	IssuerID            string `json:"issuer_id"`
	PrincipalID         string `json:"principal_id"`
	ReceiptHash         string `json:"receipt_hash"`
	PreviousReceiptHash string `json:"previous_receipt_hash"`
}

// ChainSummary holds aggregate information about a receipt chain.
type ChainSummary struct {
	ChainID        string `json:"chain_id"`
	ReceiptCount   int    `json:"receipt_count"`
	FirstTimestamp string `json:"first_timestamp"`
	LastTimestamp   string `json:"last_timestamp"`
}

// Stats holds aggregate statistics for the store.
type Stats struct {
	Total    int          `json:"total"`
	Chains   int          `json:"chains"`
	ByRisk   []GroupCount `json:"by_risk"`
	ByStatus []GroupCount `json:"by_status"`
}

// GroupCount is a label + count pair.
type GroupCount struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

// Filter controls which receipts are returned by ListReceipts.
type Filter struct {
	ChainID    *string
	ActionType *string
	RiskLevel  *string
	Status     *string
	After      *string // ISO 8601 timestamp, inclusive
	Before     *string // ISO 8601 timestamp, inclusive
	Limit      *int
}

// OpenReadOnly opens an existing receipt SQLite database in read-only mode.
// Returns an error if the file does not exist.
func OpenReadOnly(dbPath string) (*Reader, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("database not found: %w", err)
	}

	dsn := "file:" + dbPath + "?mode=ro"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Set query_only pragma as a second line of defense against writes.
	if _, err := db.Exec("PRAGMA query_only=ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set query_only: %w", err)
	}

	// Verify the database is readable and has the expected schema.
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM receipts").Scan(&count); err != nil {
		db.Close()
		return nil, fmt.Errorf("verify schema: %w", err)
	}

	return &Reader{db: db}, nil
}

// GetByID retrieves a full receipt by its ID. Returns nil if not found.
func (r *Reader) GetByID(id string) (*receipt.AgentReceipt, error) {
	var rJSON string
	err := r.db.QueryRow("SELECT receipt_json FROM receipts WHERE id = ?", id).Scan(&rJSON)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ar receipt.AgentReceipt
	if err := json.Unmarshal([]byte(rJSON), &ar); err != nil {
		return nil, fmt.Errorf("corrupt receipt (id=%s): %w", id, err)
	}
	return &ar, nil
}

// ListReceipts returns receipt rows matching the given filter.
func (r *Reader) ListReceipts(f Filter) ([]ReceiptRow, error) {
	var conds []string
	var args []any

	if f.ChainID != nil {
		conds = append(conds, "chain_id = ?")
		args = append(args, *f.ChainID)
	}
	if f.ActionType != nil {
		conds = append(conds, "action_type = ?")
		args = append(args, *f.ActionType)
	}
	if f.RiskLevel != nil {
		conds = append(conds, "risk_level = ?")
		args = append(args, *f.RiskLevel)
	}
	if f.Status != nil {
		conds = append(conds, "status = ?")
		args = append(args, *f.Status)
	}
	if f.After != nil {
		conds = append(conds, "timestamp >= ?")
		args = append(args, *f.After)
	}
	if f.Before != nil {
		conds = append(conds, "timestamp <= ?")
		args = append(args, *f.Before)
	}

	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}

	limit := 1000
	if f.Limit != nil {
		limit = *f.Limit
	}

	query := fmt.Sprintf(
		`SELECT id, chain_id, sequence, action_type, risk_level, status,
		        timestamp, issuer_id, COALESCE(principal_id, ''),
		        receipt_hash, COALESCE(previous_receipt_hash, '')
		 FROM receipts %s ORDER BY timestamp DESC LIMIT ?`,
		where,
	)
	args = append(args, limit)

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ReceiptRow
	for rows.Next() {
		var row ReceiptRow
		if err := rows.Scan(
			&row.ID, &row.ChainID, &row.Sequence, &row.ActionType,
			&row.RiskLevel, &row.Status, &row.Timestamp, &row.IssuerID,
			&row.PrincipalID, &row.ReceiptHash, &row.PreviousReceiptHash,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// GetChain retrieves all receipts in a chain, ordered by sequence.
func (r *Reader) GetChain(chainID string) ([]receipt.AgentReceipt, error) {
	rows, err := r.db.Query(
		"SELECT receipt_json FROM receipts WHERE chain_id = ? ORDER BY sequence ASC",
		chainID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []receipt.AgentReceipt
	for rows.Next() {
		var rJSON string
		if err := rows.Scan(&rJSON); err != nil {
			return nil, err
		}
		var ar receipt.AgentReceipt
		if err := json.Unmarshal([]byte(rJSON), &ar); err != nil {
			return nil, fmt.Errorf("corrupt receipt in chain %s: %w", chainID, err)
		}
		out = append(out, ar)
	}
	return out, rows.Err()
}

// ListChains returns a summary of each chain in the store.
func (r *Reader) ListChains() ([]ChainSummary, error) {
	rows, err := r.db.Query(`
		SELECT chain_id, COUNT(*), MIN(timestamp), MAX(timestamp)
		FROM receipts
		GROUP BY chain_id
		ORDER BY MAX(timestamp) DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ChainSummary
	for rows.Next() {
		var cs ChainSummary
		if err := rows.Scan(&cs.ChainID, &cs.ReceiptCount, &cs.FirstTimestamp, &cs.LastTimestamp); err != nil {
			return nil, err
		}
		out = append(out, cs)
	}
	return out, rows.Err()
}

// Stats returns aggregate statistics.
func (r *Reader) Stats() (Stats, error) {
	var st Stats

	if err := r.db.QueryRow("SELECT COUNT(*) FROM receipts").Scan(&st.Total); err != nil {
		return Stats{}, err
	}
	if err := r.db.QueryRow("SELECT COUNT(DISTINCT chain_id) FROM receipts").Scan(&st.Chains); err != nil {
		return Stats{}, err
	}

	var err error
	st.ByRisk, err = r.groupBy("risk_level")
	if err != nil {
		return Stats{}, err
	}
	st.ByStatus, err = r.groupBy("status")
	if err != nil {
		return Stats{}, err
	}

	return st, nil
}

// Close closes the database connection.
func (r *Reader) Close() error {
	return r.db.Close()
}

// execWrite is exposed for testing that writes fail on read-only connections.
func (r *Reader) execWrite(query string, args ...any) error {
	_, err := r.db.Exec(query, args...)
	return err
}

var allowedGroupByColumns = map[string]bool{
	"risk_level": true,
	"status":     true,
}

func (r *Reader) groupBy(column string) ([]GroupCount, error) {
	if !allowedGroupByColumns[column] {
		return nil, fmt.Errorf("invalid group-by column: %q", column)
	}
	query := fmt.Sprintf(
		"SELECT %s, COUNT(*) FROM receipts GROUP BY %s ORDER BY COUNT(*) DESC",
		column, column,
	)
	rows, err := r.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []GroupCount
	for rows.Next() {
		var gc GroupCount
		if err := rows.Scan(&gc.Label, &gc.Count); err != nil {
			return nil, err
		}
		out = append(out, gc)
	}
	return out, rows.Err()
}
