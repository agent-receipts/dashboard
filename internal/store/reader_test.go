package store

import (
	"strings"
	"testing"

	"github.com/agent-receipts/ar/sdk/go/receipt"
	sdkstore "github.com/agent-receipts/ar/sdk/go/store"
)

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

func TestReader_ListReceipts_ParametersDisclosurePreview(t *testing.T) {
	dbPath := t.TempDir() + "/disclosure-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}

	withDisclosure := makeReceipt("urn:receipt:d1", "chain-d", 1,
		"tool.call", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:00:00Z", nil)
	withDisclosure.CredentialSubject.Action.ParametersDisclosure = map[string]string{
		"input":  "read /etc/passwd",
		"output": "root:x:0:0:root:/root:/bin/bash",
	}

	// A receipt with a disclosure value longer than the preview cap so we can
	// confirm SQL truncates rather than streaming the whole thing.
	long := strings.Repeat("A", 500)
	withLong := makeReceipt("urn:receipt:d2", "chain-d", 2,
		"tool.call", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:01:00Z", nil)
	withLong.CredentialSubject.Action.ParametersDisclosure = map[string]string{
		"input": long,
	}

	// A receipt with disclosure containing only non-primary keys (no input/output).
	withNonPrimaryKeys := makeReceipt("urn:receipt:d2b", "chain-d", 3,
		"tool.call", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:01:30Z", nil)
	withNonPrimaryKeys.CredentialSubject.Action.ParametersDisclosure = map[string]string{
		"api_key": "sk-...",
		"region": "us-west-2",
	}

	withoutDisclosure := makeReceipt("urn:receipt:d3", "chain-d", 4,
		"tool.call", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:02:00Z", nil)

	for _, r := range []receipt.AgentReceipt{withDisclosure, withLong, withNonPrimaryKeys, withoutDisclosure} {
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
	if len(rows) != 4 {
		t.Fatalf("got %d rows, want 4", len(rows))
	}

	// newest-first: d3, d2b, d2, d1.
	if rows[0].ParametersInputPreview != "" || rows[0].ParametersOutputPreview != "" {
		t.Errorf("d3 (no disclosure) got input=%q output=%q, want empty",
			rows[0].ParametersInputPreview, rows[0].ParametersOutputPreview)
	}
	if rows[0].HasParametersDisclosure {
		t.Errorf("d3 (no disclosure) got HasParametersDisclosure=true, want false")
	}

	// d2b: has disclosure (non-primary keys only) but no input/output previews.
	if rows[1].ParametersInputPreview != "" || rows[1].ParametersOutputPreview != "" {
		t.Errorf("d2b (non-primary keys only) got input=%q output=%q, want both empty",
			rows[1].ParametersInputPreview, rows[1].ParametersOutputPreview)
	}
	if !rows[1].HasParametersDisclosure {
		t.Errorf("d2b (has disclosure) got HasParametersDisclosure=false, want true")
	}

	if got := len(rows[2].ParametersInputPreview); got != disclosurePreviewMaxLen {
		t.Errorf("d2 input preview length = %d, want %d (truncated)", got, disclosurePreviewMaxLen)
	}
	if rows[2].ParametersOutputPreview != "" {
		t.Errorf("d2 output preview = %q, want empty", rows[2].ParametersOutputPreview)
	}
	if !rows[2].HasParametersDisclosure {
		t.Errorf("d2 (has disclosure) got HasParametersDisclosure=false, want true")
	}

	if rows[3].ParametersInputPreview != "read /etc/passwd" {
		t.Errorf("d1 input preview = %q, want %q", rows[3].ParametersInputPreview, "read /etc/passwd")
	}
	if rows[3].ParametersOutputPreview != "root:x:0:0:root:/root:/bin/bash" {
		t.Errorf("d1 output preview = %q, want %q",
			rows[3].ParametersOutputPreview, "root:x:0:0:root:/root:/bin/bash")
	}
	if !rows[3].HasParametersDisclosure {
		t.Errorf("d1 (has disclosure) got HasParametersDisclosure=false, want true")
	}
}

func TestReader_ListReceipts_OutputStatusMismatch(t *testing.T) {
	dbPath := t.TempDir() + "/mismatch-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}

	// status=success + output JSON with isError:true → mismatch.
	mismatch := makeReceipt("urn:receipt:m1", "chain-m", 1,
		"mcp.tool.call", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:00:00Z", nil)
	mismatch.CredentialSubject.Action.ParametersDisclosure = map[string]string{
		"output": `{"content":[{"text":"401 Bad credentials","type":"text"}],"isError":true}`,
	}

	// status=success + output JSON with isError:false → consistent, no mismatch.
	clean := makeReceipt("urn:receipt:m2", "chain-m", 2,
		"mcp.tool.call", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:01:00Z", nil)
	clean.CredentialSubject.Action.ParametersDisclosure = map[string]string{
		"output": `{"content":[{"text":"ok"}],"isError":false}`,
	}

	// status=success + output JSON without isError key → no mismatch.
	noFlag := makeReceipt("urn:receipt:m3", "chain-m", 3,
		"mcp.tool.call", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:02:00Z", nil)
	noFlag.CredentialSubject.Action.ParametersDisclosure = map[string]string{
		"output": `{"content":[{"text":"ok"}]}`,
	}

	// status=failure + output JSON with isError:true → consistent, no mismatch.
	expectedFail := makeReceipt("urn:receipt:m4", "chain-m", 4,
		"mcp.tool.call", receipt.RiskLow, receipt.StatusFailure, "2026-04-01T10:03:00Z", nil)
	expectedFail.CredentialSubject.Action.ParametersDisclosure = map[string]string{
		"output": `{"isError":true}`,
	}

	// status=success + plain (non-JSON) string output → no mismatch.
	plain := makeReceipt("urn:receipt:m5", "chain-m", 5,
		"mcp.tool.call", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:04:00Z", nil)
	plain.CredentialSubject.Action.ParametersDisclosure = map[string]string{
		"output": "plain text body, not JSON",
	}

	// status=success + no disclosure at all → no mismatch.
	bare := makeReceipt("urn:receipt:m6", "chain-m", 6,
		"mcp.tool.call", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:05:00Z", nil)

	for _, r := range []receipt.AgentReceipt{mismatch, clean, noFlag, expectedFail, plain, bare} {
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

	byID := map[string]ReceiptRow{}
	for _, r := range rows {
		byID[r.ID] = r
	}

	cases := []struct {
		id   string
		want bool
	}{
		{"urn:receipt:m1", true},
		{"urn:receipt:m2", false},
		{"urn:receipt:m3", false},
		{"urn:receipt:m4", false},
		{"urn:receipt:m5", false},
		{"urn:receipt:m6", false},
	}
	for _, tc := range cases {
		row, ok := byID[tc.id]
		if !ok {
			t.Errorf("%s missing from results", tc.id)
			continue
		}
		if row.OutputStatusMismatch != tc.want {
			t.Errorf("%s: OutputStatusMismatch=%v, want %v", tc.id, row.OutputStatusMismatch, tc.want)
		}
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
		prev := receipts[i-1].CredentialSubject.Chain.Sequence
		curr := receipts[i].CredentialSubject.Chain.Sequence
		if curr <= prev {
			t.Errorf("receipts not ordered: seq %d after seq %d", curr, prev)
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
	// LatestTimestamp must match the newest seeded receipt; the header
	// "Updated Nm ago" indicator depends on this value being accurate.
	const wantLatest = "2026-04-01T11:01:00Z"
	if stats.LatestTimestamp != wantLatest {
		t.Errorf("got latest %q, want %q", stats.LatestTimestamp, wantLatest)
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
