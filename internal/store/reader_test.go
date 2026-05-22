package store

import (
	"database/sql"
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
	defer db.Close()

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
	db.Close()

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
