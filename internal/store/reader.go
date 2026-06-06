// Package store provides read-only access to Agent Receipt SQLite databases.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sort"
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
	ToolName            string `json:"tool_name"`
	Server              string `json:"server"`
	RiskLevel           string `json:"risk_level"`
	Status              string `json:"status"`
	Timestamp           string `json:"timestamp"`
	IssuerID            string `json:"issuer_id"`
	PrincipalID         string `json:"principal_id"`
	ReceiptHash         string `json:"receipt_hash"`
	PreviousReceiptHash string `json:"previous_receipt_hash"`
	// ParametersInputPreview and ParametersOutputPreview are short, operator-
	// disclosed snippets of the call's input/output (ADR-0012). They power the
	// list-view hover tooltip; the full disclosure map is fetched via the
	// detail endpoint. Truncated to disclosurePreviewMaxLen characters in SQL so a
	// large disclosure doesn't bloat list responses.
	ParametersInputPreview  string `json:"parameters_input_preview,omitempty"`
	ParametersOutputPreview string `json:"parameters_output_preview,omitempty"`
	// HasParametersDisclosure is true if the receipt has any parameters_disclosure
	// data, regardless of which keys are present. Allows list UI to show the
	// disclosure indicator even when only non-primary keys exist.
	HasParametersDisclosure bool `json:"has_parameters_disclosure"`
	// OutputStatusMismatch is true when outcome.status is "success" but
	// parameters_disclosure.output parses as a JSON object with isError: true.
	// MCP tool emitters historically stamp the outcome before inspecting the
	// response payload, so older receipts in existing stores carry a misleading
	// status (see issue #50). This is a read-only display hint; the dashboard
	// never rewrites the receipt or its hash.
	OutputStatusMismatch bool `json:"output_status_mismatch"`
}

// disclosurePreviewMaxLen bounds the size of input/output previews returned
// in list rows. The modal still shows the full value via the detail endpoint.
const disclosurePreviewMaxLen = 200

// ChainReceipt pairs a parsed receipt with the verbatim JSON bytes stored in
// the receipt_json column. Chain verification recomputes the canonical hash
// from Raw via receipt.HashRawReceipt — the same hash form that the collector
// and an auditor use — rather than round-tripping through the Go struct. The
// struct path (receipt.HashReceipt) silently drops any forward-compat fields a
// newer SDK wrote, yielding a hash that disagrees with the stored one and a
// false "broken chain" report (issue #719).
type ChainReceipt struct {
	Receipt receipt.AgentReceipt
	Raw     []byte
}

// ChainSummary holds aggregate information about a receipt chain.
type ChainSummary struct {
	ChainID        string `json:"chain_id"`
	ReceiptCount   int    `json:"receipt_count"`
	FirstTimestamp string `json:"first_timestamp"`
	LastTimestamp  string `json:"last_timestamp"`
}

// Stats holds aggregate statistics for the store.
type Stats struct {
	Total    int          `json:"total"`
	Chains   int          `json:"chains"`
	ByRisk   []GroupCount `json:"by_risk"`
	ByStatus []GroupCount `json:"by_status"`
	ByAction []GroupCount `json:"by_action"`
	// LatestTimestamp is the ISO 8601 timestamp of the most recent receipt
	// in the store. Omitted from the JSON response (and left as the zero
	// value) when the store is empty. Used in the header to show when the
	// audit trail was last updated.
	LatestTimestamp string `json:"latest_timestamp,omitempty"`
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
	Since      *string // ISO 8601 timestamp, inclusive — watermark for live polling; clients dedup by id
	Limit      *int
}

