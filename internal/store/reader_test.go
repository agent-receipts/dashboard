package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"obsigna.dev/sdk/go/receipt"
	sdkstore "obsigna.dev/sdk/go/store"
)

// testDisclosureEnvelope returns a sentinel HPKE disclosure envelope sized to
// match the v1 ciphersuite (ADR-0012). Values are placeholder bytes — never
// decryptable — but the receipt store does not validate envelope contents, so
// they exercise the "has disclosure" code paths end-to-end.
func testDisclosureEnvelope(kid string, ct string) *receipt.DisclosureEnvelope {
	return &receipt.DisclosureEnvelope{
		V:   "1",
		Alg: "hpke-x25519-hkdf-sha256-aes-256-gcm",
		Recipients: []receipt.DisclosureRecipient{{
			KID: kid,
			// 43-char unpadded base64url placeholder matching X25519 enc width.
			Enc: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		}},
		CT: ct,
	}
}

func makeReceipt(id, chainID string, seq int, actionType string, risk receipt.RiskLevel, status receipt.OutcomeStatus, ts string, prevHash *string) receipt.AgentReceipt {
	return receipt.AgentReceipt{
		Context:      receipt.Context(),
		ID:           id,
		Type:         receipt.CredentialType(),
		Version:      receipt.Version,
		Issuer:       receipt.Issuer{ID: "did:agent:test-agent"},
		IssuanceDate: ts,
		CredentialSubject: receipt.CredentialSubject{
			Principal: receipt.Principal{ID: "did:user:test-user"},
			Action: receipt.Action{
				ID:        "act_" + id,
				Type:      actionType,
				RiskLevel: risk,
				Timestamp: ts,
			},
			Outcome: receipt.Outcome{Status: status},
			Chain: receipt.Chain{
				Sequence:            seq,
				PreviousReceiptHash: prevHash,
				ChainID:             chainID,
			},
		},
		Proof: receipt.Proof{
			Type:       "Ed25519Signature2020",
			ProofValue: "u" + id, // dummy
		},
	}
}

func makeReceiptWithTool(id, chainID string, seq int, actionType, toolName, server string, risk receipt.RiskLevel, status receipt.OutcomeStatus, ts string) receipt.AgentReceipt {
	ar := makeReceipt(id, chainID, seq, actionType, risk, status, ts, nil)
	ar.CredentialSubject.Action.ToolName = toolName
	if server != "" {
		ar.CredentialSubject.Action.Target = &receipt.ActionTarget{System: server}
	}
	return ar
}

func strPtr(s string) *string { return &s }

// The reader must be constructable from an existing SDK store's DB.
// Since SDK stores use in-memory DBs in tests and the reader needs a file path,
// we test with a temp file instead.

func TestOpenReadOnly_NonExistentFile(t *testing.T) {
	_, err := OpenReadOnly("/nonexistent/path/to/db.sqlite")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestOpenReadOnly_FileDB(t *testing.T) {
	dbPath := seedFileDB(t)
	r, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()
}

func TestReader_GetByID(t *testing.T) {
	dbPath := seedFileDB(t)
	r, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()

	got, err := r.GetByID("urn:receipt:001")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("expected receipt, got nil")
	}
	if got.ID != "urn:receipt:001" {
		t.Errorf("got ID %q, want %q", got.ID, "urn:receipt:001")
	}
}

func TestReader_GetByID_NotFound(t *testing.T) {
	dbPath := seedFileDB(t)
	r, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()

	got, err := r.GetByID("urn:receipt:nonexistent")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestReader_ListReceipts_NoFilter(t *testing.T) {
	dbPath := seedFileDB(t)
	r, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()

	rows, err := r.ListReceipts(Filter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 5 {
		t.Errorf("got %d rows, want 5", len(rows))
	}
}

func TestReader_ListReceipts_FilterByRisk(t *testing.T) {
	dbPath := seedFileDB(t)
	r, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()

	rows, err := r.ListReceipts(Filter{RiskLevel: strPtr("high")})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("got %d high-risk rows, want 2", len(rows))
	}
	for _, row := range rows {
		if row.RiskLevel != "high" {
			t.Errorf("got risk_level %q, want high", row.RiskLevel)
		}
	}
}

func TestReader_ListReceipts_FilterByActionType(t *testing.T) {
	dbPath := seedFileDB(t)
	r, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()

	rows, err := r.ListReceipts(Filter{ActionType: strPtr("filesystem.file.read")})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("got %d rows, want 1", len(rows))
	}
}

func TestReader_ListReceipts_FilterByStatus(t *testing.T) {
	dbPath := seedFileDB(t)
	r, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()

	rows, err := r.ListReceipts(Filter{Status: strPtr("failure")})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("got %d rows, want 1", len(rows))
	}
	if rows[0].ID != "urn:receipt:004" {
		t.Errorf("got ID %q, want urn:receipt:004", rows[0].ID)
	}
}

func TestReader_ListReceipts_FilterByChainID(t *testing.T) {
	dbPath := seedFileDB(t)
	r, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()

	rows, err := r.ListReceipts(Filter{ChainID: strPtr("chain-2")})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("got %d rows, want 2", len(rows))
	}
}

func TestReader_ListReceipts_FilterByTimeRange(t *testing.T) {
	dbPath := seedFileDB(t)
	r, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()

	rows, err := r.ListReceipts(Filter{
		After:  strPtr("2026-04-01T10:01:00Z"),
		Before: strPtr("2026-04-01T10:02:00Z"),
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("got %d rows, want 2", len(rows))
	}
}

func TestReader_ListReceipts_FilterBySince(t *testing.T) {
	dbPath := seedFileDB(t)
	r, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()

	// Since is inclusive: a row whose timestamp equals the watermark must be
	// returned so callers don't silently lose receipts that share a second
	// with the boundary (timestamps may have only second precision). The
	// client dedups against the rows it already shows.
	rows, err := r.ListReceipts(Filter{Since: strPtr("2026-04-01T10:01:00Z")})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("got %d rows, want 4", len(rows))
	}
	var sawBoundary bool
	for _, row := range rows {
		if row.Timestamp < "2026-04-01T10:01:00Z" {
			t.Errorf("row %s has timestamp %q, want >= watermark", row.ID, row.Timestamp)
		}
		if row.ID == "urn:receipt:002" {
			sawBoundary = true
		}
	}
	if !sawBoundary {
		t.Error("inclusive watermark must re-emit the row at the boundary")
	}

	// A watermark strictly newer than every row returns nothing.
	rows, err = r.ListReceipts(Filter{Since: strPtr("2027-01-01T00:00:00Z")})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("got %d rows, want 0", len(rows))
	}
}

func TestReader_ListReceipts_Since_DrainsBurstAcrossPolls(t *testing.T) {
	// A burst larger than the LIMIT must not leak rows: with `Since` set the
	// query orders ASC, so two successive polls (advancing the watermark)
	// recover every row instead of dropping the middle of the range.
	dbPath := seedFileDB(t)
	r, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()

	since := "2026-04-01T10:00:00Z"
	limit := 3

	first, err := r.ListReceipts(Filter{Since: &since, Limit: &limit})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(first) != 3 {
		t.Fatalf("first page got %d rows, want 3", len(first))
	}
	// ASC ordering so the oldest rows come first.
	for i := 1; i < len(first); i++ {
		if first[i-1].Timestamp > first[i].Timestamp {
			t.Errorf("row %d (%s) older than row %d (%s); want ASC order",
				i, first[i].Timestamp, i-1, first[i-1].Timestamp)
		}
	}

	// Advance the watermark to the newest returned row and poll again.
	watermark := first[len(first)-1].Timestamp
	second, err := r.ListReceipts(Filter{Since: &watermark, Limit: &limit})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	// Merge by id (the inclusive boundary may re-emit one row; the client
	// dedups, which we mirror here) and confirm we recovered every row in
	// the burst — not the middle-of-range silent drop that DESC + LIMIT
	// would have produced.
	seen := map[string]bool{}
	for _, row := range first {
		seen[row.ID] = true
	}
	for _, row := range second {
		seen[row.ID] = true
	}
	if len(seen) != 5 {
		t.Errorf("paginated drain saw %d distinct rows, want 5", len(seen))
	}
}

func TestReader_ListReceipts_Limit(t *testing.T) {
	dbPath := seedFileDB(t)
	r, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()

	limit := 2
	rows, err := r.ListReceipts(Filter{Limit: &limit})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("got %d rows, want 2", len(rows))
	}
}

