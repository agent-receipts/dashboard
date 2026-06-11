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
	"time"

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

	// Layer 3 attribution fields — only present on receipts from daemon ≥ v0.17.0
	// / hook ≥ v0.14.0. Empty/nil for older receipts; the UI degrades gracefully.

	// CorrelationID links a hook pre-check receipt to its mcp-proxy post-action
	// receipt for the same tool call (same tool_use_id).
	CorrelationID string `json:"correlation_id,omitempty"`
	// AgentID identifies which subagent issued this receipt (orchestrator vs
	// spawned subagent). Comes from issuer.runtime.agent_id in the receipt JSON
	// (the issuer.runtime open metadata sub-object, daemon ≥ v0.18.0 / ADR-0026).
	AgentID string `json:"agent_id,omitempty"`
	// AgentType is the runtime-reported agent type label (e.g. "general-purpose"),
	// from issuer.runtime.agent_type.
	AgentType string `json:"agent_type,omitempty"`
	// IssuerModel is the AI model that issued the receipt (e.g. "claude-sonnet-4-6"),
	// from issuer.model in the receipt JSON.
	IssuerModel string `json:"issuer_model,omitempty"`
	// SessionID groups all receipts from the same agent session together.
	// Comes from issuer.session_id in the receipt JSON.
	SessionID string `json:"session_id,omitempty"`
	// Delegation is non-nil on the first receipt of a subagent chain and
	// carries the parent chain reference, enabling delegation edge rendering.
	Delegation *DelegationInfo `json:"delegation,omitempty"`

	// RuntimeModel is the transcript-derived model identifier (e.g.
	// "claude-opus-4-8"), from issuer.runtime.model (obsigna PR #779).
	// Used by the session delegation graph to label agent nodes.
	RuntimeModel string `json:"runtime_model,omitempty"`
}