// OpenReadOnly opens an existing receipt SQLite database in read-only mode.
// Returns an error if the file does not exist.
func OpenReadOnly(dbPath string) (*Reader, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("database not found: %w", err)
	}

	dsn := (&url.URL{
		Scheme:   "file",
		Opaque:   url.PathEscape(dbPath),
		RawQuery: "mode=ro",
	}).String()
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
	if f.Since != nil {
		// Inclusive so receipts that share a second with the watermark aren't
		// silently lost when timestamps lack sub-second precision; the client
		// dedups by id.
		conds = append(conds, "timestamp >= ?")
		args = append(args, *f.Since)
	}

	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}

	limit := 10000
	if f.Limit != nil {
		limit = *f.Limit
	}

	// When Since is set we are acting as a chronological tail: order ASC so a
	// burst larger than the LIMIT is drained correctly across successive polls
	// (the client advances the watermark to the newest returned row, and the
	// next call picks up from there). DESC + LIMIT would silently skip the
	// middle of an overflow. The id tie-break keeps the order deterministic
	// across rows that share a timestamp.
	orderBy := "timestamp DESC"
	if f.Since != nil {
		orderBy = "timestamp ASC, id ASC"
	}

	// output_status_mismatch flags receipts whose outcome.status is "success"
	// while their parameters_disclosure.output parses as a JSON object with
	// isError: true. parameters_disclosure values are JSON-encoded strings
	// (per ADR-0012), so we extract twice: once to get the string, once to
	// reach into it. The CASE gate ensures the inner json_extract only runs
	// on text that json_valid has cleared — SQL AND does not short-circuit,
	// and json_extract on non-JSON text raises a "malformed JSON" error.
	query := fmt.Sprintf(
		`SELECT id, chain_id, sequence, action_type,
		        COALESCE(tool_name, ''),
		        COALESCE(json_extract(receipt_json, '$.credentialSubject.action.target.system'), ''),
		        risk_level, status,
		        timestamp, issuer_id, COALESCE(principal_id, ''),
		        receipt_hash, COALESCE(previous_receipt_hash, ''),
		        COALESCE(substr(json_extract(receipt_json, '$.credentialSubject.action.parameters_disclosure.input'), 1, ?), ''),
		        COALESCE(substr(json_extract(receipt_json, '$.credentialSubject.action.parameters_disclosure.output'), 1, ?), ''),
		        COALESCE(json_extract(receipt_json, '$.credentialSubject.action.parameters_disclosure'), 'null') NOT IN ('null', '{}'),
		        CASE
		          WHEN status = 'success'
		           AND json_valid(IFNULL(json_extract(receipt_json, '$.credentialSubject.action.parameters_disclosure.output'), 'null')) = 1
		          THEN IFNULL(json_extract(IFNULL(json_extract(receipt_json, '$.credentialSubject.action.parameters_disclosure.output'), 'null'), '$.isError'), 0) = 1
		          ELSE 0
		        END
		 FROM receipts %s ORDER BY %s LIMIT ?`,
		where, orderBy,
	)
	// substr args come before the WHERE-clause args in the SELECT list, so
	// prepend them.
	args = append([]any{disclosurePreviewMaxLen, disclosurePreviewMaxLen}, args...)
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
			&row.ToolName, &row.Server,
			&row.RiskLevel, &row.Status, &row.Timestamp, &row.IssuerID,
			&row.PrincipalID, &row.ReceiptHash, &row.PreviousReceiptHash,
			&row.ParametersInputPreview, &row.ParametersOutputPreview,
			&row.HasParametersDisclosure,
			&row.OutputStatusMismatch,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// GetChain retrieves all receipts in a chain, ordered by sequence. Each result
// carries both the parsed receipt and the verbatim receipt_json bytes so chain
// verification can recompute hashes from the wire form (see ChainReceipt).
func (r *Reader) GetChain(chainID string) ([]ChainReceipt, error) {
	rows, err := r.db.Query(
		"SELECT receipt_json FROM receipts WHERE chain_id = ? ORDER BY sequence ASC",
		chainID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ChainReceipt
	for rows.Next() {
		// Scan into []byte (not string): database/sql hands *[]byte a fresh
		// copy owned by the caller, so we reuse the one allocation for both
		// Unmarshal and the retained Raw bytes instead of copying twice.
		var rawJSON []byte
		if err := rows.Scan(&rawJSON); err != nil {
			return nil, err
		}
		var ar receipt.AgentReceipt
		if err := json.Unmarshal(rawJSON, &ar); err != nil {
			return nil, fmt.Errorf("corrupt receipt in chain %s: %w", chainID, err)
		}
		out = append(out, ChainReceipt{Receipt: ar, Raw: rawJSON})
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

	// MAX over an empty table returns NULL; scan into a nullable string so the
	// empty-store case is "" rather than an error.
	var latest sql.NullString
	if err := r.db.QueryRow("SELECT MAX(timestamp) FROM receipts").Scan(&latest); err != nil {
		return Stats{}, err
	}
	if latest.Valid {
		st.LatestTimestamp = latest.String
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
	st.ByAction, err = r.groupBy("action_type")
	if err != nil {
		return Stats{}, err
	}

	return st, nil
}

// ActionStat holds aggregate statistics for a single action type.
type ActionStat struct {
	ActionType  string  `json:"action_type"`
	Total       int     `json:"total"`
	Success     int     `json:"success"`
	Failure     int     `json:"failure"`
	FailureRate float64 `json:"failure_rate"`
}

// ActionStats returns per-action-type failure rate statistics. Action types
// with fewer than 5 receipts are excluded (HAVING COUNT(*) >= 5). Results are
// sorted by failure_rate DESC, then total DESC, then action_type ASC for a
// deterministic ordering. An optional since timestamp (ISO-8601, inclusive)
// restricts the query to receipts at or after that time; nil means all-time.
func (r *Reader) ActionStats(since *string) ([]ActionStat, error) {
	var conds []string
	var args []any

	if since != nil {
		conds = append(conds, "timestamp >= ?")
		args = append(args, *since)
	}

	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT action_type,
		       COUNT(*) AS total,
		       SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END) AS success_count,
		       SUM(CASE WHEN status = 'failure' THEN 1 ELSE 0 END) AS failure_count
		FROM receipts
		%s
		GROUP BY action_type
		HAVING COUNT(*) >= 5
	`, where)

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ActionStat
	for rows.Next() {
		var s ActionStat
		if err := rows.Scan(&s.ActionType, &s.Total, &s.Success, &s.Failure); err != nil {
			return nil, err
		}
		if s.Total > 0 {
			s.FailureRate = float64(s.Failure) / float64(s.Total) * 100
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Sort: failure_rate DESC, total DESC, action_type ASC (deterministic).
	sort.Slice(out, func(i, j int) bool {
		if out[i].FailureRate != out[j].FailureRate {
			return out[i].FailureRate > out[j].FailureRate
		}
		if out[i].Total != out[j].Total {
			return out[i].Total > out[j].Total
		}
		return out[i].ActionType < out[j].ActionType
	})

	return out, nil
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
	"risk_level":  true,
	"status":      true,
	"action_type": true,
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
