package store

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/agent-receipts/ar/sdk/go/receipt"
	sdkstore "github.com/agent-receipts/ar/sdk/go/store"
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

	stats, err := r.Stats()
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

	stats, err := reader.Stats()
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

	stats, err := r.Stats()
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