// DelegationInfo holds parent-chain attribution fields emitted by subagents
// delegated from a parent chain (daemon ≥ v0.17.0). Maps to
// credentialSubject.delegation in the receipt JSON.
type DelegationInfo struct {
	ParentChainID   string `json:"parent_chain_id"`
	ParentReceiptID string `json:"parent_receipt_id"`
	// DelegatorID is the id of the entity that issued the delegation
	// (from credentialSubject.delegation.delegator.id).
	DelegatorID string `json:"delegator_id,omitempty"`
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

// BucketRow is one time bucket in a timeseries query result.
type BucketRow struct {
	Ts       string         `json:"ts"` // RFC3339 UTC, bucket start
	Total    int            `json:"total"`
	ByStatus map[string]int `json:"by_status"`
	ByRisk   map[string]int `json:"by_risk"`
}

// maxTimeseriesBuckets is the upper bound on the number of buckets
// TimeseriesStats will compute. Requests that would exceed this limit return
// an error so the handler can respond with 400.
const maxTimeseriesBuckets = 2000

// GroupCount is a label + count pair.
type GroupCount struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

// ToolStat holds per-tool aggregates within a server group.
type ToolStat struct {
	ToolName    string  `json:"tool_name"`
	Total       int     `json:"total"`
	Failure     int     `json:"failure"`
	FailureRate float64 `json:"failure_rate"`
}

// ServerStat holds per-server aggregates with a breakdown by tool.
type ServerStat struct {
	// Server is the target.system value. An empty string denotes the
	// missing-server bucket (receipts with no target.system); the frontend
	// renders it as "Unknown". Keeping it empty rather than the literal string
	// "Unknown" avoids colliding with a real server named "Unknown".
	Server  string     `json:"server"`
	Tools   []ToolStat `json:"tools"`
	Total   int        `json:"total"`
	Failure int        `json:"failure"`
	// FailureRate is the fraction of receipts that failed, in [0,1].
	FailureRate float64 `json:"failure_rate"`
}

// Filter controls which receipts are returned by ListReceipts.
type Filter struct {
	ChainID    *string
	ActionType *string
	RiskLevel  *string
	Status     *string
	Server     *string    // target.system value (exact match)
	ToolName   *string    // tool_name column (exact match)
	SessionID  *string    // issuer.session_id value (exact match)
	After      *string    // ISO 8601 timestamp, inclusive
	Before     *string    // ISO 8601 timestamp, inclusive
	Since      *string    // ISO 8601 timestamp, inclusive — watermark for live polling; clients dedup by id
	Limit      *int
	Q          *string    // free-text search against the raw receipt JSON; nil or whitespace-only means no filter
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
	if f.ToolName != nil {
		conds = append(conds, "tool_name = ?")
		args = append(args, *f.ToolName)
	}
	if f.Server != nil {
		conds = append(conds, "json_extract(receipt_json, '$.credentialSubject.action.target.system') = ?")
		args = append(args, *f.Server)
	}
	if f.SessionID != nil {
		conds = append(conds, "json_extract(receipt_json, '$.issuer.session_id') = ?")
		args = append(args, *f.SessionID)
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
	if f.Q != nil {
		if term := strings.TrimSpace(*f.Q); term != "" {
			conds = append(conds, `receipt_json LIKE '%' || ? || '%' ESCAPE '\'`)
			args = append(args, escapeLikeTerm(term))
		}
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
	//
	// Layer 3 attribution fields: correlation_id and delegation arrive with
	// daemon ≥ v0.17.0; issuer.runtime.{agent_id,agent_type} with daemon ≥
	// v0.18.0 (ADR-0026); issuer.model whenever the daemon stamps the issuer
	// identity. issuer.runtime.model is transcript-derived (obsigna PR #779)
	// and used by the session graph to label agent nodes. All are absent from
	// older receipts and scan as empty strings / NULL.
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
		        END,
		        COALESCE(json_extract(receipt_json, '$.credentialSubject.correlation_id'), ''),
		        COALESCE(json_extract(receipt_json, '$.issuer.runtime.agent_id'), ''),
		        COALESCE(json_extract(receipt_json, '$.issuer.runtime.agent_type'), ''),
		        COALESCE(json_extract(receipt_json, '$.issuer.model'), ''),
		        COALESCE(json_extract(receipt_json, '$.issuer.session_id'), ''),
		        json_extract(receipt_json, '$.credentialSubject.delegation'),
		        COALESCE(json_extract(receipt_json, '$.issuer.runtime.model'), '')
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

	out := make([]ReceiptRow, 0)
	for rows.Next() {
		var row ReceiptRow
		var delegationJSON sql.NullString
		if err := rows.Scan(
			&row.ID, &row.ChainID, &row.Sequence, &row.ActionType,
			&row.ToolName, &row.Server,
			&row.RiskLevel, &row.Status, &row.Timestamp, &row.IssuerID,
			&row.PrincipalID, &row.ReceiptHash, &row.PreviousReceiptHash,
			&row.ParametersInputPreview, &row.ParametersOutputPreview,
			&row.HasParametersDisclosure,
			&row.OutputStatusMismatch,
			&row.CorrelationID, &row.AgentID, &row.AgentType, &row.IssuerModel, &row.SessionID,
			&delegationJSON,
			&row.RuntimeModel,
		); err != nil {
			return nil, err
		}
		if delegationJSON.Valid && delegationJSON.String != "" && delegationJSON.String != "null" {
			row.Delegation = parseDelegationJSON(delegationJSON.String)
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

	out := make([]ChainReceipt, 0)
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

	out := make([]ChainSummary, 0)
	for rows.Next() {
		var cs ChainSummary
		if err := rows.Scan(&cs.ChainID, &cs.ReceiptCount, &cs.FirstTimestamp, &cs.LastTimestamp); err != nil {
			return nil, err
		}
		out = append(out, cs)
	}
	return out, rows.Err()
}

// Stats returns aggregate statistics. after and before are optional ISO-8601
// timestamps (inclusive lower / inclusive upper); nil means unbounded.
func (r *Reader) Stats(after, before *string) (Stats, error) {
	var st Stats

	// Build a shared WHERE clause applied to every query.
	var conds []string
	var args []any
	if after != nil {
		conds = append(conds, "timestamp >= ?")
		args = append(args, *after)
	}
	if before != nil {
		conds = append(conds, "timestamp <= ?")
		args = append(args, *before)
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	if err := r.db.QueryRow("SELECT COUNT(*) FROM receipts"+where, args...).Scan(&st.Total); err != nil {
		return Stats{}, err
	}
	if err := r.db.QueryRow("SELECT COUNT(DISTINCT chain_id) FROM receipts"+where, args...).Scan(&st.Chains); err != nil {
		return Stats{}, err
	}

	// MAX over an empty table returns NULL; scan into a nullable string so the
	// empty-store case is "" rather than an error.
	var latest sql.NullString
	if err := r.db.QueryRow("SELECT MAX(timestamp) FROM receipts"+where, args...).Scan(&latest); err != nil {
		return Stats{}, err
	}
	if latest.Valid {
		st.LatestTimestamp = latest.String
	}

	var err error
	st.ByRisk, err = r.groupByFiltered("risk_level", where, args)
	if err != nil {
		return Stats{}, err
	}
	st.ByStatus, err = r.groupByFiltered("status", where, args)
	if err != nil {
		return Stats{}, err
	}
	st.ByAction, err = r.groupByFiltered("action_type", where, args)
	if err != nil {
		return Stats{}, err
	}

	return st, nil
}

// TimeseriesStats returns one BucketRow per bucket from from (inclusive) to
// to (exclusive), stepping by bucket. Empty buckets are included with zero
// counts so callers get a continuous series. If from is the zero Time, the
// earliest receipt timestamp in the store is used; if the store is empty,
// returns an empty slice.
func (r *Reader) TimeseriesStats(from, to time.Time, bucket time.Duration) ([]BucketRow, error) {
	bucketSec := int64(bucket.Seconds())
	if bucketSec <= 0 {
		return nil, fmt.Errorf("bucket duration must be positive")
	}

	// When from is zero, resolve from the earliest receipt timestamp.
	if from.IsZero() {
		var earliest sql.NullString
		if err := r.db.QueryRow("SELECT MIN(timestamp) FROM receipts").Scan(&earliest); err != nil {
			return nil, err
		}
		if !earliest.Valid {
			// Empty store — return empty slice.
			return []BucketRow{}, nil
		}
		t, err := time.Parse(time.RFC3339, earliest.String)
		if err != nil {
			return nil, fmt.Errorf("parse earliest timestamp %q: %w", earliest.String, err)
		}
		from = t
	}

	// The SQL bound uses the exact requested window so e.g. range=24h returns at
	// most 24h of data. Bucket starts are floored to the bucket boundary so the
	// bars align on round times; the first/last bucket may therefore be partial.
	fromISO := from.UTC().Format(time.RFC3339)
	toISO := to.UTC().Format(time.RFC3339)

	fromSec := (from.Unix() / bucketSec) * bucketSec
	toSec := to.Unix()

	// Guard against absurd bucket counts (the number of buckets generated below).
	bucketCount := (toSec - fromSec + bucketSec - 1) / bucketSec
	if bucketCount > maxTimeseriesBuckets {
		return nil, fmt.Errorf("time range and bucket size would produce %d buckets (limit %d): narrow the range or increase the bucket size", bucketCount, maxTimeseriesBuckets)
	}

	// Query: group by floored epoch bucket, status, and risk_level.
	query := fmt.Sprintf(`
		SELECT
			(CAST(strftime('%%s', timestamp) AS INTEGER) / %d) * %d AS bucket_epoch,
			status,
			risk_level,
			COUNT(*) AS cnt
		FROM receipts
		WHERE timestamp >= ? AND timestamp < ?
		GROUP BY bucket_epoch, status, risk_level
		ORDER BY bucket_epoch ASC
	`, bucketSec, bucketSec)

	rows, err := r.db.Query(query, fromISO, toISO)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Accumulate query results into a map keyed by bucket epoch.
	type rowData struct {
		total    int
		byStatus map[string]int
		byRisk   map[string]int
	}
	bucketMap := map[int64]*rowData{}
	for rows.Next() {
		var epochSec int64
		var status, riskLevel string
		var cnt int
		if err := rows.Scan(&epochSec, &status, &riskLevel, &cnt); err != nil {
			return nil, err
		}
		rd := bucketMap[epochSec]
		if rd == nil {
			rd = &rowData{byStatus: map[string]int{}, byRisk: map[string]int{}}
			bucketMap[epochSec] = rd
		}
		rd.total += cnt
		rd.byStatus[status] += cnt
		rd.byRisk[riskLevel] += cnt
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Generate every bucket from from to to (exclusive), filling zeros where absent.
	out := make([]BucketRow, 0, bucketCount)
	for t := fromSec; t < toSec; t += bucketSec {
		ts := time.Unix(t, 0).UTC().Format(time.RFC3339)
		rd := bucketMap[t]
		if rd == nil {
			out = append(out, BucketRow{
				Ts:       ts,
				Total:    0,
				ByStatus: map[string]int{},
				ByRisk:   map[string]int{},
			})
		} else {
			out = append(out, BucketRow{
				Ts:       ts,
				Total:    rd.total,
				ByStatus: rd.byStatus,
				ByRisk:   rd.byRisk,
			})
		}
	}
	return out, nil
}

// SessionRow holds aggregate statistics for a single agent session.
type SessionRow struct {
	SessionID    string `json:"session_id"`
	ReceiptCount int    `json:"receipt_count"`
	AgentCount   int    `json:"agent_count"`
	FirstSeen    string `json:"first_seen"`
	LastSeen     string `json:"last_seen"`
}

// SessionStats returns one SessionRow per distinct session_id, ordered by
// last_seen DESC. Receipts without a session_id are excluded. The optional
// since parameter (ISO-8601 inclusive) restricts results to receipts at or
// after that timestamp; nil means all-time.
func (r *Reader) SessionStats(since *string) ([]SessionRow, error) {
	// COALESCE session_id to '' so a single != '' check handles both NULL and
	// empty-string exclusion, avoiding two json_extract calls per row in WHERE.
	conds := []string{
		"COALESCE(json_extract(receipt_json, '$.issuer.session_id'), '') != ''",
	}
	var args []any
	if since != nil {
		conds = append(conds, "timestamp >= ?")
		args = append(args, *since)
	}
	where := "WHERE " + strings.Join(conds, " AND ")

	query := fmt.Sprintf(`
		SELECT
			json_extract(receipt_json, '$.issuer.session_id') AS session_id,
			COUNT(*) AS receipt_count,
			COUNT(DISTINCT COALESCE(NULLIF(json_extract(receipt_json, '$.issuer.runtime.agent_id'), ''), json_extract(receipt_json, '$.issuer.id'))) AS agent_count,
			MIN(timestamp) AS first_seen,
			MAX(timestamp) AS last_seen
		FROM receipts
		%s
		GROUP BY session_id
		ORDER BY last_seen DESC
	`, where)

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]SessionRow, 0)
	for rows.Next() {
		var sr SessionRow
		if err := rows.Scan(&sr.SessionID, &sr.ReceiptCount, &sr.AgentCount, &sr.FirstSeen, &sr.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, sr)
	}
	return out, rows.Err()
}

// ActionStat holds aggregate statistics for a single action type.
type ActionStat struct {
	ActionType string `json:"action_type"`
	Total      int    `json:"total"`
	Success    int    `json:"success"`
	Failure    int    `json:"failure"`
	// FailureRate is the fraction of receipts that failed, in [0,1] (e.g. 0.05
	// is 5%). Matches the ratio convention used by ServerStats.
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

	out := make([]ActionStat, 0)
	for rows.Next() {
		var s ActionStat
		if err := rows.Scan(&s.ActionType, &s.Total, &s.Success, &s.Failure); err != nil {
			return nil, err
		}
		if s.Total > 0 {
			s.FailureRate = float64(s.Failure) / float64(s.Total)
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

// ServerStats returns per-server, per-tool receipt counts and failure rates.
// The optional since parameter (ISO-8601 inclusive) restricts results to
// receipts at or after that timestamp; nil means all-time.
// Rows whose extracted server value is empty are grouped into a missing-server
// bucket (Server == "") placed after all named servers in the result slice.
func (r *Reader) ServerStats(since *string) ([]ServerStat, error) {
	var args []any
	where := ""
	if since != nil {
		where = "WHERE timestamp >= ?"
		args = append(args, *since)
	}

	// Group by both server and tool in one pass. We COALESCE empty/NULL values
	// so Go sees a plain empty string rather than a SQL NULL.
	query := fmt.Sprintf(`
		SELECT
			COALESCE(json_extract(receipt_json, '$.credentialSubject.action.target.system'), '') AS server,
			COALESCE(tool_name, '') AS tool,
			COUNT(*) AS total,
			SUM(CASE WHEN status = 'failure' THEN 1 ELSE 0 END) AS failure
		FROM receipts
		%s
		GROUP BY server, tool
		ORDER BY total DESC, tool ASC
	`, where)

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type rawRow struct {
		server  string
		tool    string
		total   int
		failure int
	}
	serverMap := map[string]*ServerStat{}
	var serverOrder []string // insertion order

	for rows.Next() {
		var rr rawRow
		if err := rows.Scan(&rr.server, &rr.tool, &rr.total, &rr.failure); err != nil {
			return nil, err
		}
		// Key by the raw server value (empty string for a missing target.system)
		// rather than folding to "Unknown" here — folding now would merge a real
		// server literally named "Unknown" into the missing-server bucket. The
		// empty-string bucket is relabelled to "Unknown" for display below.
		st, ok := serverMap[rr.server]
		if !ok {
			st = &ServerStat{Server: rr.server}
			serverMap[rr.server] = st
			serverOrder = append(serverOrder, rr.server)
		}
		rate := 0.0
		if rr.total > 0 {
			rate = float64(rr.failure) / float64(rr.total)
		}
		st.Tools = append(st.Tools, ToolStat{
			ToolName:    rr.tool,
			Total:       rr.total,
			Failure:     rr.failure,
			FailureRate: rate,
		})
		st.Total += rr.total
		st.Failure += rr.failure
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Compute per-server failure rate and split named vs the missing-server
	// bucket (keyed by ""). The missing bucket keeps an empty server string in
	// the response so a real server literally named "Unknown" stays
	// distinguishable; the frontend renders "" as the "Unknown" label.
	named := make([]ServerStat, 0)
	var unknown *ServerStat
	for _, key := range serverOrder {
		st := serverMap[key]
		if st.Total > 0 {
			st.FailureRate = float64(st.Failure) / float64(st.Total)
		}
		if key == "" {
			unknown = st
		} else {
			named = append(named, *st)
		}
	}

	// Sort named servers DESC by total, tie-broken by server name ASC for a
	// deterministic order (avoids UI jitter across equivalent datasets).
	// Insertion sort is fine at dashboard scale.
	for i := 1; i < len(named); i++ {
		for j := i; j > 0; j-- {
			cur, prev := named[j], named[j-1]
			if cur.Total > prev.Total || (cur.Total == prev.Total && cur.Server < prev.Server) {
				named[j], named[j-1] = named[j-1], named[j]
			} else {
				break
			}
		}
	}

	// Sort each server's tools DESC by total, tie-break tool_name ASC.
	sortTools := func(tools []ToolStat) {
		for i := 1; i < len(tools); i++ {
			for j := i; j > 0; j-- {
				a, b := tools[j-1], tools[j]
				less := a.Total < b.Total || (a.Total == b.Total && a.ToolName > b.ToolName)
				if less {
					tools[j-1], tools[j] = tools[j], tools[j-1]
				} else {
					break
				}
			}
		}
	}
	for i := range named {
		sortTools(named[i].Tools)
	}

	out := named
	if unknown != nil {
		sortTools(unknown.Tools)
		out = append(out, *unknown)
	}
	return out, nil
}

// AttributionCoverage summarises how much of a session is identity-indexable.
// Roughly half a session may carry no file identity (shell/MCP/spawn receipts),
// so the fraction is surfaced in the UI so the view never implies blast-radius
// covers actions it cannot see.
type AttributionCoverage struct {
	TotalReceipts    int     `json:"total_receipts"`
	IdentityReceipts int     `json:"identity_receipts"`
	Fraction         float64 `json:"fraction"`
}

// NodeAttribution summarises one agent's contribution within a session.
type NodeAttribution struct {
	AgentKey      string         `json:"agent_key"` // agent_id or "__root__"
	AgentType     string         `json:"agent_type"`
	ReceiptCount  int            `json:"receipt_count"`
	IdentityCount int            `json:"identity_count"` // receipts with a resource path
	MaxRisk       string         `json:"max_risk"`
	RiskProfile   map[string]int `json:"risk_profile"`
	// SemanticDeps is a heuristic count of potentially-related receipts from
	// other agents within this agent's active time window, whose resources are
	// not already captured by a provable state-dependency edge. Surfaced as a
	// warning annotation in the UI — never a drawn edge (ADR-0029 §4).
	SemanticDeps int `json:"semantic_deps"`
}

// StatDepEdge is a provable state-dependency between two agents via a shared
// resource: agent FromAgent touched resource R, then ToAgent touched the same R
// (ordered by timestamp + chain + sequence). CrossAgent is always true here —
// same-agent re-touches are captured in BlastRadius, not as drawn edges.
type StatDepEdge struct {
	FromAgent  string   `json:"from_agent"`
	ToAgent    string   `json:"to_agent"`
	Resources  []string `json:"resources"`
	CrossAgent bool     `json:"cross_agent"`
}

// AttributionResult is the §4 attribution payload for a session: file-identity
// index, state-dependency edges, per-node blast-radius, and coverage fraction.
type AttributionResult struct {
	Coverage   AttributionCoverage `json:"coverage"`
	HasMoveOps bool                `json:"has_move_ops"`
	Nodes      []NodeAttribution   `json:"nodes"`
	StateDeps  []StatDepEdge       `json:"state_deps"`
	// BlastRadius maps each agent key to the sorted list of resource paths it
	// touched. Used by the frontend to enumerate what a node can affect on click.
	BlastRadius map[string][]string `json:"blast_radius"`
}

// riskOrder maps risk level strings to a numeric ordering for max-risk
// comparison. An empty string (missing level) ranks below "low".
var riskOrder = map[string]int{
	"low":      1,
	"medium":   2,
	"high":     3,
	"critical": 4,
}

// higherRisk returns whichever of a or b ranks higher by riskOrder.
func higherRisk(a, b string) string {
	if riskOrder[b] > riskOrder[a] {
		return b
	}
	return a
}

// SessionAttribution computes the §4 attribution payload for a session.
//
// action.target.resource is extracted from receipt_json at query time (no
// schema migration) and used as a proxy for logical file identity. When
// move/rename operations are detected, HasMoveOps is set so the caller can
// warn that path strings may not reliably identify file versions across renames.
//
// All computation is read-only; the database is not modified.
func (r *Reader) SessionAttribution(sessionID string) (AttributionResult, error) {
	rows, err := r.db.Query(`
		SELECT chain_id,
		       sequence,
		       COALESCE(json_extract(receipt_json, '$.issuer.runtime.agent_id'), '') AS agent_id,
		       COALESCE(json_extract(receipt_json, '$.issuer.runtime.agent_type'), '') AS agent_type,
		       action_type,
		       risk_level,
		       timestamp,
		       COALESCE(json_extract(receipt_json, '$.credentialSubject.action.target.resource'), '') AS resource
		FROM receipts
		WHERE json_extract(receipt_json, '$.issuer.session_id') = ?
		ORDER BY timestamp ASC, chain_id ASC, sequence ASC
	`, sessionID)
	if err != nil {
		return AttributionResult{}, err
	}
	defer rows.Close()

	type rawRow struct {
		chainID    string
		sequence   int
		agentID    string
		agentType  string
		actionType string
		riskLevel  string
		timestamp  string
		resource   string
	}
	var all []rawRow
	for rows.Next() {
		var rr rawRow
		if err := rows.Scan(&rr.chainID, &rr.sequence, &rr.agentID, &rr.agentType,
			&rr.actionType, &rr.riskLevel, &rr.timestamp, &rr.resource); err != nil {
			return AttributionResult{}, err
		}
		all = append(all, rr)
	}
	if err := rows.Err(); err != nil {
		return AttributionResult{}, err
	}

	if len(all) == 0 {
		return AttributionResult{
			Coverage:    AttributionCoverage{},
			Nodes:       []NodeAttribution{},
			StateDeps:   []StatDepEdge{},
			BlastRadius: map[string][]string{},
		}, nil
	}

	agentKeyOf := func(id string) string {
		if id == "" {
			return "__root__"
		}
		return id
	}

	type nodeAcc struct {
		agentType     string
		receiptCount  int
		identityCount int
		maxRisk       string
		riskProfile   map[string]int
		timestamps    []string
		semCount      int
	}
	nodeMap := map[string]*nodeAcc{}

	// file identity index: resource → ordered touches (agentKey + timestamp).
	type touch struct {
		agentKey  string
		timestamp string
	}
	fileIndex := map[string][]touch{}

	hasMoveOps := false
	for _, rr := range all {
		ak := agentKeyOf(rr.agentID)
		nd := nodeMap[ak]
		if nd == nil {
			nd = &nodeAcc{riskProfile: map[string]int{}}
			nodeMap[ak] = nd
		}
		if nd.agentType == "" && rr.agentType != "" {
			nd.agentType = rr.agentType
		}
		nd.receiptCount++
		if rr.riskLevel != "" {
			nd.riskProfile[rr.riskLevel]++
			nd.maxRisk = higherRisk(nd.maxRisk, rr.riskLevel)
		}
		nd.timestamps = append(nd.timestamps, rr.timestamp)

		at := rr.actionType
		if at == "filesystem.file.move" || at == "filesystem.file.rename" {
			hasMoveOps = true
		}
		if rr.resource != "" {
			fileIndex[rr.resource] = append(fileIndex[rr.resource], touch{ak, rr.timestamp})
			nd.identityCount++
		}
	}

	// Cross-agent state dep edges: for each resource, each consecutive pair of
	// touches where the agent key differs constitutes a provable state dep.
	// Edge keys are canonicalised (from < to) so A→B and B→A on the same
	// resource collapse to a single undirected edge instead of two
	// contradictory arrows.
	type edgeKey struct{ from, to string }
	edgeResources := map[edgeKey]map[string]bool{}
	for resource, touches := range fileIndex {
		for i := 1; i < len(touches); i++ {
			if touches[i-1].agentKey == touches[i].agentKey {
				continue // same-agent re-touch; captured in blast radius
			}
			a, b := touches[i-1].agentKey, touches[i].agentKey
			if a > b {
				a, b = b, a
			}
			ek := edgeKey{a, b}
			if edgeResources[ek] == nil {
				edgeResources[ek] = map[string]bool{}
			}
			edgeResources[ek][resource] = true
		}
	}

	stateDeps := make([]StatDepEdge, 0, len(edgeResources))
	for ek, rset := range edgeResources {
		resources := make([]string, 0, len(rset))
		for res := range rset {
			resources = append(resources, res)
		}
		sort.Strings(resources)
		stateDeps = append(stateDeps, StatDepEdge{
			FromAgent:  ek.from,
			ToAgent:    ek.to,
			Resources:  resources,
			CrossAgent: true,
		})
	}
	sort.Slice(stateDeps, func(i, j int) bool {
		if stateDeps[i].FromAgent != stateDeps[j].FromAgent {
			return stateDeps[i].FromAgent < stateDeps[j].FromAgent
		}
		return stateDeps[i].ToAgent < stateDeps[j].ToAgent
	})

	// Blast radius: per-agent sorted unique resource list.
	blastRadius := map[string][]string{}
	for resource, touches := range fileIndex {
		seen := map[string]bool{}
		for _, t := range touches {
			if !seen[t.agentKey] {
				seen[t.agentKey] = true
				blastRadius[t.agentKey] = append(blastRadius[t.agentKey], resource)
			}
		}
	}
	for k := range blastRadius {
		sort.Strings(blastRadius[k])
	}

	// Semantic dep heuristic: for each agent X, count distinct resources touched
	// by other agents within X's active time window that are not already covered
	// by a cross-agent state dep involving X. These represent potential co-turn
	// couplings that are unproven and must never be drawn as edges (ADR-0029 §4).
	agentStatDepRes := map[string]map[string]bool{}
	for _, sd := range stateDeps {
		if agentStatDepRes[sd.FromAgent] == nil {
			agentStatDepRes[sd.FromAgent] = map[string]bool{}
		}
		if agentStatDepRes[sd.ToAgent] == nil {
			agentStatDepRes[sd.ToAgent] = map[string]bool{}
		}
		for _, res := range sd.Resources {
			agentStatDepRes[sd.FromAgent][res] = true
			agentStatDepRes[sd.ToAgent][res] = true
		}
	}

	for ak, nd := range nodeMap {
		if len(nd.timestamps) == 0 {
			continue
		}
		first, last := nd.timestamps[0], nd.timestamps[len(nd.timestamps)-1]
		seen := map[string]bool{}
		for _, rr := range all {
			rk := agentKeyOf(rr.agentID)
			if rk == ak || rr.resource == "" {
				continue
			}
			if rr.timestamp < first || rr.timestamp > last {
				continue
			}
			if agentStatDepRes[ak] != nil && agentStatDepRes[ak][rr.resource] {
				continue
			}
			seen[rr.resource] = true
		}
		nd.semCount = len(seen)
	}

	// Build result nodes in deterministic order.
	nodeKeys := make([]string, 0, len(nodeMap))
	for k := range nodeMap {
		nodeKeys = append(nodeKeys, k)
	}
	sort.Strings(nodeKeys)

	resultNodes := make([]NodeAttribution, 0, len(nodeMap))
	for _, ak := range nodeKeys {
		nd := nodeMap[ak]
		resultNodes = append(resultNodes, NodeAttribution{
			AgentKey:      ak,
			AgentType:     nd.agentType,
			ReceiptCount:  nd.receiptCount,
			IdentityCount: nd.identityCount,
			MaxRisk:       nd.maxRisk,
			RiskProfile:   nd.riskProfile,
			SemanticDeps:  nd.semCount,
		})
	}

	totalReceipts := len(all)
	identityReceipts := 0
	for _, rr := range all {
		if rr.resource != "" {
			identityReceipts++
		}
	}
	frac := 0.0
	if totalReceipts > 0 {
		frac = float64(identityReceipts) / float64(totalReceipts)
	}

	return AttributionResult{
		Coverage: AttributionCoverage{
			TotalReceipts:    totalReceipts,
			IdentityReceipts: identityReceipts,
			Fraction:         frac,
		},
		HasMoveOps:  hasMoveOps,
		Nodes:       resultNodes,
		StateDeps:   stateDeps,
		BlastRadius: blastRadius,
	}, nil
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

// parseDelegationJSON decodes the credentialSubject.delegation JSON object and
// returns a DelegationInfo. Unknown fields are ignored. Returns nil on any
// parse error so callers degrade gracefully on malformed or forward-compat JSON.
func parseDelegationJSON(s string) *DelegationInfo {
	var wire struct {
		ParentChainID   string `json:"parent_chain_id"`
		ParentReceiptID string `json:"parent_receipt_id"`
		Delegator       struct {
			ID string `json:"id"`
		} `json:"delegator"`
	}
	if err := json.Unmarshal([]byte(s), &wire); err != nil {
		return nil
	}
	if wire.ParentChainID == "" || wire.ParentReceiptID == "" {
		return nil
	}
	return &DelegationInfo{
		ParentChainID:   wire.ParentChainID,
		ParentReceiptID: wire.ParentReceiptID,
		DelegatorID:     wire.Delegator.ID,
	}
}

// escapeLikeTerm escapes a user-supplied search term so that %, _, and \ are
// treated as literal characters in a SQLite LIKE pattern using backslash as the
// escape character (ESCAPE '\').
func escapeLikeTerm(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

func (r *Reader) groupBy(column string) ([]GroupCount, error) {
	return r.groupByFiltered(column, "", nil)
}

func (r *Reader) groupByFiltered(column, where string, args []any) ([]GroupCount, error) {
	if !allowedGroupByColumns[column] {
		return nil, fmt.Errorf("invalid group-by column: %q", column)
	}
	query := fmt.Sprintf(
		"SELECT %s, COUNT(*) FROM receipts%s GROUP BY %s ORDER BY COUNT(*) DESC",
		column, where, column,
	)
	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]GroupCount, 0)
	for rows.Next() {
		var gc GroupCount
		if err := rows.Scan(&gc.Label, &gc.Count); err != nil {
			return nil, err
		}
		out = append(out, gc)
	}
	return out, rows.Err()
}