func TestReader_ListReceipts_ServerAndTool(t *testing.T) {
	dbPath := t.TempDir() + "/tool-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}

	withTool := makeReceiptWithTool("urn:receipt:t1", "chain-tool", 1,
		"tool.call", "read_file", "filesystem", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:00:00Z")
	withoutTarget := makeReceiptWithTool("urn:receipt:t2", "chain-tool", 2,
		"tool.call", "list_dir", "", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:01:00Z")
	withoutTool := makeReceiptWithTool("urn:receipt:t3", "chain-tool", 3,
		"tool.call", "", "jira", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:02:00Z")

	for _, r := range []receipt.AgentReceipt{withTool, withoutTarget, withoutTool} {
		hash, err := receipt.HashReceipt(r)
		if err != nil {
			t.Fatalf("hash: %v", err)
		}
		if err := s.Insert(r, hash); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	s.Close()

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer reader.Close()

	rows, err := reader.ListReceipts(Filter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}

	// rows are newest-first: t3 (index 0), t2 (index 1), t1 (index 2).
	rowT3 := rows[0]
	rowT2 := rows[1]
	rowT1 := rows[2]

	if rowT1.ToolName != "read_file" {
		t.Errorf("tool_name: got %q, want %q", rowT1.ToolName, "read_file")
	}
	if rowT1.Server != "filesystem" {
		t.Errorf("server: got %q, want %q", rowT1.Server, "filesystem")
	}
	if rowT2.ToolName != "list_dir" {
		t.Errorf("tool_name: got %q, want %q", rowT2.ToolName, "list_dir")
	}
	if rowT2.Server != "" {
		t.Errorf("server: got %q, want empty (nil target)", rowT2.Server)
	}
	if rowT3.ToolName != "" {
		t.Errorf("tool_name: got %q, want empty", rowT3.ToolName)
	}
	if rowT3.Server != "jira" {
		t.Errorf("server: got %q, want %q", rowT3.Server, "jira")
	}
}

func TestReader_ListReceipts_FilterByServer(t *testing.T) {
	dbPath := t.TempDir() + "/server-filter-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}

	r1 := makeReceiptWithTool("urn:receipt:sf1", "chain-sf", 1, "tool.call", "read_file", "filesystem", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:00:00Z")
	r2 := makeReceiptWithTool("urn:receipt:sf2", "chain-sf", 2, "tool.call", "list_dir", "filesystem", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:01:00Z")
	r3 := makeReceiptWithTool("urn:receipt:sf3", "chain-sf", 3, "tool.call", "create_issue", "jira", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:02:00Z")

	for _, r := range []receipt.AgentReceipt{r1, r2, r3} {
		h, err := receipt.HashReceipt(r)
		if err != nil {
			t.Fatalf("hash: %v", err)
		}
		if err := s.Insert(r, h); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	s.Close()

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer reader.Close()

	// Filter by server "filesystem" — should return 2 rows.
	rows, err := reader.ListReceipts(Filter{Server: strPtr("filesystem")})
	if err != nil {
		t.Fatalf("list by server: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("got %d rows by server=filesystem, want 2", len(rows))
	}
	for _, row := range rows {
		if row.Server != "filesystem" {
			t.Errorf("row server %q, want filesystem", row.Server)
		}
	}

	// Filter by tool_name "read_file" — should return 1 row.
	rows, err = reader.ListReceipts(Filter{ToolName: strPtr("read_file")})
	if err != nil {
		t.Fatalf("list by tool_name: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("got %d rows by tool_name=read_file, want 1", len(rows))
	}
	if len(rows) == 1 && rows[0].ToolName != "read_file" {
		t.Errorf("row tool_name %q, want read_file", rows[0].ToolName)
	}

	// Filter by server + tool_name combined — should return 1 row.
	rows, err = reader.ListReceipts(Filter{Server: strPtr("filesystem"), ToolName: strPtr("list_dir")})
	if err != nil {
		t.Fatalf("list by server+tool: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("got %d rows by server=filesystem tool_name=list_dir, want 1", len(rows))
	}

	// Non-matching filter — should return 0 rows.
	rows, err = reader.ListReceipts(Filter{Server: strPtr("nonexistent")})
	if err != nil {
		t.Fatalf("list nonexistent server: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("got %d rows for nonexistent server, want 0", len(rows))
	}
}

func TestServerStats(t *testing.T) {
	dbPath := t.TempDir() + "/serverstats-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}

	// Server A: 3 calls (2 success, 1 failure), 2 tools.
	// Server B: 2 calls (1 success, 1 failure), 1 tool.
	// No server: 1 call (1 failure) — the missing-server bucket (Server == "").
	receipts := []receipt.AgentReceipt{
		makeReceiptWithTool("urn:receipt:ss1", "chain-ss", 1, "tool.call", "tool_a1", "server-a", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:00:00Z"),
		makeReceiptWithTool("urn:receipt:ss2", "chain-ss", 2, "tool.call", "tool_a1", "server-a", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:01:00Z"),
		makeReceiptWithTool("urn:receipt:ss3", "chain-ss", 3, "tool.call", "tool_a2", "server-a", receipt.RiskLow, receipt.StatusFailure, "2026-04-01T10:02:00Z"),
		makeReceiptWithTool("urn:receipt:ss4", "chain-ss", 4, "tool.call", "tool_b1", "server-b", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:03:00Z"),
		makeReceiptWithTool("urn:receipt:ss5", "chain-ss", 5, "tool.call", "tool_b1", "server-b", receipt.RiskLow, receipt.StatusFailure, "2026-04-01T10:04:00Z"),
		makeReceiptWithTool("urn:receipt:ss6", "chain-ss", 6, "tool.call", "", "", receipt.RiskLow, receipt.StatusFailure, "2026-04-01T10:05:00Z"),
	}
	for _, r := range receipts {
		h, err := receipt.HashReceipt(r)
		if err != nil {
			t.Fatalf("hash: %v", err)
		}
		if err := s.Insert(r, h); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	s.Close()

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer reader.Close()

	// All-time stats.
	stats, err := reader.ServerStats(nil)
	if err != nil {
		t.Fatalf("ServerStats: %v", err)
	}

	// Expect 3 servers: server-a (3), server-b (2), Unknown (1).
	if len(stats) != 3 {
		t.Fatalf("got %d servers, want 3: %+v", len(stats), stats)
	}

	// server-a must come first (highest total).
	if stats[0].Server != "server-a" {
		t.Errorf("stats[0].Server = %q, want server-a", stats[0].Server)
	}
	if stats[0].Total != 3 {
		t.Errorf("server-a total = %d, want 3", stats[0].Total)
	}
	if stats[0].Failure != 1 {
		t.Errorf("server-a failure = %d, want 1", stats[0].Failure)
	}
	wantRate := 1.0 / 3.0
	if diff := stats[0].FailureRate - wantRate; diff < -1e-9 || diff > 1e-9 {
		t.Errorf("server-a failure_rate = %f, want %f", stats[0].FailureRate, wantRate)
	}
	if len(stats[0].Tools) != 2 {
		t.Errorf("server-a tools = %d, want 2", len(stats[0].Tools))
	}
	// tool_a1 (2 total) comes before tool_a2 (1 total).
	if stats[0].Tools[0].ToolName != "tool_a1" {
		t.Errorf("server-a tools[0].ToolName = %q, want tool_a1", stats[0].Tools[0].ToolName)
	}
	if stats[0].Tools[0].Total != 2 {
		t.Errorf("server-a tools[0].Total = %d, want 2", stats[0].Tools[0].Total)
	}
	if stats[0].Tools[0].Failure != 0 {
		t.Errorf("server-a tools[0].Failure = %d, want 0", stats[0].Tools[0].Failure)
	}

	// server-b.
	if stats[1].Server != "server-b" {
		t.Errorf("stats[1].Server = %q, want server-b", stats[1].Server)
	}
	if stats[1].Total != 2 {
		t.Errorf("server-b total = %d, want 2", stats[1].Total)
	}
	if stats[1].Failure != 1 {
		t.Errorf("server-b failure = %d, want 1", stats[1].Failure)
	}
	if diff := stats[1].FailureRate - 0.5; diff < -1e-9 || diff > 1e-9 {
		t.Errorf("server-b failure_rate = %f, want 0.5", stats[1].FailureRate)
	}

	// The missing-server bucket (Server == "") must be last.
	if stats[2].Server != "" {
		t.Errorf("stats[2].Server = %q, want \"\" (missing-server bucket)", stats[2].Server)
	}
	if stats[2].Total != 1 {
		t.Errorf("missing-server total = %d, want 1", stats[2].Total)
	}
	if stats[2].Failure != 1 {
		t.Errorf("missing-server failure = %d, want 1", stats[2].Failure)
	}
	if diff := stats[2].FailureRate - 1.0; diff < -1e-9 || diff > 1e-9 {
		t.Errorf("missing-server failure_rate = %f, want 1.0", stats[2].FailureRate)
	}

	// Since filter — only receipts from 10:03 onward: ss4 (server-b success), ss5 (server-b failure), ss6 (unknown failure).
	since := "2026-04-01T10:03:00Z"
	filtered, err := reader.ServerStats(&since)
	if err != nil {
		t.Fatalf("ServerStats since: %v", err)
	}
	// 2 servers: server-b (2) and the missing-server bucket (1).
	if len(filtered) != 2 {
		t.Fatalf("since filter: got %d servers, want 2: %+v", len(filtered), filtered)
	}
	if filtered[0].Server != "server-b" {
		t.Errorf("since filter stats[0].Server = %q, want server-b", filtered[0].Server)
	}
	if filtered[0].Total != 2 {
		t.Errorf("since filter server-b total = %d, want 2", filtered[0].Total)
	}
	if filtered[1].Server != "" {
		t.Errorf("since filter stats[1].Server = %q, want \"\" (missing-server bucket)", filtered[1].Server)
	}
}

// A server literally named "Unknown" must not be merged into the missing-server
// bucket. The missing bucket is returned with an empty Server string; only the
// frontend renders "" as the "Unknown" label.
func TestServerStats_LiteralUnknownNotMerged(t *testing.T) {
	dbPath := t.TempDir() + "/serverstats-unknown.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}

	receipts := []receipt.AgentReceipt{
		// A real server whose system name happens to be "Unknown".
		makeReceiptWithTool("urn:receipt:u1", "chain-u", 1, "tool.call", "tool_u", "Unknown", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:00:00Z"),
		makeReceiptWithTool("urn:receipt:u2", "chain-u", 2, "tool.call", "tool_u", "Unknown", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:01:00Z"),
		// A receipt with no server — the missing-server bucket.
		makeReceiptWithTool("urn:receipt:u3", "chain-u", 3, "tool.call", "", "", receipt.RiskLow, receipt.StatusFailure, "2026-04-01T10:02:00Z"),
	}
	for _, r := range receipts {
		h, err := receipt.HashReceipt(r)
		if err != nil {
			t.Fatalf("hash: %v", err)
		}
		if err := s.Insert(r, h); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	s.Close()

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer reader.Close()

	stats, err := reader.ServerStats(nil)
	if err != nil {
		t.Fatalf("ServerStats: %v", err)
	}

	// Two distinct buckets: the real server keeps Server == "Unknown"
	// (2 receipts, 0 failures); the missing-server bucket keeps Server == ""
	// (1 receipt, 1 failure). They must not be conflated.
	if len(stats) != 2 {
		t.Fatalf("got %d server groups, want 2 (real 'Unknown' must stay separate from missing): %+v", len(stats), stats)
	}
	var foundReal, foundMissing bool
	for _, st := range stats {
		switch st.Server {
		case "Unknown":
			foundReal = true
			if st.Total != 2 || st.Failure != 0 {
				t.Errorf("real 'Unknown' server = {total %d, failure %d}, want {2, 0}", st.Total, st.Failure)
			}
		case "":
			foundMissing = true
			if st.Total != 1 || st.Failure != 1 {
				t.Errorf("missing-server bucket = {total %d, failure %d}, want {1, 1}", st.Total, st.Failure)
			}
		default:
			t.Errorf("unexpected server label %q", st.Server)
		}
	}
	if !foundReal {
		t.Error("did not find the real 'Unknown' server bucket (Server == \"Unknown\")")
	}
	if !foundMissing {
		t.Error("did not find the missing-server bucket (Server == \"\")")
	}
}

func TestServerStats_Empty(t *testing.T) {
	dbPath := seedEmptyDB(t)
	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer reader.Close()

	stats, err := reader.ServerStats(nil)
	if err != nil {
		t.Fatalf("ServerStats: %v", err)
	}
	if len(stats) != 0 {
		t.Errorf("empty store: got %d servers, want 0", len(stats))
	}
}

func TestServerStats_ToolOrdering(t *testing.T) {
	// Two tools with equal totals — tie-break by tool_name ASC.
	dbPath := t.TempDir() + "/tool-order-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}

	recs := []receipt.AgentReceipt{
		makeReceiptWithTool("urn:receipt:to1", "chain-to", 1, "tool.call", "zebra", "srv", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:00:00Z"),
		makeReceiptWithTool("urn:receipt:to2", "chain-to", 2, "tool.call", "alpha", "srv", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:01:00Z"),
	}
	for _, r := range recs {
		h, err := receipt.HashReceipt(r)
		if err != nil {
			t.Fatalf("hash: %v", err)
		}
		if err := s.Insert(r, h); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	s.Close()

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer reader.Close()

	stats, err := reader.ServerStats(nil)
	if err != nil {
		t.Fatalf("ServerStats: %v", err)
	}
	if len(stats) != 1 || len(stats[0].Tools) != 2 {
		t.Fatalf("unexpected shape: %+v", stats)
	}
	// Equal totals → alphabetical ASC: alpha before zebra.
	if stats[0].Tools[0].ToolName != "alpha" {
		t.Errorf("tools[0].ToolName = %q, want alpha (tie-break ASC)", stats[0].Tools[0].ToolName)
	}
	if stats[0].Tools[1].ToolName != "zebra" {
		t.Errorf("tools[1].ToolName = %q, want zebra", stats[0].Tools[1].ToolName)
	}
}

func TestReader_ListReceipts_ParametersDisclosurePresence(t *testing.T) {
	// Under the v0.3.0 envelope shape (ADR-0012), parameters_disclosure is an
	// opaque HPKE ciphertext blob — there are no `input`/`output` keys to
	// preview. The list view's HasParametersDisclosure indicator must still
	// fire whenever an envelope is present, and the preview columns must
	// remain empty (the SQL json_extract paths target keys that no longer
	// exist on the wire). Rendering the envelope itself is out of scope here;
	// see the follow-up issue for the detail-view UX.
	dbPath := t.TempDir() + "/disclosure-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}

	withDisclosure := makeReceipt("urn:receipt:d1", "chain-d", 1,
		"tool.call", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:00:00Z", nil)
	withDisclosure.CredentialSubject.Action.ParametersDisclosure = testDisclosureEnvelope(
		"did:key:test#enc-1", "ciphertext-placeholder-d1")

	withoutDisclosure := makeReceipt("urn:receipt:d2", "chain-d", 2,
		"tool.call", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:01:00Z", nil)

	for _, r := range []receipt.AgentReceipt{withDisclosure, withoutDisclosure} {
		hash, err := receipt.HashReceipt(r)
		if err != nil {
			t.Fatalf("hash: %v", err)
		}
		if err := s.Insert(r, hash); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	s.Close()

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer reader.Close()

	rows, err := reader.ListReceipts(Filter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}

	// newest-first: d2 (no envelope), d1 (envelope).
	if rows[0].HasParametersDisclosure {
		t.Errorf("d2 (no envelope) got HasParametersDisclosure=true, want false")
	}
	if rows[0].ParametersInputPreview != "" || rows[0].ParametersOutputPreview != "" {
		t.Errorf("d2 (no envelope) got non-empty previews input=%q output=%q",
			rows[0].ParametersInputPreview, rows[0].ParametersOutputPreview)
	}

	if !rows[1].HasParametersDisclosure {
		t.Errorf("d1 (envelope present) got HasParametersDisclosure=false, want true")
	}
	// The envelope is opaque to the reader, so the legacy input/output
	// previews must be empty — surfacing ciphertext would be misleading.
	if rows[1].ParametersInputPreview != "" || rows[1].ParametersOutputPreview != "" {
		t.Errorf("d1 (envelope) got non-empty previews input=%q output=%q; envelope payload must not leak into list view",
			rows[1].ParametersInputPreview, rows[1].ParametersOutputPreview)
	}
}

func TestReader_ListReceipts_OutputStatusMismatch(t *testing.T) {
	// Pre-v0.3.0 the reader inspected parameters_disclosure.output for an
	// isError:true marker so it could flag MCP receipts whose outcome.status
	// had been stamped before the response payload was inspected (issue #50).
	// Under ADR-0012 the envelope is opaque ciphertext — the dashboard cannot
	// peek inside without the forensic private key — so OutputStatusMismatch
	// must always be false at the list-view layer, regardless of whether an
	// envelope is attached. Detecting the mismatch will move to whatever
	// component eventually holds the disclosure key; see follow-up issue.
	dbPath := t.TempDir() + "/mismatch-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}

	// status=success + envelope present (legacy mismatch source) → still false.
	withEnvelope := makeReceipt("urn:receipt:m1", "chain-m", 1,
		"mcp.tool.call", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:00:00Z", nil)
	withEnvelope.CredentialSubject.Action.ParametersDisclosure = testDisclosureEnvelope(
		"did:key:test#enc-1", "opaque-ciphertext-m1")

	// status=success, no disclosure → false (control).
	bare := makeReceipt("urn:receipt:m2", "chain-m", 2,
		"mcp.tool.call", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:01:00Z", nil)

	for _, r := range []receipt.AgentReceipt{withEnvelope, bare} {
		hash, err := receipt.HashReceipt(r)
		if err != nil {
			t.Fatalf("hash: %v", err)
		}
		if err := s.Insert(r, hash); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	s.Close()

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer reader.Close()

	rows, err := reader.ListReceipts(Filter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, row := range rows {
		if row.OutputStatusMismatch {
			t.Errorf("%s: OutputStatusMismatch=true, want false under envelope-shape disclosure", row.ID)
		}
	}
}

// TestReader_ListReceipts_OutputStatusMismatch_LegacyShape pins that the
// SQL mismatch detector still flags pre-v0.3.0 receipts that physically
// carry the flat-map disclosure shape on disk. The sdkstore Insert path
// will not produce these — the typed Go API requires
// *receipt.DisclosureEnvelope — so we INSERT a raw receipt_json blob
// directly to simulate a store seeded under an older SDK that has not
// been re-keyed. The SQL in reader.go is shape-agnostic by design: it
// looks for .parameters_disclosure.output, and a legacy row still has
// that key.
func TestReader_ListReceipts_OutputStatusMismatch_LegacyShape(t *testing.T) {
	dbPath := t.TempDir() + "/mismatch-legacy.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}
	s.Close()

	// Open a raw sqlite handle (read-write) and insert a v0.2.x-shaped
	// receipt whose parameters_disclosure.output is a JSON-encoded string
	// containing isError:true. Status is "success" — the exact mismatch
	// the SQL is meant to flag.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	// Failure-path safety net: if any t.Fatalf below short-circuits the
	// explicit Close, t.Cleanup still releases the handle. database/sql's
	// DB.Close is idempotent (sets a `closed` flag, subsequent calls return
	// nil), so this is a no-op on the happy path. The explicit Close before
	// OpenReadOnly is the load-bearing one — it surfaces errors and quiesces
	// the file before the reader opens it.
	t.Cleanup(func() { _ = db.Close() })

	const legacyReceiptJSON = `{
		"@context":["https://www.w3.org/ns/credentials/v2","https://agentreceipts.ai/context/v1"],
		"id":"urn:receipt:legacy-1",
		"type":["VerifiableCredential","AgentReceipt"],
		"version":"0.2.2",
		"issuer":{"id":"did:agent:test-agent"},
		"issuanceDate":"2026-03-01T09:00:00Z",
		"credentialSubject":{
			"principal":{"id":"did:user:test-user"},
			"action":{
				"id":"act_legacy-1",
				"type":"mcp.tool.call",
				"risk_level":"low",
				"timestamp":"2026-03-01T09:00:00Z",
				"parameters_disclosure":{
					"input":"{\"path\":\"/etc/hosts\"}",
					"output":"{\"isError\":true,\"content\":[{\"type\":\"text\",\"text\":\"denied\"}]}"
				}
			},
			"outcome":{"status":"success"},
			"chain":{"sequence":1,"previous_receipt_hash":null,"chain_id":"chain-legacy"}
		},
		"proof":{"type":"Ed25519Signature2020","proofValue":"u-legacy-1"}
	}`

	if _, err := db.Exec(
		`INSERT INTO receipts (id, chain_id, sequence, action_type, risk_level, status, timestamp, issuer_id, receipt_json, receipt_hash)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"urn:receipt:legacy-1", "chain-legacy", 1, "mcp.tool.call", "low", "success",
		"2026-03-01T09:00:00Z", "did:agent:test-agent", legacyReceiptJSON, "sha256:legacy",
	); err != nil {
		t.Fatalf("insert legacy receipt: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close raw sqlite: %v", err)
	}

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer reader.Close()

	rows, err := reader.ListReceipts(Filter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if !rows[0].OutputStatusMismatch {
		t.Errorf("legacy mismatch row got OutputStatusMismatch=false, want true (status=success + parameters_disclosure.output.isError=true)")
	}
	if !rows[0].HasParametersDisclosure {
		t.Errorf("legacy row got HasParametersDisclosure=false, want true")
	}
}

func TestReader_GetChain(t *testing.T) {
	dbPath := seedFileDB(t)
	r, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()

	receipts, err := r.GetChain("chain-1")
	if err != nil {
		t.Fatalf("get chain: %v", err)
	}
	if len(receipts) != 3 {
		t.Errorf("got %d receipts, want 3", len(receipts))
	}
	// Should be ordered by sequence.
	for i := 1; i < len(receipts); i++ {
		prev := receipts[i-1].Receipt.CredentialSubject.Chain.Sequence
		curr := receipts[i].Receipt.CredentialSubject.Chain.Sequence
		if curr <= prev {
			t.Errorf("receipts not ordered: seq %d after seq %d", curr, prev)
		}
	}
	// Each result must carry the verbatim wire bytes used for hash recompute.
	for i, cr := range receipts {
		if len(cr.Raw) == 0 {
			t.Errorf("receipt %d: missing raw JSON bytes", i)
		}
	}
}

func TestReader_GetChain_Empty(t *testing.T) {
	dbPath := seedFileDB(t)
	r, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()

	receipts, err := r.GetChain("nonexistent-chain")
	if err != nil {
		t.Fatalf("get chain: %v", err)
	}
	if len(receipts) != 0 {
		t.Errorf("got %d receipts, want 0", len(receipts))
	}
}

func TestReader_ListChains(t *testing.T) {
	dbPath := seedFileDB(t)
	r, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()

	chains, err := r.ListChains()
	if err != nil {
		t.Fatalf("list chains: %v", err)
	}
	if len(chains) != 2 {
		t.Errorf("got %d chains, want 2", len(chains))
	}
	// Verify chain summaries have expected fields.
	for _, c := range chains {
		if c.ChainID == "" {
			t.Error("empty chain ID")
		}
		if c.ReceiptCount == 0 {
			t.Error("zero receipt count")
		}
		if c.FirstTimestamp == "" || c.LastTimestamp == "" {
			t.Error("missing timestamps")
		}
	}
}

func TestReader_Stats(t *testing.T) {
	dbPath := seedFileDB(t)
	r, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()

	stats, err := r.Stats(nil, nil)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Total != 5 {
		t.Errorf("got total %d, want 5", stats.Total)
	}
	if stats.Chains != 2 {
		t.Errorf("got chains %d, want 2", stats.Chains)
	}
	if len(stats.ByRisk) == 0 {
		t.Error("empty by_risk")
	}
	if len(stats.ByStatus) == 0 {
		t.Error("empty by_status")
	}
	if len(stats.ByAction) == 0 {
		t.Error("empty by_action")
	}
	// LatestTimestamp must match the newest seeded receipt; the header
	// "Updated Nm ago" indicator depends on this value being accurate.
	const wantLatest = "2026-04-01T11:01:00Z"
	if stats.LatestTimestamp != wantLatest {
		t.Errorf("got latest %q, want %q", stats.LatestTimestamp, wantLatest)
	}
}

func TestReader_Stats_ByAction(t *testing.T) {
	dbPath := t.TempDir() + "/by-action-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}

	recs := []receipt.AgentReceipt{
		makeReceipt("urn:receipt:a1", "chain-a", 1, "filesystem.file.read", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:00:00Z", nil),
		makeReceipt("urn:receipt:a2", "chain-a", 2, "filesystem.file.read", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:01:00Z", nil),
		makeReceipt("urn:receipt:a3", "chain-a", 3, "system.command.execute", receipt.RiskHigh, receipt.StatusSuccess, "2026-04-01T10:02:00Z", nil),
	}
	for _, r := range recs {
		hash, err := receipt.HashReceipt(r)
		if err != nil {
			t.Fatalf("hash: %v", err)
		}
		if err := s.Insert(r, hash); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	s.Close()

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer reader.Close()

	stats, err := reader.Stats(nil, nil)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}

	counts := map[string]int{}
	for _, gc := range stats.ByAction {
		counts[gc.Label] = gc.Count
	}
	if counts["filesystem.file.read"] != 2 {
		t.Errorf("filesystem.file.read: got %d, want 2", counts["filesystem.file.read"])
	}
	if counts["system.command.execute"] != 1 {
		t.Errorf("system.command.execute: got %d, want 1", counts["system.command.execute"])
	}
}

func TestReader_Stats_EmptyStore(t *testing.T) {
	// MAX(timestamp) returns NULL on an empty table; the reader must return
	// the zero value rather than erroring, so the dashboard can render an
	// "(none)" latest-timestamp slot on a fresh install.
	dbPath := seedEmptyDB(t)
	r, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()

	stats, err := r.Stats(nil, nil)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Total != 0 {
		t.Errorf("got total %d, want 0", stats.Total)
	}
	if stats.Chains != 0 {
		t.Errorf("got chains %d, want 0", stats.Chains)
	}
	if stats.LatestTimestamp != "" {
		t.Errorf("got latest %q, want empty", stats.LatestTimestamp)
	}
}

// TestEmptyResultsNeverNil guards against the class of bug where a store
// function returns a nil Go slice on an empty result set, which json.Marshal
// encodes as JSON null rather than []. All list/stat functions that feed API
// responses must return non-nil slices so clients always receive [].
func TestEmptyResultsNeverNil(t *testing.T) {
	dbPath := seedEmptyDB(t)
	r, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()

	t.Run("ListReceipts", func(t *testing.T) {
		rows, err := r.ListReceipts(Filter{})
		if err != nil {
			t.Fatalf("ListReceipts: %v", err)
		}
		if rows == nil {
			t.Error("ListReceipts returned nil slice; want non-nil empty slice (would JSON-encode as null)")
		}
	})

	t.Run("GetChain", func(t *testing.T) {
		rows, err := r.GetChain("nonexistent")
		if err != nil {
			t.Fatalf("GetChain: %v", err)
		}
		if rows == nil {
			t.Error("GetChain returned nil slice; want non-nil empty slice (would JSON-encode as null)")
		}
	})

	t.Run("ListChains", func(t *testing.T) {
		chains, err := r.ListChains()
		if err != nil {
			t.Fatalf("ListChains: %v", err)
		}
		if chains == nil {
			t.Error("ListChains returned nil slice; want non-nil empty slice (would JSON-encode as null)")
		}
	})

	t.Run("TimeseriesStats", func(t *testing.T) {
		buckets, err := r.TimeseriesStats(time.Time{}, time.Now(), time.Hour)
		if err != nil {
			t.Fatalf("TimeseriesStats: %v", err)
		}
		if buckets == nil {
			t.Error("TimeseriesStats returned nil slice; want non-nil empty slice (would JSON-encode as null)")
		}
	})

	t.Run("ActionStats", func(t *testing.T) {
		stats, err := r.ActionStats(nil)
		if err != nil {
			t.Fatalf("ActionStats: %v", err)
		}
		if stats == nil {
			t.Error("ActionStats returned nil slice; want non-nil empty slice (would JSON-encode as null)")
		}
	})

	t.Run("ServerStats", func(t *testing.T) {
		stats, err := r.ServerStats(nil)
		if err != nil {
			t.Fatalf("ServerStats: %v", err)
		}
		if stats == nil {
			t.Error("ServerStats returned nil slice; want non-nil empty slice (would JSON-encode as null)")
		}
	})
}

func TestListReceiptsQ(t *testing.T) {
	dbPath := t.TempDir() + "/q-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}

	// Receipts differ in action_type so we can search by a unique substring of
	// their JSON, and in risk_level so we can combine Q with another filter.
	recs := []receipt.AgentReceipt{
		makeReceiptWithTool("urn:receipt:q1", "chain-q", 1, "filesystem.file.read", "read_file", "fs-server", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:00:00Z"),
		makeReceiptWithTool("urn:receipt:q2", "chain-q", 2, "network.http.request", "http_get", "net-server", receipt.RiskHigh, receipt.StatusSuccess, "2026-04-01T10:01:00Z"),
		makeReceiptWithTool("urn:receipt:q3", "chain-q", 3, "communication.email.send", "send_mail", "mail-server", receipt.RiskHigh, receipt.StatusSuccess, "2026-04-01T10:02:00Z"),
	}
	for _, r := range recs {
		hash, err := receipt.HashReceipt(r)
		if err != nil {
			t.Fatalf("hash: %v", err)
		}
		if err := s.Insert(r, hash); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	s.Close()

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer reader.Close()

	// A term matching a single receipt returns only that receipt.
	rows, err := reader.ListReceipts(Filter{Q: strPtr("http_get")})
	if err != nil {
		t.Fatalf("list q=http_get: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("q=http_get: got %d rows, want 1", len(rows))
	}
	if rows[0].ID != "urn:receipt:q2" {
		t.Errorf("q=http_get: got ID %q, want urn:receipt:q2", rows[0].ID)
	}

	// A pointer to empty string returns all rows (same as nil).
	rowsAll, err := reader.ListReceipts(Filter{Q: strPtr("")})
	if err != nil {
		t.Fatalf("list q='': %v", err)
	}
	if len(rowsAll) != 3 {
		t.Errorf("q='': got %d rows, want 3 (same as nil)", len(rowsAll))
	}

	// nil Q also returns all rows.
	rowsNil, err := reader.ListReceipts(Filter{Q: nil})
	if err != nil {
		t.Fatalf("list q=nil: %v", err)
	}
	if len(rowsNil) != 3 {
		t.Errorf("q=nil: got %d rows, want 3", len(rowsNil))
	}

	// Whitespace-only Q returns all rows (trimmed before matching).
	rowsWS, err := reader.ListReceipts(Filter{Q: strPtr("   ")})
	if err != nil {
		t.Fatalf("list q=whitespace: %v", err)
	}
	if len(rowsWS) != 3 {
		t.Errorf("q=whitespace: got %d rows, want 3", len(rowsWS))
	}

	// Q combined with another filter narrows to the intersection.
	rows, err = reader.ListReceipts(Filter{Q: strPtr("mail"), RiskLevel: strPtr("high")})
	if err != nil {
		t.Fatalf("list q=mail + risk=high: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("q=mail+risk=high: got %d rows, want 1", len(rows))
	}
	if rows[0].ID != "urn:receipt:q3" {
		t.Errorf("q=mail+risk=high: got ID %q, want urn:receipt:q3", rows[0].ID)
	}

	// A term with a LIKE metacharacter (%) must be literal, not a wildcard.
	// "http%" is chosen so the test actually fails if escaping breaks: as a
	// wildcard it would match the "http_get" tool (JSON contains "http"), but
	// as a literal it matches nothing — no receipt JSON contains "http%".
	rowsMeta, err := reader.ListReceipts(Filter{Q: strPtr("http%")})
	if err != nil {
		t.Fatalf("list q=http%%: %v", err)
	}
	if len(rowsMeta) != 0 {
		t.Errorf("q='http%%': got %d rows, want 0 (percent must not act as wildcard, else it would match http_get)", len(rowsMeta))
	}

	// Similarly, underscore (_) in the term must be literal.
	rowsUnderscore, err := reader.ListReceipts(Filter{Q: strPtr("q_")})
	if err != nil {
		t.Fatalf("list q=q_: %v", err)
	}
	// "q_" with a literal underscore would not match any action_type or tool_name
	// in the seeded data (the chain_id is "chain-q", not "chain_q").
	if len(rowsUnderscore) != 0 {
		t.Errorf("q='q_': got %d rows, want 0 (underscore must not act as wildcard)", len(rowsUnderscore))
	}

	// Search is case-insensitive (SQLite LIKE folds ASCII case): an uppercase
	// term matches lowercase JSON content.
	rowsCase, err := reader.ListReceipts(Filter{Q: strPtr("EMAIL")})
	if err != nil {
		t.Fatalf("list q=EMAIL: %v", err)
	}
	if len(rowsCase) != 1 {
		t.Fatalf("q=EMAIL: got %d rows, want 1 (case-insensitive match of communication.email.send)", len(rowsCase))
	}
	if rowsCase[0].ID != "urn:receipt:q3" {
		t.Errorf("q=EMAIL: got ID %q, want urn:receipt:q3", rowsCase[0].ID)
	}
}

// seedEmptyDB creates a temporary SQLite file with the receipts schema in
// place but no rows — used to exercise empty-store paths like NULL MAX().
func seedEmptyDB(t *testing.T) string {
	t.Helper()
	dbPath := t.TempDir() + "/empty-receipts.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}
	s.Close()
	return dbPath
}

// ---------- SessionAttribution tests ----------

// makeAttrReceipt builds a receipt with session, agent, and resource fields set.
func makeAttrReceipt(id, chainID string, seq int, actionType string,
	risk receipt.RiskLevel, status receipt.OutcomeStatus, ts,
	sessionID, agentID, agentType, resource string) receipt.AgentReceipt {
	ar := makeReceipt(id, chainID, seq, actionType, risk, status, ts, nil)
	ar.Issuer.SessionID = sessionID
	if agentID != "" || agentType != "" {
		ar.Issuer.Runtime = &receipt.Runtime{AgentID: agentID, AgentType: agentType}
	}
	if resource != "" {
		ar.CredentialSubject.Action.Target = &receipt.ActionTarget{Resource: resource}
	}
	return ar
}

func insertAttr(t *testing.T, s *sdkstore.Store, r receipt.AgentReceipt) {
	t.Helper()
	h, err := receipt.HashReceipt(r)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := s.Insert(r, h); err != nil {
		t.Fatalf("insert: %v", err)
	}
}

// TestSessionAttribution_Empty checks that a session ID with no receipts returns
// an empty result without error.
func TestSessionAttribution_Empty(t *testing.T) {
	dbPath := seedEmptyDB(t)
	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer reader.Close()

	res, err := reader.SessionAttribution("session-nonexistent")
	if err != nil {
		t.Fatalf("SessionAttribution: %v", err)
	}
	if res.Coverage.TotalReceipts != 0 {
		t.Errorf("TotalReceipts = %d, want 0", res.Coverage.TotalReceipts)
	}
	if len(res.Nodes) != 0 {
		t.Errorf("Nodes len = %d, want 0", len(res.Nodes))
	}
	if len(res.StateDeps) != 0 {
		t.Errorf("StateDeps len = %d, want 0", len(res.StateDeps))
	}
	if res.BlastRadius == nil {
		t.Error("BlastRadius should be non-nil map")
	}
}

// TestSessionAttribution_Coverage verifies that coverage fraction is computed
// correctly when only some receipts carry a target.resource path.
func TestSessionAttribution_Coverage(t *testing.T) {
	dbPath := t.TempDir() + "/attr-coverage-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}
	const sid = "session-cov"
	// 2 receipts with a resource, 1 without.
	insertAttr(t, s, makeAttrReceipt("urn:receipt:c1", "chain-c", 1, "filesystem.file.read",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:00:00Z", sid, "", "", "src/main.go"))
	insertAttr(t, s, makeAttrReceipt("urn:receipt:c2", "chain-c", 2, "filesystem.file.write",
		receipt.RiskMedium, receipt.StatusSuccess, "2026-04-01T10:01:00Z", sid, "", "", "src/util.go"))
	insertAttr(t, s, makeAttrReceipt("urn:receipt:c3", "chain-c", 3, "system.bash.execute",
		receipt.RiskHigh, receipt.StatusSuccess, "2026-04-01T10:02:00Z", sid, "", "", ""))
	s.Close()

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer reader.Close()

	res, err := reader.SessionAttribution(sid)
	if err != nil {
		t.Fatalf("SessionAttribution: %v", err)
	}
	if res.Coverage.TotalReceipts != 3 {
		t.Errorf("TotalReceipts = %d, want 3", res.Coverage.TotalReceipts)
	}
	if res.Coverage.IdentityReceipts != 2 {
		t.Errorf("IdentityReceipts = %d, want 2", res.Coverage.IdentityReceipts)
	}
	wantFrac := 2.0 / 3.0
	if diff := res.Coverage.Fraction - wantFrac; diff < -1e-9 || diff > 1e-9 {
		t.Errorf("Fraction = %f, want %f", res.Coverage.Fraction, wantFrac)
	}
	// One node: the root agent (no agent_id set).
	if len(res.Nodes) != 1 || res.Nodes[0].AgentKey != "__root__" {
		t.Errorf("Nodes = %+v, want one root node", res.Nodes)
	}
	if res.Nodes[0].ReceiptCount != 3 {
		t.Errorf("root ReceiptCount = %d, want 3", res.Nodes[0].ReceiptCount)
	}
	if res.Nodes[0].IdentityCount != 2 {
		t.Errorf("root IdentityCount = %d, want 2", res.Nodes[0].IdentityCount)
	}
	if res.Nodes[0].MaxRisk != "high" {
		t.Errorf("root MaxRisk = %q, want high", res.Nodes[0].MaxRisk)
	}
}

// TestSessionAttribution_CrossAgentStateDep verifies that cross-agent state dep
// edges are produced when two agents touch the same resource.
func TestSessionAttribution_CrossAgentStateDep(t *testing.T) {
	dbPath := t.TempDir() + "/attr-statedep-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}
	const sid = "session-statedep"
	const subAgent = "subagent-xyz"
	// Orchestrator (root) reads index.html first.
	insertAttr(t, s, makeAttrReceipt("urn:receipt:sd1", "chain-root", 1, "filesystem.file.read",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:00:00Z", sid, "", "", "index.html"))
	// Subagent writes index.html later → cross-agent state dep.
	insertAttr(t, s, makeAttrReceipt("urn:receipt:sd2", "chain-sub", 1, "filesystem.file.write",
		receipt.RiskMedium, receipt.StatusSuccess, "2026-04-01T10:01:00Z", sid, subAgent, "general-purpose", "index.html"))
	// Subagent also reads util.go (no root touch → no cross-agent dep for this file).
	insertAttr(t, s, makeAttrReceipt("urn:receipt:sd3", "chain-sub", 2, "filesystem.file.read",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:02:00Z", sid, subAgent, "general-purpose", "util.go"))
	s.Close()

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer reader.Close()

	res, err := reader.SessionAttribution(sid)
	if err != nil {
		t.Fatalf("SessionAttribution: %v", err)
	}

	// Exactly one cross-agent state dep: root → subagent via index.html.
	if len(res.StateDeps) != 1 {
		t.Fatalf("StateDeps len = %d, want 1: %+v", len(res.StateDeps), res.StateDeps)
	}
	sd := res.StateDeps[0]
	if sd.FromAgent != "__root__" {
		t.Errorf("FromAgent = %q, want __root__", sd.FromAgent)
	}
	if sd.ToAgent != subAgent {
		t.Errorf("ToAgent = %q, want %s", sd.ToAgent, subAgent)
	}
	if !sd.CrossAgent {
		t.Error("CrossAgent = false, want true")
	}
	if len(sd.Resources) != 1 || sd.Resources[0] != "index.html" {
		t.Errorf("Resources = %v, want [index.html]", sd.Resources)
	}

	// Blast radius: root touched index.html, subagent touched index.html + util.go.
	rootRes := res.BlastRadius["__root__"]
	if len(rootRes) != 1 || rootRes[0] != "index.html" {
		t.Errorf("BlastRadius[root] = %v, want [index.html]", rootRes)
	}
	subRes := res.BlastRadius[subAgent]
	if len(subRes) != 2 {
		t.Errorf("BlastRadius[sub] = %v, want [index.html util.go]", subRes)
	}

	// Two nodes.
	if len(res.Nodes) != 2 {
		t.Fatalf("Nodes len = %d, want 2", len(res.Nodes))
	}
}

// TestSessionAttribution_MoveOp checks that has_move_ops is set when the session
// contains a move or rename action type.
func TestSessionAttribution_MoveOp(t *testing.T) {
	dbPath := t.TempDir() + "/attr-moveop-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}
	const sid = "session-moveop"
	insertAttr(t, s, makeAttrReceipt("urn:receipt:mv1", "chain-mv", 1, "filesystem.file.move",
		receipt.RiskHigh, receipt.StatusSuccess, "2026-04-01T10:00:00Z", sid, "", "", "old/path.go"))
	insertAttr(t, s, makeAttrReceipt("urn:receipt:mv2", "chain-mv", 2, "filesystem.file.read",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:01:00Z", sid, "", "", "new/path.go"))
	s.Close()

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer reader.Close()

	res, err := reader.SessionAttribution(sid)
	if err != nil {
		t.Fatalf("SessionAttribution: %v", err)
	}
	if !res.HasMoveOps {
		t.Error("HasMoveOps = false, want true for session with filesystem.file.move")
	}
}

// TestSessionAttribution_NoMoveOp checks that has_move_ops is false when there
// are no move or rename operations.
func TestSessionAttribution_NoMoveOp(t *testing.T) {
	dbPath := t.TempDir() + "/attr-nomoveop-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}
	const sid = "session-nomoveop"
	insertAttr(t, s, makeAttrReceipt("urn:receipt:nm1", "chain-nm", 1, "filesystem.file.read",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:00:00Z", sid, "", "", "file.go"))
	s.Close()

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer reader.Close()

	res, err := reader.SessionAttribution(sid)
	if err != nil {
		t.Fatalf("SessionAttribution: %v", err)
	}
	if res.HasMoveOps {
		t.Error("HasMoveOps = true, want false for session with only read operations")
	}
}

// TestSessionAttribution_RemoveNotMove checks that filesystem.file.remove does not
// set has_move_ops (the old substring-match would falsely match "remove" ⊃ "move").
func TestSessionAttribution_RemoveNotMove(t *testing.T) {
	dbPath := t.TempDir() + "/attr-remove-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}
	const sid = "session-remove"
	insertAttr(t, s, makeAttrReceipt("urn:receipt:rm1", "chain-rm", 1, "filesystem.file.remove",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:00:00Z", sid, "", "", "old.go"))
	s.Close()

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer reader.Close()

	res, err := reader.SessionAttribution(sid)
	if err != nil {
		t.Fatalf("SessionAttribution: %v", err)
	}
	if res.HasMoveOps {
		t.Error("HasMoveOps = true, want false for session with filesystem.file.remove (not a move)")
	}
}

// TestSessionAttribution_BidirectionalEdgeCollapse verifies that alternating
// writes by two agents on the same resource produce a single state-dep edge,
// not contradictory A→B and B→A edges, and that from_agent reflects the
// temporally-first agent regardless of alphabetical ID order.
func TestSessionAttribution_BidirectionalEdgeCollapse(t *testing.T) {
	dbPath := t.TempDir() + "/attr-bidir-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}
	const sid = "session-bidir"
	const agentA = "agent-alpha"
	const agentB = "agent-beta"
	// A writes, B writes, A writes — three consecutive touches alternating agents.
	// agentA ("agent-alpha") acts first across all resources for this pair;
	// the timestamp election picks it correctly regardless of map iteration order.
	insertAttr(t, s, makeAttrReceipt("urn:receipt:bd1", "chain-a", 1, "filesystem.file.write",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:00:00Z", sid, agentA, "worker", "shared.go"))
	insertAttr(t, s, makeAttrReceipt("urn:receipt:bd2", "chain-b", 1, "filesystem.file.write",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:01:00Z", sid, agentB, "worker", "shared.go"))
	insertAttr(t, s, makeAttrReceipt("urn:receipt:bd3", "chain-a", 2, "filesystem.file.write",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:02:00Z", sid, agentA, "worker", "shared.go"))
	s.Close()

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer reader.Close()

	res, err := reader.SessionAttribution(sid)
	if err != nil {
		t.Fatalf("SessionAttribution: %v", err)
	}
	if got := len(res.StateDeps); got != 1 {
		t.Fatalf("len(StateDeps) = %d, want 1 (bidirectional edges must collapse to one)", got)
	}
	sd := res.StateDeps[0]
	if sd.FromAgent != agentA {
		t.Errorf("FromAgent = %q, want %q (temporally first)", sd.FromAgent, agentA)
	}
	if sd.ToAgent != agentB {
		t.Errorf("ToAgent = %q, want %q", sd.ToAgent, agentB)
	}
}

// TestSessionAttribution_TemporalDirectionOverridesAlpha verifies that from_agent
// is the agent that acted first in time, even when its ID sorts after the other
// agent's ID alphabetically.
func TestSessionAttribution_TemporalDirectionOverridesAlpha(t *testing.T) {
	dbPath := t.TempDir() + "/attr-temporal-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}
	const sid = "session-temporal"
	// "zzz-agent" sorts after "aaa-agent" alphabetically, but acts first.
	const firstAgent = "zzz-agent"
	const secondAgent = "aaa-agent"
	insertAttr(t, s, makeAttrReceipt("urn:receipt:td1", "chain-z", 1, "filesystem.file.write",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:00:00Z", sid, firstAgent, "worker", "main.go"))
	insertAttr(t, s, makeAttrReceipt("urn:receipt:td2", "chain-a", 1, "filesystem.file.write",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:01:00Z", sid, secondAgent, "worker", "main.go"))
	s.Close()

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer reader.Close()

	res, err := reader.SessionAttribution(sid)
	if err != nil {
		t.Fatalf("SessionAttribution: %v", err)
	}
	if got := len(res.StateDeps); got != 1 {
		t.Fatalf("len(StateDeps) = %d, want 1", got)
	}
	sd := res.StateDeps[0]
	if sd.FromAgent != firstAgent {
		t.Errorf("FromAgent = %q, want %q (temporally first, alphabetically last)", sd.FromAgent, firstAgent)
	}
	if sd.ToAgent != secondAgent {
		t.Errorf("ToAgent = %q, want %q", sd.ToAgent, secondAgent)
	}
}

// TestSessionAttribution_MultiResource verifies that a state dep edge aggregates
// all shared resources between two agents into a single edge entry.
func TestSessionAttribution_MultiResource(t *testing.T) {
	dbPath := t.TempDir() + "/attr-multiresource-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}
	const sid = "session-multi"
	const subAgent = "subagent-abc"
	// Root touches two files, subagent later touches both.
	for i, res := range []string{"a.go", "b.go"} {
		insertAttr(t, s, makeAttrReceipt(
			fmt.Sprintf("urn:receipt:mr-root-%d", i), "chain-root", i+1, "filesystem.file.read",
			receipt.RiskLow, receipt.StatusSuccess, fmt.Sprintf("2026-04-01T10:%02d:00Z", i),
			sid, "", "", res))
	}
	for i, res := range []string{"a.go", "b.go"} {
		insertAttr(t, s, makeAttrReceipt(
			fmt.Sprintf("urn:receipt:mr-sub-%d", i), "chain-sub", i+1, "filesystem.file.write",
			receipt.RiskMedium, receipt.StatusSuccess, fmt.Sprintf("2026-04-01T10:%02d:00Z", i+2),
			sid, subAgent, "Explore", res))
	}
	s.Close()

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer reader.Close()

	res, err := reader.SessionAttribution(sid)
	if err != nil {
		t.Fatalf("SessionAttribution: %v", err)
	}
	// One edge aggregating both files.
	if len(res.StateDeps) != 1 {
		t.Fatalf("StateDeps len = %d, want 1 (two resources merged into one edge): %+v", len(res.StateDeps), res.StateDeps)
	}
	if len(res.StateDeps[0].Resources) != 2 {
		t.Errorf("Resources len = %d, want 2", len(res.StateDeps[0].Resources))
	}
}

// TestSessionAttribution_IsolatesSessions verifies that receipts from a different
// session_id are not included in the attribution result.
func TestSessionAttribution_IsolatesSessions(t *testing.T) {
	dbPath := t.TempDir() + "/attr-isolate-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}
	insertAttr(t, s, makeAttrReceipt("urn:receipt:iso1", "chain-a", 1, "filesystem.file.read",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:00:00Z", "session-A", "", "", "file.go"))
	insertAttr(t, s, makeAttrReceipt("urn:receipt:iso2", "chain-b", 1, "filesystem.file.read",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:01:00Z", "session-B", "", "", "file.go"))
	s.Close()

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer reader.Close()

	res, err := reader.SessionAttribution("session-A")
	if err != nil {
		t.Fatalf("SessionAttribution: %v", err)
	}
	if res.Coverage.TotalReceipts != 1 {
		t.Errorf("TotalReceipts = %d, want 1 (only session-A)", res.Coverage.TotalReceipts)
	}
	// session-B's receipt should not create a state dep.
	if len(res.StateDeps) != 0 {
		t.Errorf("StateDeps len = %d, want 0 (cross-session file touches are not deps)", len(res.StateDeps))
	}
}

func TestActionStats(t *testing.T) {
	// Seed: three action types —
	//   "cmd.exec":    6 receipts (4 failure, 2 success) → 66.7% failure rate
	//   "file.read":   5 receipts (0 failure, 5 success) → 0% failure rate, all-success
	//   "tiny.action": 3 receipts (3 failure)            → excluded (< 5)
	dbPath := t.TempDir() + "/action-stats-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}

	var recs []receipt.AgentReceipt

	// "cmd.exec": 4 failures + 2 successes (6 total)
	for i := range 4 {
		ts := fmt.Sprintf("2026-05-01T10:%02d:00Z", i)
		recs = append(recs, makeReceipt(
			fmt.Sprintf("urn:receipt:cmd-fail-%d", i), "chain-cmd", i+1,
			"cmd.exec", receipt.RiskHigh, receipt.StatusFailure, ts, nil,
		))
	}
	for i := range 2 {
		ts := fmt.Sprintf("2026-05-01T11:%02d:00Z", i)
		recs = append(recs, makeReceipt(
			fmt.Sprintf("urn:receipt:cmd-ok-%d", i), "chain-cmd", i+5,
			"cmd.exec", receipt.RiskHigh, receipt.StatusSuccess, ts, nil,
		))
	}

	// "file.read": 5 successes (0 failures)
	for i := range 5 {
		ts := fmt.Sprintf("2026-05-01T12:%02d:00Z", i)
		recs = append(recs, makeReceipt(
			fmt.Sprintf("urn:receipt:file-%d", i), "chain-file", i+1,
			"file.read", receipt.RiskLow, receipt.StatusSuccess, ts, nil,
		))
	}

	// "tiny.action": 3 receipts — must be EXCLUDED by HAVING COUNT(*) >= 5
	for i := range 3 {
		ts := fmt.Sprintf("2026-05-01T13:%02d:00Z", i)
		recs = append(recs, makeReceipt(
			fmt.Sprintf("urn:receipt:tiny-%d", i), "chain-tiny", i+1,
			"tiny.action", receipt.RiskLow, receipt.StatusFailure, ts, nil,
		))
	}

	for _, r := range recs {
		hash, err := receipt.HashReceipt(r)
		if err != nil {
			t.Fatalf("hash: %v", err)
		}
		if err := s.Insert(r, hash); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	s.Close()

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer reader.Close()

	// All-time query.
	stats, err := reader.ActionStats(nil)
	if err != nil {
		t.Fatalf("ActionStats: %v", err)
	}

	// Only "cmd.exec" and "file.read" must be present (tiny.action excluded).
	if len(stats) != 2 {
		t.Fatalf("got %d entries, want 2 (tiny.action must be excluded)", len(stats))
	}

	// Verify exclusion of tiny.action.
	for _, s := range stats {
		if s.ActionType == "tiny.action" {
			t.Errorf("tiny.action (total=3) should be excluded by HAVING COUNT(*) >= 5")
		}
	}

	// First entry must be cmd.exec (highest failure rate).
	first := stats[0]
	if first.ActionType != "cmd.exec" {
		t.Errorf("first action: got %q, want cmd.exec", first.ActionType)
	}
	if first.Total != 6 {
		t.Errorf("cmd.exec total: got %d, want 6", first.Total)
	}
	if first.Failure != 4 {
		t.Errorf("cmd.exec failure: got %d, want 4", first.Failure)
	}
	if first.Success != 2 {
		t.Errorf("cmd.exec success: got %d, want 2", first.Success)
	}
	wantRate := 4.0 / 6.0 // failure_rate is a 0–1 ratio
	if first.FailureRate < wantRate-0.0001 || first.FailureRate > wantRate+0.0001 {
		t.Errorf("cmd.exec failure_rate: got %.4f, want %.4f", first.FailureRate, wantRate)
	}

	// Second entry must be file.read (0% failure rate).
	second := stats[1]
	if second.ActionType != "file.read" {
		t.Errorf("second action: got %q, want file.read", second.ActionType)
	}
	if second.Total != 5 {
		t.Errorf("file.read total: got %d, want 5", second.Total)
	}
	if second.Failure != 0 {
		t.Errorf("file.read failure: got %d, want 0", second.Failure)
	}
	if second.FailureRate != 0 {
		t.Errorf("file.read failure_rate: got %f, want 0", second.FailureRate)
	}

	// Test since filter: only include receipts from 2026-05-01T11:00:00Z onward.
	// cmd.exec: 2 successes in that window (the 4 failures are before 11:00).
	// file.read: 5 receipts (12:xx) — still included, still 5 total.
	// => cmd.exec total < 5 after the window cut, so it should be excluded.
	since := "2026-05-01T11:00:00Z"
	sinceStats, err := reader.ActionStats(&since)
	if err != nil {
		t.Fatalf("ActionStats with since: %v", err)
	}
	// Only file.read survives (cmd.exec has only 2 receipts in the window).
	if len(sinceStats) != 1 {
		t.Fatalf("since filter: got %d entries, want 1", len(sinceStats))
	}
	if sinceStats[0].ActionType != "file.read" {
		t.Errorf("since filter first: got %q, want file.read", sinceStats[0].ActionType)
	}
}

func TestReader_IsReadOnly(t *testing.T) {
	dbPath := seedFileDB(t)
	r, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()

	// Attempting to write should fail.
	err = r.execWrite("INSERT INTO receipts (id, chain_id, sequence, action_type, risk_level, status, timestamp, issuer_id, receipt_json, receipt_hash) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		"urn:receipt:evil", "chain-x", 1, "test", "low", "success", "2026-01-01T00:00:00Z", "did:agent:x", "{}", "sha256:x")
	if err == nil {
		t.Fatal("expected write to fail on read-only connection")
	}
}

// TestTimeseriesStats tests the TimeseriesStats method.
func TestTimeseriesStats(t *testing.T) {
	// Seed receipts across three hours with mixed status/risk.
	// Hour 1 (10:00–10:59): 2 success/low, 1 failure/high
	// Hour 2 (11:00–11:59): 1 success/medium
	// Hour 3 (12:00–12:59): 0 receipts (empty bucket)
	// from=10:00, to=13:00 (exclusive), 1h buckets → 3 buckets: 10:00, 11:00, 12:00.
	dbPath := t.TempDir() + "/ts-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}

	recs := []receipt.AgentReceipt{
		makeReceipt("urn:receipt:ts1", "chain-ts", 1, "filesystem.file.read", receipt.RiskLow, receipt.StatusSuccess, "2026-05-01T10:05:00Z", nil),
		makeReceipt("urn:receipt:ts2", "chain-ts", 2, "filesystem.file.modify", receipt.RiskLow, receipt.StatusSuccess, "2026-05-01T10:30:00Z", nil),
		makeReceipt("urn:receipt:ts3", "chain-ts", 3, "communication.email.send", receipt.RiskHigh, receipt.StatusFailure, "2026-05-01T10:50:00Z", nil),
		makeReceipt("urn:receipt:ts4", "chain-ts", 4, "filesystem.file.read", receipt.RiskMedium, receipt.StatusSuccess, "2026-05-01T11:15:00Z", nil),
	}
	for _, r := range recs {
		hash, err := receipt.HashReceipt(r)
		if err != nil {
			t.Fatalf("hash: %v", err)
		}
		if err := s.Insert(r, hash); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	s.Close()

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer reader.Close()

	from := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	to := time.Date(2026, 5, 1, 13, 0, 0, 0, time.UTC)
	bucket := time.Hour

	buckets, err := reader.TimeseriesStats(from, to, bucket)
	if err != nil {
		t.Fatalf("TimeseriesStats: %v", err)
	}

	// Expect 3 buckets: 10:00, 11:00, 12:00 (to=13:00 is exclusive).
	if len(buckets) != 3 {
		t.Fatalf("got %d buckets, want 3; buckets: %+v", len(buckets), buckets)
	}

	// Bucket 0: 10:00 — 3 receipts (2 success/low, 1 failure/high).
	b0 := buckets[0]
	if b0.Ts != "2026-05-01T10:00:00Z" {
		t.Errorf("bucket[0].Ts = %q, want 2026-05-01T10:00:00Z", b0.Ts)
	}
	if b0.Total != 3 {
		t.Errorf("bucket[0].Total = %d, want 3", b0.Total)
	}
	if b0.ByStatus["success"] != 2 {
		t.Errorf("bucket[0].ByStatus[success] = %d, want 2", b0.ByStatus["success"])
	}
	if b0.ByStatus["failure"] != 1 {
		t.Errorf("bucket[0].ByStatus[failure] = %d, want 1", b0.ByStatus["failure"])
	}
	if b0.ByRisk["low"] != 2 {
		t.Errorf("bucket[0].ByRisk[low] = %d, want 2", b0.ByRisk["low"])
	}
	if b0.ByRisk["high"] != 1 {
		t.Errorf("bucket[0].ByRisk[high] = %d, want 1", b0.ByRisk["high"])
	}

	// Bucket 1: 11:00 — 1 receipt (1 success/medium).
	b1 := buckets[1]
	if b1.Ts != "2026-05-01T11:00:00Z" {
		t.Errorf("bucket[1].Ts = %q, want 2026-05-01T11:00:00Z", b1.Ts)
	}
	if b1.Total != 1 {
		t.Errorf("bucket[1].Total = %d, want 1", b1.Total)
	}
	if b1.ByStatus["success"] != 1 {
		t.Errorf("bucket[1].ByStatus[success] = %d, want 1", b1.ByStatus["success"])
	}
	if b1.ByRisk["medium"] != 1 {
		t.Errorf("bucket[1].ByRisk[medium] = %d, want 1", b1.ByRisk["medium"])
	}

	// Bucket 2: 12:00 — empty bucket.
	b2 := buckets[2]
	if b2.Ts != "2026-05-01T12:00:00Z" {
		t.Errorf("bucket[2].Ts = %q, want 2026-05-01T12:00:00Z", b2.Ts)
	}
	if b2.Total != 0 {
		t.Errorf("bucket[2].Total = %d, want 0 (empty bucket)", b2.Total)
	}
	if len(b2.ByStatus) != 0 {
		t.Errorf("bucket[2].ByStatus = %v, want empty", b2.ByStatus)
	}
	if len(b2.ByRisk) != 0 {
		t.Errorf("bucket[2].ByRisk = %v, want empty", b2.ByRisk)
	}

	t.Run("from-zero resolves to earliest receipt", func(t *testing.T) {
		// Pass zero Time — should start from the earliest receipt timestamp.
		toTs := time.Date(2026, 5, 1, 13, 0, 0, 0, time.UTC)
		bkts, err := reader.TimeseriesStats(time.Time{}, toTs, time.Hour)
		if err != nil {
			t.Fatalf("TimeseriesStats with zero from: %v", err)
		}
		// Earliest receipt is at 10:05 → floors to 10:00. So we should get 10:00, 11:00, 12:00.
		if len(bkts) < 1 {
			t.Fatalf("got %d buckets, want at least 1", len(bkts))
		}
		// First bucket must start at or before the first receipt.
		if bkts[0].Ts > "2026-05-01T10:05:00Z" {
			t.Errorf("first bucket Ts %q is after earliest receipt 10:05", bkts[0].Ts)
		}
		// Total across all buckets must equal 4.
		total := 0
		for _, b := range bkts {
			total += b.Total
		}
		if total != 4 {
			t.Errorf("all-time total across buckets = %d, want 4", total)
		}
	})

	t.Run("empty store returns empty slice", func(t *testing.T) {
		emptyPath := seedEmptyDB(t)
		emptyReader, err := OpenReadOnly(emptyPath)
		if err != nil {
			t.Fatalf("open empty reader: %v", err)
		}
		defer emptyReader.Close()

		bkts, err := emptyReader.TimeseriesStats(time.Time{}, time.Now(), time.Hour)
		if err != nil {
			t.Fatalf("TimeseriesStats on empty store: %v", err)
		}
		if len(bkts) != 0 {
			t.Errorf("empty store: got %d buckets, want 0", len(bkts))
		}
	})

	t.Run("too many buckets returns error", func(t *testing.T) {
		// 3 years at 1-minute intervals → far exceeds 2000 buckets.
		bigFrom := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
		bigTo := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		_, err := reader.TimeseriesStats(bigFrom, bigTo, time.Minute)
		if err == nil {
			t.Error("expected error for too many buckets, got nil")
		}
	})
}

// TestStatsWithRange tests the range-aware Stats method.
func TestStatsWithRange(t *testing.T) {
	// Seed: 3 receipts spread across two hours.
	// 10:00 low/success, 10:01 medium/success, 11:00 high/failure
	dbPath := t.TempDir() + "/stats-range-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}

	recs := []receipt.AgentReceipt{
		makeReceipt("urn:receipt:r1", "chain-r", 1, "filesystem.file.read", receipt.RiskLow, receipt.StatusSuccess, "2026-06-01T10:00:00Z", nil),
		makeReceipt("urn:receipt:r2", "chain-r", 2, "filesystem.file.modify", receipt.RiskMedium, receipt.StatusSuccess, "2026-06-01T10:01:00Z", nil),
		makeReceipt("urn:receipt:r3", "chain-r", 3, "communication.email.send", receipt.RiskHigh, receipt.StatusFailure, "2026-06-01T11:00:00Z", nil),
	}
	for _, r := range recs {
		hash, err := receipt.HashReceipt(r)
		if err != nil {
			t.Fatalf("hash: %v", err)
		}
		if err := s.Insert(r, hash); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	s.Close()

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer reader.Close()

	// nil/nil — all-time.
	allTime, err := reader.Stats(nil, nil)
	if err != nil {
		t.Fatalf("Stats nil/nil: %v", err)
	}
	if allTime.Total != 3 {
		t.Errorf("all-time total = %d, want 3", allTime.Total)
	}

	// after= cuts to the 11:00 receipt only.
	after := "2026-06-01T11:00:00Z"
	filtered, err := reader.Stats(&after, nil)
	if err != nil {
		t.Fatalf("Stats after: %v", err)
	}
	if filtered.Total != 1 {
		t.Errorf("filtered total = %d, want 1", filtered.Total)
	}
	if len(filtered.ByStatus) != 1 {
		t.Errorf("filtered by_status len = %d, want 1", len(filtered.ByStatus))
	}
	if filtered.ByStatus[0].Label != "failure" {
		t.Errorf("filtered by_status[0].Label = %q, want failure", filtered.ByStatus[0].Label)
	}
	// ByRisk should only contain high.
	riskMap := map[string]int{}
	for _, gc := range filtered.ByRisk {
		riskMap[gc.Label] = gc.Count
	}
	if riskMap["high"] != 1 {
		t.Errorf("filtered by_risk[high] = %d, want 1", riskMap["high"])
	}
	if riskMap["low"] != 0 {
		t.Errorf("filtered by_risk[low] = %d, want 0", riskMap["low"])
	}

	// before= cuts to the 10:00 and 10:01 receipts.
	before := "2026-06-01T10:30:00Z"
	beforeFiltered, err := reader.Stats(nil, &before)
	if err != nil {
		t.Fatalf("Stats before: %v", err)
	}
	if beforeFiltered.Total != 2 {
		t.Errorf("before-filtered total = %d, want 2", beforeFiltered.Total)
	}
}

// ---------- Layer 3 attribution tests ----------

// insertLayer3 creates a receipt with Layer 3 attribution fields by marshalling
// the base receipt to JSON, injecting the new fields, and inserting the
// augmented bytes via InsertRaw. This mirrors how daemon ≥ v0.17.0 emits
// receipts with fields not yet in the SDK Go types.
func insertLayer3(t *testing.T, s *sdkstore.Store, r receipt.AgentReceipt, correlationID, agentID string, delegation *DelegationInfo) {
	t.Helper()
	rawJSON, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal receipt: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(rawJSON, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Inject correlation_id and delegation into credentialSubject.
	cs, _ := m["credentialSubject"].(map[string]any)
	if cs == nil {
		cs = map[string]any{}
	}
	if correlationID != "" {
		cs["correlation_id"] = correlationID
	}
	if delegation != nil {
		cs["delegation"] = map[string]any{
			"parent_chain_id":   delegation.ParentChainID,
			"parent_receipt_id": delegation.ParentReceiptID,
			"delegator":         map[string]any{"id": delegation.DelegatorID},
		}
	}
	m["credentialSubject"] = cs

	// Inject agent_id into issuer.runtime (the open metadata sub-object, ADR-0026;
	// daemon ≥ v0.18.0). Older receipts had no agent_id at all. Merge into any
	// pre-existing runtime keys so this helper composes with insertRuntimeModel.
	if agentID != "" {
		issuer, _ := m["issuer"].(map[string]any)
		if issuer == nil {
			issuer = map[string]any{}
		}
		rt, _ := issuer["runtime"].(map[string]any)
		if rt == nil {
			rt = map[string]any{}
		}
		rt["agent_id"] = agentID
		rt["agent_type"] = "general-purpose"
		issuer["runtime"] = rt
		m["issuer"] = issuer
	}

	augmented, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal augmented: %v", err)
	}
	hash, err := receipt.HashRawReceipt(augmented)
	if err != nil {
		t.Fatalf("hash augmented receipt: %v", err)
	}
	if err := s.InsertRaw(r, augmented, hash); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
}

// TestReader_Layer3_GracefulDegradation verifies that old receipts (no Layer 3
// fields) are returned with empty CorrelationID, AgentID, SessionID, and nil
// Delegation — no errors and no zero-value noise.
func TestReader_Layer3_GracefulDegradation(t *testing.T) {
	dbPath := seedFileDB(t)
	r, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()

	rows, err := r.ListReceipts(Filter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, row := range rows {
		if row.CorrelationID != "" {
			t.Errorf("row %s: want empty CorrelationID for old receipt, got %q", row.ID, row.CorrelationID)
		}
		if row.AgentID != "" {
			t.Errorf("row %s: want empty AgentID for old receipt, got %q", row.ID, row.AgentID)
		}
		if row.SessionID != "" {
			t.Errorf("row %s: want empty SessionID for old receipt, got %q", row.ID, row.SessionID)
		}
		if row.Delegation != nil {
			t.Errorf("row %s: want nil Delegation for old receipt, got %+v", row.ID, row.Delegation)
		}
	}
}

// TestReader_Layer3_NewFields verifies that correlation_id, agent_id,
// issuer_model, session_id, and delegation are correctly extracted from
// receipts emitted by daemon ≥ v0.17.0 / hook ≥ v0.14.0.
func TestReader_Layer3_NewFields(t *testing.T) {
	dbPath := t.TempDir() + "/layer3-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}

	// Base receipt using the current Issuer struct with session_id and model set.
	base := makeReceipt("urn:receipt:l3a", "chain-l3", 1, "tool.call",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:00:00Z", nil)
	base.Issuer.SessionID = "session-abc"
	base.Issuer.Model = "claude-sonnet-4-6"

	del := &DelegationInfo{
		ParentChainID:   "urn:chain:parent",
		ParentReceiptID: "urn:receipt:parent-last",
		DelegatorID:     "did:agent:orchestrator",
	}
	insertLayer3(t, s, base, "corr-001", "subagent-x", del)

	// Receipt with only correlation_id (no delegation, no agent_id).
	r2 := makeReceipt("urn:receipt:l3b", "chain-l3", 2, "tool.call",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:01:00Z", nil)
	r2.Issuer.SessionID = "session-abc"
	insertLayer3(t, s, r2, "corr-001", "", nil)

	// Old-style receipt with no Layer 3 fields at all.
	r3 := makeReceipt("urn:receipt:l3c", "chain-l3-old", 1, "tool.call",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:02:00Z", nil)
	hash3, err := receipt.HashReceipt(r3)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := s.Insert(r3, hash3); err != nil {
		t.Fatalf("insert old receipt: %v", err)
	}

	s.Close()

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer reader.Close()

	rows, err := reader.ListReceipts(Filter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}

	// Rows are newest-first: l3c (index 0), l3b (index 1), l3a (index 2).
	rowC := rows[0] // old-style, no Layer 3
	rowB := rows[1] // correlation only
	rowA := rows[2] // full Layer 3

	// l3a: all Layer 3 fields set.
	if rowA.CorrelationID != "corr-001" {
		t.Errorf("l3a CorrelationID: got %q, want corr-001", rowA.CorrelationID)
	}
	if rowA.AgentID != "subagent-x" {
		t.Errorf("l3a AgentID: got %q, want subagent-x", rowA.AgentID)
	}
	if rowA.AgentType != "general-purpose" {
		t.Errorf("l3a AgentType: got %q, want general-purpose", rowA.AgentType)
	}
	if rowA.IssuerModel != "claude-sonnet-4-6" {
		t.Errorf("l3a IssuerModel: got %q, want claude-sonnet-4-6", rowA.IssuerModel)
	}
	if rowA.SessionID != "session-abc" {
		t.Errorf("l3a SessionID: got %q, want session-abc", rowA.SessionID)
	}
	if rowA.Delegation == nil {
		t.Fatal("l3a Delegation: got nil, want non-nil")
	}
	if rowA.Delegation.ParentChainID != "urn:chain:parent" {
		t.Errorf("l3a Delegation.ParentChainID: got %q, want urn:chain:parent", rowA.Delegation.ParentChainID)
	}
	if rowA.Delegation.ParentReceiptID != "urn:receipt:parent-last" {
		t.Errorf("l3a Delegation.ParentReceiptID: got %q, want urn:receipt:parent-last", rowA.Delegation.ParentReceiptID)
	}
	if rowA.Delegation.DelegatorID != "did:agent:orchestrator" {
		t.Errorf("l3a Delegation.DelegatorID: got %q, want did:agent:orchestrator", rowA.Delegation.DelegatorID)
	}

	// l3b: correlation_id + session_id, no agent_id or delegation.
	if rowB.CorrelationID != "corr-001" {
		t.Errorf("l3b CorrelationID: got %q, want corr-001", rowB.CorrelationID)
	}
	if rowB.AgentID != "" {
		t.Errorf("l3b AgentID: got %q, want empty", rowB.AgentID)
	}
	if rowB.IssuerModel != "" {
		t.Errorf("l3b IssuerModel: got %q, want empty", rowB.IssuerModel)
	}
	if rowB.SessionID != "session-abc" {
		t.Errorf("l3b SessionID: got %q, want session-abc", rowB.SessionID)
	}
	if rowB.Delegation != nil {
		t.Errorf("l3b Delegation: got %+v, want nil", rowB.Delegation)
	}

	// l3c: old-style receipt — all Layer 3 fields empty/nil.
	if rowC.CorrelationID != "" {
		t.Errorf("l3c CorrelationID: got %q, want empty", rowC.CorrelationID)
	}
	if rowC.AgentID != "" {
		t.Errorf("l3c AgentID: got %q, want empty", rowC.AgentID)
	}
	if rowC.SessionID != "" {
		t.Errorf("l3c SessionID: got %q, want empty", rowC.SessionID)
	}
	if rowC.Delegation != nil {
		t.Errorf("l3c Delegation: got %+v, want nil", rowC.Delegation)
	}
}

// insertRuntimeModel augments a receipt's issuer.runtime with transcript-derived
// model, capture_method, and (optionally) usage fields (obsigna PR #779) and
// inserts it via InsertRaw, mirroring how the daemon injects fields the SDK
// doesn't yet type. usage is only injected when at least one token count is
// non-zero, so callers can represent the "model present, usage absent" case by
// passing all zeros. Merges into any pre-existing runtime keys so this helper
// composes with insertLayer3 when testing receipts that carry both sets of fields.
func insertRuntimeModel(t *testing.T, s *sdkstore.Store, r receipt.AgentReceipt, model, captureMethod string, inputTokens, outputTokens, cacheReadTokens, cacheCreateTokens int) {
	t.Helper()
	rawJSON, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal receipt: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(rawJSON, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	issuer, _ := m["issuer"].(map[string]any)
	if issuer == nil {
		issuer = map[string]any{}
	}
	rt, _ := issuer["runtime"].(map[string]any)
	if rt == nil {
		rt = map[string]any{}
	}
	rt["model"] = model
	rt["capture_method"] = captureMethod
	if inputTokens != 0 || outputTokens != 0 || cacheReadTokens != 0 || cacheCreateTokens != 0 {
		rt["usage"] = map[string]any{
			"input_tokens":                inputTokens,
			"output_tokens":               outputTokens,
			"cache_read_input_tokens":     cacheReadTokens,
			"cache_creation_input_tokens": cacheCreateTokens,
		}
	}
	issuer["runtime"] = rt
	m["issuer"] = issuer
	augmented, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal augmented: %v", err)
	}
	hash, err := receipt.HashRawReceipt(augmented)
	if err != nil {
		t.Fatalf("hash augmented receipt: %v", err)
	}
	if err := s.InsertRaw(r, augmented, hash); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
}

// TestReader_RuntimeModelUsage verifies that issuer.runtime.model is extracted
// into ReceiptRow.RuntimeModel and that older receipts without this field return
// an empty string without error. capture_method and usage are not extracted into
// ReceiptRow; they reach the detail modal through the detail endpoint's raw JSON
// passthrough (runtime.Extra preserves them across the GetByID round-trip).
func TestReader_RuntimeModelUsage(t *testing.T) {
	dbPath := t.TempDir() + "/runtime-model-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}

	// Receipt with full transcript-derived enrichment (root agent — no agent_id).
	r1 := makeReceipt("urn:receipt:rm1", "chain-rm", 1, "tool.call",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:00:00Z", nil)
	insertRuntimeModel(t, s, r1, "claude-opus-4-8", "transcript", 1954, 392, 0, 16762)

	// Receipt with only model (no usage) — tests partial enrichment.
	r2 := makeReceipt("urn:receipt:rm2", "chain-rm", 2, "tool.call",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:01:00Z", nil)
	insertRuntimeModel(t, s, r2, "claude-haiku-4-5-20251001", "transcript", 0, 0, 0, 0)

	// Old-style receipt — no runtime enrichment at all.
	r3 := makeReceipt("urn:receipt:rm3", "chain-rm-old", 1, "tool.call",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:02:00Z", nil)
	hash3, err := receipt.HashReceipt(r3)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := s.Insert(r3, hash3); err != nil {
		t.Fatalf("insert old receipt: %v", err)
	}

	s.Close()

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer reader.Close()

	rows, err := reader.ListReceipts(Filter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}

	// Newest-first: rm3 (index 0), rm2 (index 1), rm1 (index 2).
	rowOld := rows[0]
	rowPartial := rows[1]
	rowFull := rows[2]

	// Full enrichment: RuntimeModel must be populated.
	if rowFull.RuntimeModel != "claude-opus-4-8" {
		t.Errorf("rm1 RuntimeModel: got %q, want claude-opus-4-8", rowFull.RuntimeModel)
	}

	// Partial enrichment: model present, zero token counts are fine.
	if rowPartial.RuntimeModel != "claude-haiku-4-5-20251001" {
		t.Errorf("rm2 RuntimeModel: got %q, want claude-haiku-4-5-20251001", rowPartial.RuntimeModel)
	}

	// Old receipt: RuntimeModel must be empty.
	if rowOld.RuntimeModel != "" {
		t.Errorf("rm3 RuntimeModel: got %q, want empty", rowOld.RuntimeModel)
	}
}

// TestReader_RuntimeModel_CombinedWithLayer3 verifies that a receipt carrying
// both Layer 3 attribution (agent_id) and transcript-derived enrichment (model)
// has all fields scanned correctly. The insertLayer3 and insertRuntimeModel
// helpers must merge into issuer.runtime rather than wholesale-replacing it.
func TestReader_RuntimeModel_CombinedWithLayer3(t *testing.T) {
	dbPath := t.TempDir() + "/combined-runtime-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}

	base := makeReceipt("urn:receipt:combo1", "chain-combo", 1, "tool.call",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:00:00Z", nil)
	// First inject agent_id, then model — both must survive.
	insertLayer3(t, s, base, "corr-xyz", "subagent-q", nil)

	// Re-augment the already-inserted receipt by reading it back, adding model,
	// and re-inserting via a fresh store open. Simpler: insert a second receipt
	// that has both fields set in one pass by calling insertRuntimeModel on a
	// receipt that already has runtime fields baked in via the SDK struct.
	base2 := makeReceipt("urn:receipt:combo2", "chain-combo", 2, "tool.call",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:01:00Z", nil)
	base2.Issuer.Runtime = &receipt.Runtime{AgentID: "subagent-q", AgentType: "general-purpose"}
	insertRuntimeModel(t, s, base2, "claude-sonnet-4-6", "transcript", 100, 50, 0, 0)

	s.Close()

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer reader.Close()

	rows, err := reader.ListReceipts(Filter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}

	// Newest first: combo2, combo1.
	rowCombined := rows[0]

	if rowCombined.AgentID != "subagent-q" {
		t.Errorf("combo2 AgentID: got %q, want subagent-q", rowCombined.AgentID)
	}
	if rowCombined.AgentType != "general-purpose" {
		t.Errorf("combo2 AgentType: got %q, want general-purpose", rowCombined.AgentType)
	}
	if rowCombined.RuntimeModel != "claude-sonnet-4-6" {
		t.Errorf("combo2 RuntimeModel: got %q, want claude-sonnet-4-6", rowCombined.RuntimeModel)
	}
}

// TestReader_ListReceipts_FilterBySessionID verifies that the session_id filter
// returns only receipts whose issuer.session_id matches the supplied value.
func TestReader_ListReceipts_FilterBySessionID(t *testing.T) {
	dbPath := t.TempDir() + "/session-filter-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}

	// Two receipts in session-alpha, one in session-beta, one with no session.
	r1 := makeReceipt("urn:receipt:sf1", "chain-sf", 1, "tool.call",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:00:00Z", nil)
	r1.Issuer.SessionID = "session-alpha"
	insertLayer3(t, s, r1, "", "", nil)

	r2 := makeReceipt("urn:receipt:sf2", "chain-sf", 2, "tool.call",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:01:00Z", nil)
	r2.Issuer.SessionID = "session-alpha"
	insertLayer3(t, s, r2, "", "", nil)

	r3 := makeReceipt("urn:receipt:sf3", "chain-sf", 3, "tool.call",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:02:00Z", nil)
	r3.Issuer.SessionID = "session-beta"
	insertLayer3(t, s, r3, "", "", nil)

	// Old-style receipt with no session_id at all.
	r4 := makeReceipt("urn:receipt:sf4", "chain-sf-old", 1, "tool.call",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:03:00Z", nil)
	hash4, err := receipt.HashReceipt(r4)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := s.Insert(r4, hash4); err != nil {
		t.Fatalf("insert old receipt: %v", err)
	}
	s.Close()

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer reader.Close()

	// Filter by session-alpha — should return 2 rows.
	rows, err := reader.ListReceipts(Filter{SessionID: strPtr("session-alpha")})
	if err != nil {
		t.Fatalf("list by session-alpha: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("got %d rows for session-alpha, want 2", len(rows))
	}
	for _, row := range rows {
		if row.SessionID != "session-alpha" {
			t.Errorf("row %s: session_id = %q, want session-alpha", row.ID, row.SessionID)
		}
	}

	// Filter by session-beta — should return 1 row.
	rows, err = reader.ListReceipts(Filter{SessionID: strPtr("session-beta")})
	if err != nil {
		t.Fatalf("list by session-beta: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("got %d rows for session-beta, want 1", len(rows))
	}
	if len(rows) == 1 && rows[0].ID != "urn:receipt:sf3" {
		t.Errorf("session-beta row ID = %q, want urn:receipt:sf3", rows[0].ID)
	}

	// Filter by a non-existent session — should return 0 rows.
	rows, err = reader.ListReceipts(Filter{SessionID: strPtr("session-nonexistent")})
	if err != nil {
		t.Fatalf("list by nonexistent session: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("got %d rows for nonexistent session, want 0", len(rows))
	}
}

// TestReader_Layer3_DelegationMalformed verifies that a malformed delegation
// JSON value does not error — it results in nil Delegation (graceful
// degradation for forward-compat payloads the dashboard can't fully parse).
func TestReader_Layer3_DelegationMalformed(t *testing.T) {
	dbPath := t.TempDir() + "/layer3-malformed.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}

	base := makeReceipt("urn:receipt:dm1", "chain-dm", 1, "tool.call",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:00:00Z", nil)

	rawJSON, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(rawJSON, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	cs, _ := m["credentialSubject"].(map[string]any)
	if cs == nil {
		cs = map[string]any{}
	}
	// Inject a delegation field with missing required keys — should parse to nil.
	cs["delegation"] = map[string]any{"unexpected_key": "value"}
	m["credentialSubject"] = cs

	augmented, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal augmented: %v", err)
	}
	hash, err := receipt.HashRawReceipt(augmented)
	if err != nil {
		t.Fatalf("hash augmented receipt: %v", err)
	}
	if err := s.InsertRaw(base, augmented, hash); err != nil {
		t.Fatalf("insert: %v", err)
	}
	s.Close()

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer reader.Close()

	rows, err := reader.ListReceipts(Filter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Delegation != nil {
		t.Errorf("want nil Delegation for empty-keys object, got %+v", rows[0].Delegation)
	}
}

// TestParseDelegationJSON unit-tests parseDelegationJSON directly, covering
// the boundary cases that the DB-roundtrip tests cannot easily express.
func TestParseDelegationJSON(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantNil bool
	}{
		{"both fields present", `{"parent_chain_id":"urn:chain:a","parent_receipt_id":"urn:receipt:b"}`, false},
		{"both fields with delegator", `{"parent_chain_id":"c","parent_receipt_id":"r","delegator":{"id":"did:agent:x"}}`, false},
		{"only parent_chain_id", `{"parent_chain_id":"urn:chain:a"}`, true},
		{"only parent_receipt_id", `{"parent_receipt_id":"urn:receipt:b"}`, true},
		{"neither field", `{"unexpected":"value"}`, true},
		{"empty object", `{}`, true},
		{"malformed JSON", `not-json`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseDelegationJSON(tc.input)
			if tc.wantNil && got != nil {
				t.Errorf("want nil, got %+v", got)
			}
			if !tc.wantNil && got == nil {
				t.Errorf("want non-nil DelegationInfo, got nil")
			}
		})
	}
}

// TestReader_SessionStats verifies that SessionStats groups receipts by session_id,
// computes correct receipt/agent counts, and excludes receipts without a session_id.
func TestReader_SessionStats(t *testing.T) {
	dbPath := t.TempDir() + "/sessions-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}

	// session-alpha: 3 receipts, 2 agents (orchestrator + subagent-x)
	r1 := makeReceipt("urn:receipt:ss1", "chain-ss", 1, "tool.call",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T09:00:00Z", nil)
	r1.Issuer.SessionID = "session-alpha"
	insertLayer3(t, s, r1, "", "orchestrator", nil)

	r2 := makeReceipt("urn:receipt:ss2", "chain-ss", 2, "tool.call",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:00:00Z", nil)
	r2.Issuer.SessionID = "session-alpha"
	insertLayer3(t, s, r2, "", "subagent-x", nil)

	r3 := makeReceipt("urn:receipt:ss3", "chain-ss", 3, "tool.call",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T11:00:00Z", nil)
	r3.Issuer.SessionID = "session-alpha"
	insertLayer3(t, s, r3, "", "orchestrator", nil)

	// session-beta: 1 receipt, 1 agent
	r4 := makeReceipt("urn:receipt:ss4", "chain-ss2", 1, "tool.call",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T08:00:00Z", nil)
	r4.Issuer.SessionID = "session-beta"
	insertLayer3(t, s, r4, "", "orchestrator", nil)

	// no session: should be excluded
	r5 := makeReceipt("urn:receipt:ss5", "chain-ss3", 1, "tool.call",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T12:00:00Z", nil)
	hash5, err := receipt.HashReceipt(r5)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := s.Insert(r5, hash5); err != nil {
		t.Fatalf("insert no-session receipt: %v", err)
	}
	s.Close()

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer reader.Close()

	rows, err := reader.SessionStats(nil)
	if err != nil {
		t.Fatalf("session stats: %v", err)
	}

	if len(rows) != 2 {
		t.Fatalf("got %d sessions, want 2", len(rows))
	}

	// Results are ordered by last_seen DESC, so session-alpha (last 11:00) comes first.
	alpha := rows[0]
	if alpha.SessionID != "session-alpha" {
		t.Errorf("rows[0].SessionID = %q, want session-alpha", alpha.SessionID)
	}
	if alpha.ReceiptCount != 3 {
		t.Errorf("session-alpha ReceiptCount = %d, want 3", alpha.ReceiptCount)
	}
	if alpha.AgentCount != 2 {
		t.Errorf("session-alpha AgentCount = %d, want 2", alpha.AgentCount)
	}
	if alpha.FirstSeen != "2026-04-01T09:00:00Z" {
		t.Errorf("session-alpha FirstSeen = %q, want 2026-04-01T09:00:00Z", alpha.FirstSeen)
	}
	if alpha.LastSeen != "2026-04-01T11:00:00Z" {
		t.Errorf("session-alpha LastSeen = %q, want 2026-04-01T11:00:00Z", alpha.LastSeen)
	}

	beta := rows[1]
	if beta.SessionID != "session-beta" {
		t.Errorf("rows[1].SessionID = %q, want session-beta", beta.SessionID)
	}
	if beta.ReceiptCount != 1 {
		t.Errorf("session-beta ReceiptCount = %d, want 1", beta.ReceiptCount)
	}
	if beta.AgentCount != 1 {
		t.Errorf("session-beta AgentCount = %d, want 1", beta.AgentCount)
	}
}

// TestReader_SessionStats_SinceFilter verifies that the since parameter
// restricts results to receipts at or after the given timestamp.
func TestReader_SessionStats_SinceFilter(t *testing.T) {
	dbPath := t.TempDir() + "/sessions-since-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}

	// session-alpha: one old receipt, one new receipt
	r1 := makeReceipt("urn:receipt:sn1", "chain-sn", 1, "tool.call",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T08:00:00Z", nil)
	r1.Issuer.SessionID = "session-alpha"
	insertLayer3(t, s, r1, "", "", nil)

	r2 := makeReceipt("urn:receipt:sn2", "chain-sn", 2, "tool.call",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:00:00Z", nil)
	r2.Issuer.SessionID = "session-alpha"
	insertLayer3(t, s, r2, "", "", nil)

	// session-beta: only an old receipt (before the since cutoff)
	r3 := makeReceipt("urn:receipt:sn3", "chain-sn2", 1, "tool.call",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T07:00:00Z", nil)
	r3.Issuer.SessionID = "session-beta"
	insertLayer3(t, s, r3, "", "", nil)
	s.Close()

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer reader.Close()

	since := "2026-04-01T09:00:00Z"
	rows, err := reader.SessionStats(&since)
	if err != nil {
		t.Fatalf("session stats with since: %v", err)
	}

	// Only session-alpha has a receipt at or after 09:00; session-beta's only
	// receipt is at 07:00 and should be excluded.
	if len(rows) != 1 {
		t.Fatalf("got %d sessions, want 1", len(rows))
	}
	if rows[0].SessionID != "session-alpha" {
		t.Errorf("session ID = %q, want session-alpha", rows[0].SessionID)
	}
	if rows[0].ReceiptCount != 1 {
		t.Errorf("ReceiptCount = %d, want 1 (only the receipt at 10:00 is in window)", rows[0].ReceiptCount)
	}
}

// ---------- FleetAttribution tests ----------

// TestFleetAttribution_CrossSessionCollision is the issue #156 de-risking test:
// two independent sessions touching the same global resource must produce a
// single cross-session state-dep edge, while a resource touched by only one
// session produces no edge. Agent keys are namespaced "<session>::<agentKey>".
func TestFleetAttribution_CrossSessionCollision(t *testing.T) {
	dbPath := t.TempDir() + "/fleet-collision-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}
	const sidA = "session-a"
	const sidB = "session-b"
	// Sessions A and B run concurrently (their activity windows interleave).
	// Session A's orchestrator touches a shared global resource (a DB row).
	insertAttr(t, s, makeAttrReceipt("urn:receipt:fa1", "chain-a", 1, "filesystem.file.write",
		receipt.RiskMedium, receipt.StatusSuccess, "2026-04-01T10:00:00Z", sidA, "", "", "db://orders/42"))
	// Session B's orchestrator later touches the same resource → cross-session dep.
	insertAttr(t, s, makeAttrReceipt("urn:receipt:fa2", "chain-b", 1, "filesystem.file.write",
		receipt.RiskMedium, receipt.StatusSuccess, "2026-04-01T10:01:00Z", sidB, "", "", "db://orders/42"))
	// Session B also touches a worktree-local path nobody else does → no edge.
	insertAttr(t, s, makeAttrReceipt("urn:receipt:fa3", "chain-b", 2, "filesystem.file.read",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:02:00Z", sidB, "", "", "/wt/b/local.go"))
	// A keeps working after B starts, so the two session windows overlap.
	insertAttr(t, s, makeAttrReceipt("urn:receipt:fa4", "chain-a", 2, "filesystem.file.read",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:03:00Z", sidA, "", "", "/wt/a/notes.md"))
	s.Close()

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer reader.Close()

	res, err := reader.FleetAttribution([]string{sidA, sidB})
	if err != nil {
		t.Fatalf("FleetAttribution: %v", err)
	}

	// Two namespaced root nodes, one per session.
	if len(res.Nodes) != 2 {
		t.Fatalf("Nodes len = %d, want 2: %+v", len(res.Nodes), res.Nodes)
	}
	wantKeys := map[string]string{
		sidA + "::__root__": sidA,
		sidB + "::__root__": sidB,
	}
	for _, n := range res.Nodes {
		wantSession, ok := wantKeys[n.AgentKey]
		if !ok {
			t.Errorf("unexpected node key %q", n.AgentKey)
			continue
		}
		if n.SessionID != wantSession {
			t.Errorf("node %q SessionID = %q, want %q", n.AgentKey, n.SessionID, wantSession)
		}
	}

	// Exactly one edge: session A → session B via the shared global resource.
	if len(res.StateDeps) != 1 {
		t.Fatalf("StateDeps len = %d, want 1: %+v", len(res.StateDeps), res.StateDeps)
	}
	sd := res.StateDeps[0]
	if sd.FromAgent != sidA+"::__root__" {
		t.Errorf("FromAgent = %q, want %q", sd.FromAgent, sidA+"::__root__")
	}
	if sd.ToAgent != sidB+"::__root__" {
		t.Errorf("ToAgent = %q, want %q", sd.ToAgent, sidB+"::__root__")
	}
	if !sd.CrossSession {
		t.Error("CrossSession = false, want true")
	}
	if !sd.CrossAgent {
		t.Error("CrossAgent = false, want true")
	}
	if !sd.TemporalOverlap {
		t.Error("TemporalOverlap = false, want true (sessions ran concurrently)")
	}
	if len(sd.Resources) != 1 || sd.Resources[0] != "db://orders/42" {
		t.Errorf("Resources = %v, want [db://orders/42]", sd.Resources)
	}
}

// TestFleetAttribution_NonConcurrentCollision verifies the temporal gate: two
// sessions that touched the same resource a day apart still produce a
// cross-session edge, but with TemporalOverlap=false — incidental same-file
// reuse, not concurrent contention. This is the case the fleet view filters out.
func TestFleetAttribution_NonConcurrentCollision(t *testing.T) {
	dbPath := t.TempDir() + "/fleet-nonconcurrent-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}
	const sidA = "session-a"
	const sidB = "session-b"
	// Session A edits a file on day 1, session B edits the same file on day 2.
	insertAttr(t, s, makeAttrReceipt("urn:receipt:nc1", "chain-a", 1, "filesystem.file.write",
		receipt.RiskMedium, receipt.StatusSuccess, "2026-04-01T10:00:00Z", sidA, "", "", "/repo/main.go"))
	insertAttr(t, s, makeAttrReceipt("urn:receipt:nc2", "chain-a", 2, "filesystem.file.read",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:30:00Z", sidA, "", "", "/repo/util.go"))
	insertAttr(t, s, makeAttrReceipt("urn:receipt:nc3", "chain-b", 1, "filesystem.file.write",
		receipt.RiskMedium, receipt.StatusSuccess, "2026-04-02T10:00:00Z", sidB, "", "", "/repo/main.go"))
	s.Close()

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer reader.Close()

	res, err := reader.FleetAttribution([]string{sidA, sidB})
	if err != nil {
		t.Fatalf("FleetAttribution: %v", err)
	}
	if len(res.StateDeps) != 1 {
		t.Fatalf("StateDeps len = %d, want 1: %+v", len(res.StateDeps), res.StateDeps)
	}
	sd := res.StateDeps[0]
	if !sd.CrossSession {
		t.Error("CrossSession = false, want true")
	}
	if sd.TemporalOverlap {
		t.Error("TemporalOverlap = true, want false (sessions ran a day apart)")
	}
}

// TestFleetAttribution_DistinctPathsNoCollision verifies that two sessions
// working in separate worktrees (distinct absolute paths) produce no
// cross-session edge — the attribution-over-undo property the issue relies on.
func TestFleetAttribution_DistinctPathsNoCollision(t *testing.T) {
	dbPath := t.TempDir() + "/fleet-distinct-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}
	const sidA = "session-a"
	const sidB = "session-b"
	insertAttr(t, s, makeAttrReceipt("urn:receipt:fd1", "chain-a", 1, "filesystem.file.write",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:00:00Z", sidA, "", "", "/wt/a/main.go"))
	insertAttr(t, s, makeAttrReceipt("urn:receipt:fd2", "chain-b", 1, "filesystem.file.write",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:01:00Z", sidB, "", "", "/wt/b/main.go"))
	s.Close()

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer reader.Close()

	res, err := reader.FleetAttribution([]string{sidA, sidB})
	if err != nil {
		t.Fatalf("FleetAttribution: %v", err)
	}
	if len(res.StateDeps) != 0 {
		t.Errorf("StateDeps len = %d, want 0 (distinct worktree paths must not collide): %+v",
			len(res.StateDeps), res.StateDeps)
	}
}

// TestFleetAttribution_IntraSessionEdgeNotCrossSession verifies that a
// within-session collision in a fleet payload keeps CrossSession=false, so the
// frontend styles it as an intra-session dependency, not a fleet collision.
func TestFleetAttribution_IntraSessionEdgeNotCrossSession(t *testing.T) {
	dbPath := t.TempDir() + "/fleet-intra-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}
	const sidA = "session-a"
	const subAgent = "subagent-xyz"
	// Within session A: root then a subagent touch the same file.
	insertAttr(t, s, makeAttrReceipt("urn:receipt:fi1", "chain-root", 1, "filesystem.file.read",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:00:00Z", sidA, "", "", "shared.go"))
	insertAttr(t, s, makeAttrReceipt("urn:receipt:fi2", "chain-sub", 1, "filesystem.file.write",
		receipt.RiskMedium, receipt.StatusSuccess, "2026-04-01T10:01:00Z", sidA, subAgent, "general-purpose", "shared.go"))
	// A second session with an unrelated resource — present but not colliding.
	insertAttr(t, s, makeAttrReceipt("urn:receipt:fi3", "chain-b", 1, "filesystem.file.read",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:02:00Z", "session-b", "", "", "other.go"))
	s.Close()

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer reader.Close()

	res, err := reader.FleetAttribution([]string{sidA, "session-b"})
	if err != nil {
		t.Fatalf("FleetAttribution: %v", err)
	}
	if len(res.StateDeps) != 1 {
		t.Fatalf("StateDeps len = %d, want 1: %+v", len(res.StateDeps), res.StateDeps)
	}
	sd := res.StateDeps[0]
	if sd.CrossSession {
		t.Errorf("CrossSession = true, want false for intra-session edge %q→%q", sd.FromAgent, sd.ToAgent)
	}
	if !sd.TemporalOverlap {
		t.Error("TemporalOverlap = false, want true (a session always overlaps itself)")
	}
	if sd.FromAgent != sidA+"::__root__" || sd.ToAgent != sidA+"::"+subAgent {
		t.Errorf("edge = %q→%q, want intra-session root→subagent", sd.FromAgent, sd.ToAgent)
	}
}

// TestFleetAttribution_Empty verifies that no session IDs yields an empty,
// non-nil payload rather than a query error.
func TestFleetAttribution_Empty(t *testing.T) {
	dbPath := seedEmptyDB(t)
	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer reader.Close()

	res, err := reader.FleetAttribution(nil)
	if err != nil {
		t.Fatalf("FleetAttribution: %v", err)
	}
	if len(res.Nodes) != 0 || len(res.StateDeps) != 0 {
		t.Errorf("want empty result, got %+v", res)
	}
	if res.BlastRadius == nil {
		t.Error("BlastRadius should be non-nil map")
	}
}

// seedFileDB creates a temporary SQLite file with test data using the SDK store,
// then closes the SDK store so the reader can open it read-only.
func seedFileDB(t *testing.T) string {
	t.Helper()
	dbPath := t.TempDir() + "/test-receipts.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}

	receipts := []receipt.AgentReceipt{
		makeReceipt("urn:receipt:001", "chain-1", 1, "filesystem.file.read", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:00:00Z", nil),
		makeReceipt("urn:receipt:002", "chain-1", 2, "filesystem.file.modify", receipt.RiskMedium, receipt.StatusSuccess, "2026-04-01T10:01:00Z", strPtr("sha256:abc")),
		makeReceipt("urn:receipt:003", "chain-1", 3, "communication.email.send", receipt.RiskHigh, receipt.StatusSuccess, "2026-04-01T10:02:00Z", strPtr("sha256:def")),
		makeReceipt("urn:receipt:004", "chain-2", 1, "filesystem.file.delete", receipt.RiskHigh, receipt.StatusFailure, "2026-04-01T11:00:00Z", nil),
		makeReceipt("urn:receipt:005", "chain-2", 2, "financial.payment.initiate", receipt.RiskCritical, receipt.StatusPending, "2026-04-01T11:01:00Z", strPtr("sha256:ghi")),
	}

	for _, r := range receipts {
		hash, err := receipt.HashReceipt(r)
		if err != nil {
			t.Fatalf("hash receipt: %v", err)
		}
		if err := s.Insert(r, hash); err != nil {
			t.Fatalf("insert receipt: %v", err)
		}
	}
	s.Close()
	return dbPath
}

