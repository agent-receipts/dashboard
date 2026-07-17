package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"obsigna.dev/sdk/go/receipt"
	sdkstore "obsigna.dev/sdk/go/store"
	"obsigna.dev/sdk/go/taxonomy"
	"github.com/agent-receipts/dashboard/internal/enrich"
	"github.com/agent-receipts/dashboard/internal/store"
)

func seedTestDB(t *testing.T) string {
	t.Helper()
	dbPath := t.TempDir() + "/test-receipts.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}

	r1 := makeReceipt("urn:receipt:001", "chain-1", 1, "filesystem.file.read", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:00:00Z", nil)
	hash1, _ := receipt.HashReceipt(r1)
	r2 := makeReceipt("urn:receipt:002", "chain-1", 2, "filesystem.file.modify", receipt.RiskMedium, receipt.StatusSuccess, "2026-04-01T10:01:00Z", &hash1)
	hash2, _ := receipt.HashReceipt(r2)
	r3 := makeReceipt("urn:receipt:003", "chain-1", 3, "communication.email.send", receipt.RiskHigh, receipt.StatusFailure, "2026-04-01T10:02:00Z", &hash2)

	for _, r := range []receipt.AgentReceipt{r1, r2, r3} {
		h, _ := receipt.HashReceipt(r)
		if err := s.Insert(r, h); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	s.Close()
	return dbPath
}

func makeReceipt(id, chainID string, seq int, actionType string, risk receipt.RiskLevel, status receipt.OutcomeStatus, ts string, prevHash *string) receipt.AgentReceipt {
	return receipt.AgentReceipt{
		Context:      receipt.Context(),
		ID:           id,
		Type:         receipt.CredentialType(),
		Version:      receipt.Version,
		Issuer:       receipt.Issuer{ID: "did:agent:test-agent", Name: "Test Agent"},
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
			ProofValue: "udummy",
		},
	}
}

func setupServer(t *testing.T) *Server {
	t.Helper()
	dbPath := seedTestDB(t)
	reader, err := store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	t.Cleanup(func() { reader.Close() })
	return New(reader, Config{})
}

func TestHealthEndpoint(t *testing.T) {
	srv := setupServer(t)
	req := httptest.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", w.Code)
	}
}

func TestStatsEndpoint(t *testing.T) {
	srv := setupServer(t)
	req := httptest.NewRequest("GET", "/api/stats", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", w.Code)
	}

	var stats store.Stats
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if stats.Total != 3 {
		t.Errorf("got total %d, want 3", stats.Total)
	}
	if stats.Chains != 1 {
		t.Errorf("got chains %d, want 1", stats.Chains)
	}
}

func TestReceiptsEndpoint(t *testing.T) {
	srv := setupServer(t)
	req := httptest.NewRequest("GET", "/api/receipts", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", w.Code)
	}

	var rows []store.ReceiptRow
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("got %d rows, want 3", len(rows))
	}
}

func TestReceiptsEndpoint_FilterByRisk(t *testing.T) {
	srv := setupServer(t)
	req := httptest.NewRequest("GET", "/api/receipts?risk_level=high", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", w.Code)
	}

	var rows []store.ReceiptRow
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("got %d high-risk rows, want 1", len(rows))
	}
}

func TestReceiptsEndpoint_Limit(t *testing.T) {
	srv := setupServer(t)

	// Valid limit caps the result set.
	req := httptest.NewRequest("GET", "/api/receipts?limit=1", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("limit=1: got status %d, want 200", w.Code)
	}
	var rows []store.ReceiptRow
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("limit=1: got %d rows, want 1", len(rows))
	}

	for _, bad := range []string{"0", "-1", "abc", "10001"} {
		req = httptest.NewRequest("GET", "/api/receipts?limit="+bad, nil)
		w = httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("limit=%s: got status %d, want 400", bad, w.Code)
		}
	}
}

func TestReceiptsEndpoint_FilterBySince(t *testing.T) {
	srv := setupServer(t)
	req := httptest.NewRequest("GET", "/api/receipts?since=2026-04-01T10:01:00Z", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}

	var rows []store.ReceiptRow
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// since is inclusive: the row at the boundary plus everything newer.
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	gotIDs := map[string]bool{rows[0].ID: true, rows[1].ID: true}
	for _, want := range []string{"urn:receipt:002", "urn:receipt:003"} {
		if !gotIDs[want] {
			t.Errorf("missing %s in response", want)
		}
	}
}

func TestConfigEndpoint(t *testing.T) {
	dbPath := seedTestDB(t)
	reader, err := store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	t.Cleanup(func() { reader.Close() })

	cases := []struct {
		name       string
		cfg        Config
		wantMs     int64
		wantDBPath string
		wantDBName string
		wantVer    string
	}{
		{"zero falls back to default", Config{}, DefaultPollInterval.Milliseconds(), "", "", ""},
		{"explicit interval is echoed", Config{PollInterval: 2500 * time.Millisecond}, 2500, "", "", ""},
		{
			"db path and version are echoed",
			Config{DBPath: "/var/lib/agent-receipts/receipts.db", Version: "v1.2.3"},
			DefaultPollInterval.Milliseconds(),
			"/var/lib/agent-receipts/receipts.db",
			"receipts.db",
			"v1.2.3",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := New(reader, tc.cfg)
			req := httptest.NewRequest("GET", "/api/config", nil)
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("got status %d, want 200", w.Code)
			}
			var got struct {
				PollIntervalMs int64  `json:"poll_interval_ms"`
				ServerTime     string `json:"server_time"`
				DBPath         string `json:"db_path"`
				DBName         string `json:"db_name"`
				Version        string `json:"version"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.PollIntervalMs != tc.wantMs {
				t.Errorf("poll_interval_ms = %d, want %d", got.PollIntervalMs, tc.wantMs)
			}
			if _, err := time.Parse(time.RFC3339, got.ServerTime); err != nil {
				t.Errorf("server_time %q is not RFC3339: %v", got.ServerTime, err)
			}
			if got.DBPath != tc.wantDBPath {
				t.Errorf("db_path = %q, want %q", got.DBPath, tc.wantDBPath)
			}
			// Empty DBPath must surface as empty db_name, not "." — that's
			// what filepath.Base would return without the guard in handleConfig.
			if got.DBName != tc.wantDBName {
				t.Errorf("db_name = %q, want %q", got.DBName, tc.wantDBName)
			}
			if got.Version != tc.wantVer {
				t.Errorf("version = %q, want %q", got.Version, tc.wantVer)
			}
		})
	}
}

func TestReceiptDetailEndpoint(t *testing.T) {
	srv := setupServer(t)
	req := httptest.NewRequest("GET", "/api/receipts/urn:receipt:001", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", w.Code)
	}

	// The detail endpoint returns the signed receipt and an unverified
	// enrichment sibling: {"receipt": {...}, "enrichment": null|{...}}.
	var resp struct {
		Receipt    receipt.AgentReceipt `json:"receipt"`
		Enrichment *enrich.Enrichment   `json:"enrichment"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Receipt.ID != "urn:receipt:001" {
		t.Errorf("got ID %q, want urn:receipt:001", resp.Receipt.ID)
	}
	// The seeded fixture has no local session file, so enrichment is absent —
	// never surfaced as an error, just null.
	if resp.Enrichment != nil {
		t.Errorf("enrichment = %+v, want nil for a receipt with no local session data", resp.Enrichment)
	}
}

// fakeEnricher records the session id it was asked about and returns a fixed
// enrichment, letting the handler test exercise the populated path without a
// real local session file.
type fakeEnricher struct {
	gotSession string
	ret        *enrich.Enrichment
}

func (f *fakeEnricher) Enrich(sessionID string) *enrich.Enrichment {
	f.gotSession = sessionID
	return f.ret
}

// mapEnricher returns a fixed enrichment per session id, letting tests that
// exercise more than one session (e.g. fleet signatures) assert per-session
// enrichment without every session sharing the same fake payload.
type mapEnricher struct {
	data map[string]*enrich.Enrichment
}

func (m *mapEnricher) Enrich(sessionID string) *enrich.Enrichment {
	return m.data[sessionID]
}

func TestReceiptDetailEndpoint_EnrichmentSibling(t *testing.T) {
	dbPath := t.TempDir() + "/enrich-server-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}
	ar := makeReceipt("urn:receipt:sess", "chain-1", 1, "filesystem.file.read", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:00:00Z", nil)
	ar.Issuer.SessionID = "sess-123"
	h, err := receipt.HashReceipt(ar)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := s.Insert(ar, h); err != nil {
		t.Fatalf("insert: %v", err)
	}
	s.Close()

	reader, err := store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	t.Cleanup(func() { reader.Close() })

	srv := New(reader, Config{})
	cost := 1.25
	fake := &fakeEnricher{ret: &enrich.Enrichment{
		Unverified:       true,
		Source:           "claude-code",
		Model:            "claude-opus-4-8",
		InputTokens:      10,
		EstimatedCostUSD: &cost,
	}}
	srv.enricher = fake

	req := httptest.NewRequest("GET", "/api/receipts/urn:receipt:sess", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", w.Code, w.Body.String())
	}
	if fake.gotSession != "sess-123" {
		t.Errorf("enricher asked about %q, want sess-123 (issuer.session_id)", fake.gotSession)
	}

	var resp struct {
		Receipt    receipt.AgentReceipt `json:"receipt"`
		Enrichment *enrich.Enrichment   `json:"enrichment"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Receipt.ID != "urn:receipt:sess" {
		t.Errorf("receipt.id = %q, want urn:receipt:sess", resp.Receipt.ID)
	}
	if resp.Enrichment == nil {
		t.Fatal("enrichment = nil, want the fake enrichment")
	}
	if !resp.Enrichment.Unverified || resp.Enrichment.Source != "claude-code" {
		t.Errorf("enrichment = %+v, want unverified claude-code payload", resp.Enrichment)
	}

	// Enrichment must be a top-level sibling of receipt, never nested inside the
	// signed structure. Decode generically and assert the shape.
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &generic); err != nil {
		t.Fatalf("decode generic: %v", err)
	}
	if _, ok := generic["enrichment"]; !ok {
		t.Error("response has no top-level enrichment sibling")
	}
	var receiptObj map[string]json.RawMessage
	if err := json.Unmarshal(generic["receipt"], &receiptObj); err != nil {
		t.Fatalf("decode receipt object: %v", err)
	}
	if _, ok := receiptObj["enrichment"]; ok {
		t.Error("enrichment leaked inside the receipt object; it must be a sibling only")
	}
	if _, ok := receiptObj["credentialSubject"]; !ok {
		t.Error("receipt object missing credentialSubject — wrong nesting")
	}
}

func TestReceiptDetailEndpoint_NotFound(t *testing.T) {
	srv := setupServer(t)
	req := httptest.NewRequest("GET", "/api/receipts/urn:receipt:nonexistent", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("got status %d, want 404", w.Code)
	}
}

func TestChainsEndpoint(t *testing.T) {
	srv := setupServer(t)
	req := httptest.NewRequest("GET", "/api/chains", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", w.Code)
	}

	var chains []store.ChainSummary
	if err := json.Unmarshal(w.Body.Bytes(), &chains); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(chains) != 1 {
		t.Errorf("got %d chains, want 1", len(chains))
	}
	if chains[0].ReceiptCount != 3 {
		t.Errorf("got receipt count %d, want 3", chains[0].ReceiptCount)
	}
}

func TestChainVerifyEndpoint(t *testing.T) {
	srv := setupServer(t)
	req := httptest.NewRequest("GET", "/api/chains/chain-1/verify", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", w.Code)
	}

	var result struct {
		Valid    bool `json:"valid"`
		Length   int  `json:"length"`
		BrokenAt int  `json:"broken_at"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !result.Valid {
		t.Errorf("chain should be valid, broken at %d", result.BrokenAt)
	}
	if result.Length != 3 {
		t.Errorf("got length %d, want 3", result.Length)
	}
}

// TestChainVerifyEndpoint_ForwardCompatChain is the end-to-end regression for
// issue #719. A collector persists receipts via InsertRaw, keeping the verbatim
// wire bytes (which may carry fields a newer SDK added) and storing the hash
// computed over those bytes. The verify endpoint must read those raw bytes back
// and recompute the same hash — reporting the chain as valid, not broken.
func TestChainVerifyEndpoint_ForwardCompatChain(t *testing.T) {
	dbPath := t.TempDir() + "/fc-receipts.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}

	// rawWithFutureField marshals r and splices in a top-level field the
	// AgentReceipt struct does not know about, then returns the bytes and
	// their canonical (raw) hash.
	rawWithFutureField := func(r receipt.AgentReceipt) ([]byte, string) {
		var generic map[string]any
		b, mErr := json.Marshal(r)
		if mErr != nil {
			t.Fatalf("marshal: %v", mErr)
		}
		if uErr := json.Unmarshal(b, &generic); uErr != nil {
			t.Fatalf("unmarshal: %v", uErr)
		}
		generic["_future_field"] = "v2"
		raw, mErr := json.Marshal(generic)
		if mErr != nil {
			t.Fatalf("marshal enriched: %v", mErr)
		}
		h, hErr := receipt.HashRawReceipt(raw)
		if hErr != nil {
			t.Fatalf("hash raw: %v", hErr)
		}
		return raw, h
	}

	r1 := makeReceipt("urn:receipt:f01", "fc-chain", 1, "filesystem.file.read", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:00:00Z", nil)
	raw1, hash1 := rawWithFutureField(r1)
	r2 := makeReceipt("urn:receipt:f02", "fc-chain", 2, "filesystem.file.modify", receipt.RiskMedium, receipt.StatusSuccess, "2026-04-01T10:01:00Z", &hash1)
	raw2, hash2 := rawWithFutureField(r2)
	r3 := makeReceipt("urn:receipt:f03", "fc-chain", 3, "communication.email.send", receipt.RiskHigh, receipt.StatusSuccess, "2026-04-01T10:02:00Z", &hash2)
	raw3, hash3 := rawWithFutureField(r3)

	for _, rec := range []struct {
		r   receipt.AgentReceipt
		raw []byte
		h   string
	}{{r1, raw1, hash1}, {r2, raw2, hash2}, {r3, raw3, hash3}} {
		if err := s.InsertRaw(rec.r, rec.raw, rec.h); err != nil {
			t.Fatalf("insert raw: %v", err)
		}
	}
	s.Close()

	reader, err := store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	t.Cleanup(func() { reader.Close() })
	srv := New(reader, Config{})

	req := httptest.NewRequest("GET", "/api/chains/fc-chain/verify", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}
	var result struct {
		Valid    bool `json:"valid"`
		Length   int  `json:"length"`
		BrokenAt int  `json:"broken_at"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !result.Valid {
		t.Errorf("forward-compat chain should be valid, broken at %d", result.BrokenAt)
	}
	if result.Length != 3 {
		t.Errorf("got length %d, want 3", result.Length)
	}
}

func TestChainVerifyEndpoint_EmptyChain(t *testing.T) {
	srv := setupServer(t)
	req := httptest.NewRequest("GET", "/api/chains/nonexistent/verify", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", w.Code)
	}

	var result struct {
		Valid  bool `json:"valid"`
		Length int  `json:"length"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !result.Valid {
		t.Error("empty chain should be valid")
	}
	if result.Length != 0 {
		t.Errorf("got length %d, want 0", result.Length)
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

func setupServerWithTools(t *testing.T) *Server {
	t.Helper()
	dbPath := t.TempDir() + "/tools-receipts.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}

	recs := []receipt.AgentReceipt{
		makeReceiptWithTool("urn:receipt:st1", "chain-st", 1, "tool.call", "read_file", "server-a", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:00:00Z"),
		makeReceiptWithTool("urn:receipt:st2", "chain-st", 2, "tool.call", "read_file", "server-a", receipt.RiskLow, receipt.StatusFailure, "2026-04-01T10:01:00Z"),
		makeReceiptWithTool("urn:receipt:st3", "chain-st", 3, "tool.call", "write_file", "server-b", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:02:00Z"),
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

	reader, err := store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	t.Cleanup(func() { reader.Close() })
	return New(reader, Config{})
}

func TestServerStatsEndpoint(t *testing.T) {
	srv := setupServerWithTools(t)
	req := httptest.NewRequest("GET", "/api/stats/servers", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", w.Code, w.Body.String())
	}

	var body struct {
		Servers []store.ServerStat `json:"servers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// 2 named servers: server-a (2 receipts), server-b (1 receipt).
	if len(body.Servers) != 2 {
		t.Fatalf("got %d servers, want 2: %+v", len(body.Servers), body.Servers)
	}
	if body.Servers[0].Server != "server-a" {
		t.Errorf("servers[0].Server = %q, want server-a", body.Servers[0].Server)
	}
	if body.Servers[0].Total != 2 {
		t.Errorf("server-a total = %d, want 2", body.Servers[0].Total)
	}
	if body.Servers[0].Failure != 1 {
		t.Errorf("server-a failure = %d, want 1", body.Servers[0].Failure)
	}
	if len(body.Servers[0].Tools) != 1 {
		t.Errorf("server-a tools count = %d, want 1", len(body.Servers[0].Tools))
	}
}

func TestServerStatsEndpoint_EmptySlice(t *testing.T) {
	// An empty store must return {"servers":[]} not {"servers":null}.
	dbPath := t.TempDir() + "/empty-stats.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}
	s.Close()
	reader, err := store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	t.Cleanup(func() { reader.Close() })
	srv := New(reader, Config{})

	req := httptest.NewRequest("GET", "/api/stats/servers", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(body["servers"]) != "[]" {
		t.Errorf("empty store: servers = %s, want []", body["servers"])
	}
}

func TestServerStatsEndpoint_InvalidRange(t *testing.T) {
	srv := setupServer(t)
	req := httptest.NewRequest("GET", "/api/stats/servers?range=notaduration", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want 400", w.Code)
	}
}

func TestServerStatsEndpoint_ValidRange(t *testing.T) {
	srv := setupServerWithTools(t)
	req := httptest.NewRequest("GET", "/api/stats/servers?range=24h", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}
	var body struct {
		Servers []store.ServerStat `json:"servers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// All receipts are in the past (2026) so they won't be in a 24h window
	// relative to the test's wall clock. We just check the shape is valid.
	if body.Servers == nil {
		t.Error("servers must not be nil (empty slice expected)")
	}
}

func TestReceiptsEndpoint_FilterByServer(t *testing.T) {
	srv := setupServerWithTools(t)
	req := httptest.NewRequest("GET", "/api/receipts?server=server-a", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}
	var rows []store.ReceiptRow
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("got %d rows for server=server-a, want 2", len(rows))
	}
	for _, row := range rows {
		if row.Server != "server-a" {
			t.Errorf("row server = %q, want server-a", row.Server)
		}
	}
}

func TestReceiptsEndpoint_FilterByToolName(t *testing.T) {
	srv := setupServerWithTools(t)
	req := httptest.NewRequest("GET", "/api/receipts?tool_name=write_file", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}
	var rows []store.ReceiptRow
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("got %d rows for tool_name=write_file, want 1", len(rows))
	}
	if len(rows) == 1 && rows[0].ToolName != "write_file" {
		t.Errorf("row tool_name = %q, want write_file", rows[0].ToolName)
	}
}

func TestReceiptsEndpoint_FilterByServerAndTool(t *testing.T) {
	srv := setupServerWithTools(t)
	req := httptest.NewRequest("GET", "/api/receipts?server=server-a&tool_name=read_file", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}
	var rows []store.ReceiptRow
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("got %d rows for server-a+read_file, want 2", len(rows))
	}
}

func TestIndexPage(t *testing.T) {
	srv := setupServer(t)
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", w.Code)
	}
	contentType := w.Header().Get("Content-Type")
	if contentType != "text/html; charset=utf-8" {
		t.Errorf("got content-type %q, want text/html", contentType)
	}
}

func TestActionStatsEndpoint(t *testing.T) {
	// seedTestDB seeds 3 receipts: filesystem.file.read (success), filesystem.file.modify
	// (success), communication.email.send (failure). None of these action types has >= 5
	// receipts, so the endpoint should return an empty actions list.
	srv := setupServer(t)

	t.Run("200 with empty actions (all types below minimum threshold)", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/stats/actions", nil)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("got status %d, want 200", w.Code)
		}

		var resp struct {
			Actions []store.ActionStat `json:"actions"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		// All 3 action types have only 1 receipt each — all excluded by HAVING >= 5.
		if resp.Actions == nil {
			t.Error("actions must be [] not null")
		}
		if len(resp.Actions) != 0 {
			t.Errorf("got %d actions, want 0 (all below 5-receipt threshold)", len(resp.Actions))
		}
	})

	t.Run("400 on invalid range param", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/stats/actions?range=notaduration", nil)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("got status %d, want 400", w.Code)
		}

		var resp map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode error body: %v", err)
		}
		if resp["error"] == "" {
			t.Error("expected error message in response body")
		}
	})

	t.Run("200 with valid range param", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/stats/actions?range=24h", nil)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("got status %d, want 200", w.Code)
		}
	})
}

func TestActionStatsEndpoint_WithSufficientData(t *testing.T) {
	// Seed a database with enough receipts (>=5) for one action type so that the
	// endpoint returns populated action stats.
	dbPath := t.TempDir() + "/action-stats-server-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}

	// Insert 5 receipts for "cmd.exec" (3 failure, 2 success) and 2 for "file.read"
	// (below threshold).
	for i := 0; i < 3; i++ {
		r := makeReceipt(
			fmt.Sprintf("urn:receipt:exec-fail-%d", i), "chain-cmd", i+1,
			"cmd.exec", receipt.RiskHigh, receipt.StatusFailure,
			fmt.Sprintf("2026-05-01T10:%02d:00Z", i), nil,
		)
		h, _ := receipt.HashReceipt(r)
		if err := s.Insert(r, h); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	for i := 0; i < 2; i++ {
		r := makeReceipt(
			fmt.Sprintf("urn:receipt:exec-ok-%d", i), "chain-cmd", i+4,
			"cmd.exec", receipt.RiskHigh, receipt.StatusSuccess,
			fmt.Sprintf("2026-05-01T11:%02d:00Z", i), nil,
		)
		h, _ := receipt.HashReceipt(r)
		if err := s.Insert(r, h); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	for i := 0; i < 2; i++ {
		r := makeReceipt(
			fmt.Sprintf("urn:receipt:file-%d", i), "chain-file", i+1,
			"file.read", receipt.RiskLow, receipt.StatusSuccess,
			fmt.Sprintf("2026-05-01T12:%02d:00Z", i), nil,
		)
		h, _ := receipt.HashReceipt(r)
		if err := s.Insert(r, h); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	s.Close()

	reader, err := store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	t.Cleanup(func() { reader.Close() })
	srv := New(reader, Config{})

	req := httptest.NewRequest("GET", "/api/stats/actions", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}

	var resp struct {
		Actions []store.ActionStat `json:"actions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Only cmd.exec (5 receipts) must appear; file.read (2) must be excluded.
	if len(resp.Actions) != 1 {
		t.Fatalf("got %d actions, want 1", len(resp.Actions))
	}
	if resp.Actions[0].ActionType != "cmd.exec" {
		t.Errorf("got action_type %q, want cmd.exec", resp.Actions[0].ActionType)
	}
	if resp.Actions[0].Total != 5 {
		t.Errorf("got total %d, want 5", resp.Actions[0].Total)
	}
	if resp.Actions[0].Failure != 3 {
		t.Errorf("got failure %d, want 3", resp.Actions[0].Failure)
	}
}

func TestReceiptsEndpoint_Q(t *testing.T) {
	srv := setupServer(t)

	// A term unique to one receipt should return only that receipt.
	req := httptest.NewRequest("GET", "/api/receipts?q=email", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("q=email: got status %d, want 200", w.Code)
	}
	var rows []store.ReceiptRow
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("q=email: decode: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("q=email: got %d rows, want 1", len(rows))
	}
	if len(rows) > 0 && rows[0].ID != "urn:receipt:003" {
		t.Errorf("q=email: got ID %q, want urn:receipt:003", rows[0].ID)
	}

	// An empty q= behaves like no param — returns all rows.
	req = httptest.NewRequest("GET", "/api/receipts?q=", nil)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("q=: got status %d, want 200", w.Code)
	}
	var allRows []store.ReceiptRow
	if err := json.Unmarshal(w.Body.Bytes(), &allRows); err != nil {
		t.Fatalf("q=: decode: %v", err)
	}
	if len(allRows) != 3 {
		t.Errorf("q=: got %d rows, want 3 (same as no param)", len(allRows))
	}
}

// seedTimedDB creates a temporary SQLite file with receipts spread across
// two distinct hours, used by timeseries and range-aware stats tests.
func seedTimedDB(t *testing.T) (*Server, string) {
	t.Helper()
	dbPath := t.TempDir() + "/timed-receipts.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}

	recs := []receipt.AgentReceipt{
		makeReceipt("urn:receipt:td1", "chain-td", 1, "filesystem.file.read", receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:00:00Z", nil),
		makeReceipt("urn:receipt:td2", "chain-td", 2, "filesystem.file.modify", receipt.RiskMedium, receipt.StatusSuccess, "2026-04-01T10:30:00Z", nil),
		makeReceipt("urn:receipt:td3", "chain-td", 3, "communication.email.send", receipt.RiskHigh, receipt.StatusFailure, "2026-04-01T11:00:00Z", nil),
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

	reader, err := store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	t.Cleanup(func() { reader.Close() })
	return New(reader, Config{}), dbPath
}

func TestTimeseriesStatsEndpoint(t *testing.T) {
	srv, _ := seedTimedDB(t)

	t.Run("200 with range param produces buckets", func(t *testing.T) {
		// Use an absolute from/to that covers the seeded data.
		req := httptest.NewRequest("GET",
			"/api/stats/timeseries?from=2026-04-01T10:00:00Z&to=2026-04-01T13:00:00Z&bucket=1h",
			nil)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("got status %d, want 200: %s", w.Code, w.Body.String())
		}

		var resp struct {
			Buckets        []store.BucketRow `json:"buckets"`
			BucketDuration string            `json:"bucket_duration"`
			RangeFrom      string            `json:"range_from"`
			RangeTo        string            `json:"range_to"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.Buckets == nil {
			t.Error("buckets must not be null")
		}
		// 3 buckets: 10:00, 11:00, 12:00 (to=13:00 exclusive).
		if len(resp.Buckets) != 3 {
			t.Errorf("got %d buckets, want 3", len(resp.Buckets))
		}
		// bucket_duration is rendered cleanly, not as "1h0m0s".
		if resp.BucketDuration != "1h" {
			t.Errorf("bucket_duration = %q, want \"1h\"", resp.BucketDuration)
		}
		if resp.RangeFrom == "" || resp.RangeTo == "" {
			t.Error("range_from and range_to must not be empty")
		}
		// Bucket[0] should have 2 receipts (10:00 and 10:30).
		if len(resp.Buckets) >= 1 && resp.Buckets[0].Total != 2 {
			t.Errorf("bucket[0].Total = %d, want 2", resp.Buckets[0].Total)
		}
	})

	t.Run("to= alone is honored without from", func(t *testing.T) {
		// from omitted → earliest receipt; to= must still bound the upper edge
		// rather than defaulting to now.
		req := httptest.NewRequest("GET", "/api/stats/timeseries?to=2026-04-01T11:00:00Z", nil)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("got status %d, want 200: %s", w.Code, w.Body.String())
		}
		var resp struct {
			RangeTo string `json:"range_to"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.RangeTo != "2026-04-01T11:00:00Z" {
			t.Errorf("range_to = %q, want 2026-04-01T11:00:00Z (to must be honored without from)", resp.RangeTo)
		}
	})

	t.Run("200 with Go range param", func(t *testing.T) {
		// "range" shorthand: last 24h relative to now — seeded data is in the past,
		// but we just check the shape is valid (same as other range tests).
		req := httptest.NewRequest("GET", "/api/stats/timeseries?range=24h", nil)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("got status %d, want 200: %s", w.Code, w.Body.String())
		}
		var resp struct {
			Buckets []store.BucketRow `json:"buckets"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.Buckets == nil {
			t.Error("buckets must not be null (expect [])")
		}
	})

	t.Run("200 with day-suffix range (7d)", func(t *testing.T) {
		// The day shorthand from the issue examples must be accepted (time.ParseDuration alone rejects "7d").
		req := httptest.NewRequest("GET", "/api/stats/timeseries?range=7d", nil)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("range=7d: got status %d, want 200: %s", w.Code, w.Body.String())
		}
	})

	t.Run("400 on invalid range", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/stats/timeseries?range=notaduration", nil)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("got status %d, want 400", w.Code)
		}
	})

	t.Run("400 on bad from", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/stats/timeseries?from=notadate&to=2026-04-01T13:00:00Z", nil)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("got status %d, want 400", w.Code)
		}
	})

	t.Run("400 on bad to", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/stats/timeseries?from=2026-04-01T10:00:00Z&to=notadate", nil)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("got status %d, want 400", w.Code)
		}
	})

	t.Run("400 when from >= to", func(t *testing.T) {
		req := httptest.NewRequest("GET",
			"/api/stats/timeseries?from=2026-04-01T13:00:00Z&to=2026-04-01T10:00:00Z",
			nil)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("got status %d, want 400", w.Code)
		}
	})

	t.Run("400 when from equals to", func(t *testing.T) {
		req := httptest.NewRequest("GET",
			"/api/stats/timeseries?from=2026-04-01T10:00:00Z&to=2026-04-01T10:00:00Z",
			nil)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("got status %d, want 400", w.Code)
		}
	})
}

// seedSessionsDB creates a DB with two sessions and one no-session receipt.
func seedSessionsDB(t *testing.T) *Server {
	t.Helper()
	dbPath := t.TempDir() + "/sessions-srv.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}

	// injectSession augments a receipt's JSON with issuer.session_id and issuer.runtime.agent_id
	// and inserts it via InsertRaw (same pattern as TestChainVerifyEndpoint_ForwardCompatChain).
	injectSession := func(r receipt.AgentReceipt, sessionID, agentID string) {
		var m map[string]any
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("marshal receipt: %v", err)
		}
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("unmarshal receipt: %v", err)
		}
		issuer, _ := m["issuer"].(map[string]any)
		if issuer == nil {
			issuer = map[string]any{}
		}
		if sessionID != "" {
			issuer["session_id"] = sessionID
		}
		if agentID != "" {
			// agent_id lives under the issuer.runtime open sub-object (ADR-0026).
			issuer["runtime"] = map[string]any{"agent_id": agentID}
		}
		m["issuer"] = issuer
		raw, _ := json.Marshal(m)
		h, err := receipt.HashRawReceipt(raw)
		if err != nil {
			t.Fatalf("hash raw: %v", err)
		}
		if err := s.InsertRaw(r, raw, h); err != nil {
			t.Fatalf("insert raw: %v", err)
		}
	}

	r1 := makeReceipt("urn:receipt:ses1", "chain-ses", 1, "tool.call",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:00:00Z", nil)
	injectSession(r1, "session-alpha", "orchestrator")

	r2 := makeReceipt("urn:receipt:ses2", "chain-ses", 2, "tool.call",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T10:01:00Z", nil)
	injectSession(r2, "session-alpha", "subagent-x")

	r3 := makeReceipt("urn:receipt:ses3", "chain-ses2", 1, "tool.call",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T09:00:00Z", nil)
	injectSession(r3, "session-beta", "orchestrator")

	// No session — should be excluded from /api/sessions.
	r4 := makeReceipt("urn:receipt:ses4", "chain-old", 1, "tool.call",
		receipt.RiskLow, receipt.StatusSuccess, "2026-04-01T08:00:00Z", nil)
	h4, err := receipt.HashReceipt(r4)
	if err != nil {
		t.Fatalf("hash receipt: %v", err)
	}
	if err := s.Insert(r4, h4); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}

	s.Close()

	reader, err := store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	t.Cleanup(func() { reader.Close() })
	return New(reader, Config{})
}

func TestSessionsEndpoint(t *testing.T) {
	srv := seedSessionsDB(t)

	t.Run("200 returns sessions list excluding no-session receipts", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/sessions", nil)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("got status %d, want 200: %s", w.Code, w.Body.String())
		}

		var resp struct {
			Sessions []store.SessionRow `json:"sessions"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}

		if resp.Sessions == nil {
			t.Error("sessions must be [] not null")
		}
		if len(resp.Sessions) != 2 {
			t.Fatalf("got %d sessions, want 2", len(resp.Sessions))
		}

		// Results ordered by last_seen DESC: session-alpha (10:01) before session-beta (09:00).
		if resp.Sessions[0].SessionID != "session-alpha" {
			t.Errorf("sessions[0].session_id = %q, want session-alpha", resp.Sessions[0].SessionID)
		}
		if resp.Sessions[0].ReceiptCount != 2 {
			t.Errorf("session-alpha receipt_count = %d, want 2", resp.Sessions[0].ReceiptCount)
		}
		if resp.Sessions[0].AgentCount != 2 {
			t.Errorf("session-alpha agent_count = %d, want 2", resp.Sessions[0].AgentCount)
		}
		if resp.Sessions[1].SessionID != "session-beta" {
			t.Errorf("sessions[1].session_id = %q, want session-beta", resp.Sessions[1].SessionID)
		}
	})

	t.Run("400 on invalid range param", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/sessions?range=notaduration", nil)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("got status %d, want 400", w.Code)
		}
		var resp map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode error body: %v", err)
		}
		if resp["error"] == "" {
			t.Error("expected error message in response body")
		}
	})

	t.Run("200 with valid range param", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/sessions?range=24h", nil)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("got status %d, want 200", w.Code)
		}
	})
}

func TestStatsEndpoint_WithRange(t *testing.T) {
	srv, _ := seedTimedDB(t)

	// All-time: 3 receipts.
	req := httptest.NewRequest("GET", "/api/stats", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("all-time: got status %d, want 200", w.Code)
	}
	var allTime store.Stats
	if err := json.Unmarshal(w.Body.Bytes(), &allTime); err != nil {
		t.Fatalf("all-time decode: %v", err)
	}
	if allTime.Total != 3 {
		t.Errorf("all-time total = %d, want 3", allTime.Total)
	}

	// With after= restricting to 11:00 only (1 receipt).
	req = httptest.NewRequest("GET", "/api/stats?after=2026-04-01T11:00:00Z", nil)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("after=: got status %d, want 200", w.Code)
	}
	var filtered store.Stats
	if err := json.Unmarshal(w.Body.Bytes(), &filtered); err != nil {
		t.Fatalf("after= decode: %v", err)
	}
	if filtered.Total != 1 {
		t.Errorf("after= total = %d, want 1 (filtered counts differ from all-time)", filtered.Total)
	}
	// Confirm the range-filtered count is genuinely different from all-time.
	if filtered.Total >= allTime.Total {
		t.Errorf("filtered total (%d) must be less than all-time (%d)", filtered.Total, allTime.Total)
	}
}

// ---------- Session attribution endpoint ----------

func seedAttributionDB(t *testing.T) (*Server, string) {
	t.Helper()
	dbPath := t.TempDir() + "/attr-server-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}
	const sid = "session-attr"
	const subAgent = "sub-agent-001"

	makeAttr := func(id, chainID string, seq int, actionType string, risk receipt.RiskLevel, ts, agentID, resource string) receipt.AgentReceipt {
		ar := makeReceipt(id, chainID, seq, actionType, risk, receipt.StatusSuccess, ts, nil)
		ar.Issuer.SessionID = sid
		if agentID != "" {
			ar.Issuer.Runtime = &receipt.Runtime{AgentID: agentID, AgentType: "general-purpose"}
		}
		if resource != "" {
			ar.CredentialSubject.Action.Target = &receipt.ActionTarget{Resource: resource}
		}
		return ar
	}
	insert := func(ar receipt.AgentReceipt) {
		h, err := receipt.HashReceipt(ar)
		if err != nil {
			t.Fatalf("hash: %v", err)
		}
		if err := s.Insert(ar, h); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	// Orchestrator reads index.html, then subagent writes it.
	insert(makeAttr("urn:receipt:a1", "chain-root", 1, "filesystem.file.read", receipt.RiskLow, "2026-04-01T10:00:00Z", "", "index.html"))
	insert(makeAttr("urn:receipt:a2", "chain-sub", 1, "filesystem.file.write", receipt.RiskMedium, "2026-04-01T10:01:00Z", subAgent, "index.html"))
	// Subagent does a bash command (no resource).
	insert(makeAttr("urn:receipt:a3", "chain-sub", 2, "system.bash.execute", receipt.RiskHigh, "2026-04-01T10:02:00Z", subAgent, ""))
	s.Close()

	reader, err := store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	t.Cleanup(func() { reader.Close() })
	return New(reader, Config{}), sid
}

func TestSessionAttributionEndpoint_OK(t *testing.T) {
	srv, sid := seedAttributionDB(t)
	req := httptest.NewRequest("GET", "/api/sessions/"+sid+"/attribution", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", w.Code, w.Body.String())
	}

	var result store.AttributionResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.Coverage.TotalReceipts != 3 {
		t.Errorf("TotalReceipts = %d, want 3", result.Coverage.TotalReceipts)
	}
	if result.Coverage.IdentityReceipts != 2 {
		t.Errorf("IdentityReceipts = %d, want 2", result.Coverage.IdentityReceipts)
	}
	if len(result.StateDeps) != 1 {
		t.Fatalf("StateDeps len = %d, want 1: %+v", len(result.StateDeps), result.StateDeps)
	}
	if !result.StateDeps[0].CrossAgent {
		t.Error("StateDeps[0].CrossAgent = false, want true")
	}
	if len(result.Nodes) != 2 {
		t.Errorf("Nodes len = %d, want 2 (root + sub)", len(result.Nodes))
	}
}

func TestSessionAttributionEndpoint_MissingSession(t *testing.T) {
	srv := setupServer(t)
	req := httptest.NewRequest("GET", "/api/sessions/no-such-session/attribution", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	// An unknown session returns 200 with empty coverage (no receipts found).
	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want 200 for unknown session", w.Code)
	}
	var result store.AttributionResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.Coverage.TotalReceipts != 0 {
		t.Errorf("TotalReceipts = %d, want 0 for unknown session", result.Coverage.TotalReceipts)
	}
}

// TestSessionEnrichmentEndpoint_OK covers the populated path: an enricher
// with data for the requested session returns that Enrichment as the raw
// response body (not wrapped in an envelope).
func TestSessionEnrichmentEndpoint_OK(t *testing.T) {
	srv := setupServer(t)
	cost := 4.2
	fake := &fakeEnricher{ret: &enrich.Enrichment{
		Unverified:       true,
		Source:           "claude-code",
		Model:            "claude-opus-4-8",
		TotalTokens:      128000,
		EstimatedCostUSD: &cost,
	}}
	srv.enricher = fake

	req := httptest.NewRequest("GET", "/api/sessions/sess-abc/enrichment", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", w.Code, w.Body.String())
	}
	if fake.gotSession != "sess-abc" {
		t.Errorf("enricher asked about %q, want sess-abc", fake.gotSession)
	}

	var got enrich.Enrichment
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Unverified || got.Source != "claude-code" || got.TotalTokens != 128000 {
		t.Errorf("got %+v, want the fake enrichment", got)
	}
}

// TestSessionEnrichmentEndpoint_NilEnricher covers a server with no enricher
// configured at all: the handler must not panic and must respond with a null
// body, never an error.
func TestSessionEnrichmentEndpoint_NilEnricher(t *testing.T) {
	srv := setupServer(t)
	srv.enricher = nil

	req := httptest.NewRequest("GET", "/api/sessions/sess-abc/enrichment", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", w.Code, w.Body.String())
	}
	if got := strings.TrimSpace(w.Body.String()); got != "null" {
		t.Errorf("body = %q, want \"null\"", got)
	}
}

// TestSessionEnrichmentEndpoint_NoLocalData covers a real enricher with no
// local transcript file for the requested session — the common case for any
// dashboard not running on the same host as the agent. Absence must not be an
// error.
func TestSessionEnrichmentEndpoint_NoLocalData(t *testing.T) {
	srv := setupServer(t) // real enrich.New() enricher, no matching local file

	req := httptest.NewRequest("GET", "/api/sessions/no-such-local-session/enrichment", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", w.Code, w.Body.String())
	}
	if got := strings.TrimSpace(w.Body.String()); got != "null" {
		t.Errorf("body = %q, want \"null\"", got)
	}
}

// seedFleetAttributionDB seeds two sessions whose orchestrators collide on one
// shared global resource, plus one session-local resource that must not collide.
func seedFleetAttributionDB(t *testing.T) *Server {
	t.Helper()
	dbPath := t.TempDir() + "/fleet-server-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}
	insert := func(id, chainID string, seq int, actionType string, risk receipt.RiskLevel, ts, sid, resource string) {
		ar := makeReceipt(id, chainID, seq, actionType, risk, receipt.StatusSuccess, ts, nil)
		ar.Issuer.SessionID = sid
		if resource != "" {
			ar.CredentialSubject.Action.Target = &receipt.ActionTarget{Resource: resource}
		}
		h, err := receipt.HashReceipt(ar)
		if err != nil {
			t.Fatalf("hash: %v", err)
		}
		if err := s.Insert(ar, h); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	insert("urn:receipt:f1", "chain-a", 1, "filesystem.file.write", receipt.RiskMedium, "2026-04-01T10:00:00Z", "session-a", "db://orders/42")
	insert("urn:receipt:f2", "chain-b", 1, "filesystem.file.write", receipt.RiskMedium, "2026-04-01T10:01:00Z", "session-b", "db://orders/42")
	insert("urn:receipt:f3", "chain-b", 2, "filesystem.file.read", receipt.RiskLow, "2026-04-01T10:02:00Z", "session-b", "/wt/b/local.go")
	s.Close()

	reader, err := store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	t.Cleanup(func() { reader.Close() })
	return New(reader, Config{})
}

func TestFleetAttributionEndpoint_CrossSessionEdge(t *testing.T) {
	srv := seedFleetAttributionDB(t)
	req := httptest.NewRequest("GET", "/api/fleet/attribution", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", w.Code, w.Body.String())
	}
	var result store.AttributionResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.Nodes) != 2 {
		t.Errorf("Nodes len = %d, want 2 (one root per session)", len(result.Nodes))
	}
	if len(result.StateDeps) != 1 {
		t.Fatalf("StateDeps len = %d, want 1: %+v", len(result.StateDeps), result.StateDeps)
	}
	if !result.StateDeps[0].CrossSession {
		t.Error("StateDeps[0].CrossSession = false, want true")
	}
}

func TestFleetAttributionEndpoint_BadLimit(t *testing.T) {
	srv := seedFleetAttributionDB(t)
	req := httptest.NewRequest("GET", "/api/fleet/attribution?limit=0", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want 400 for limit=0", w.Code)
	}
}

func TestFleetAttributionEndpoint_LimitCaps(t *testing.T) {
	srv := seedFleetAttributionDB(t)
	// limit=1 restricts the fleet to the single most-recently-active session, so
	// the cross-session collision is no longer in scope and no edge is produced.
	req := httptest.NewRequest("GET", "/api/fleet/attribution?limit=1", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", w.Code, w.Body.String())
	}
	var result store.AttributionResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.StateDeps) != 0 {
		t.Errorf("StateDeps len = %d, want 0 with limit=1: %+v", len(result.StateDeps), result.StateDeps)
	}
}

func TestTaxonomyEndpoint(t *testing.T) {
	srv := setupServer(t)
	req := httptest.NewRequest("GET", "/api/taxonomy", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}

	var body struct {
		Categories []taxonomyCategory `json:"categories"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Every category must carry a name and at least one fully-populated entry —
	// the frontend renders type, description and risk level for each.
	byType := map[string]taxonomy.ActionTypeEntry{}
	for _, c := range body.Categories {
		if c.Name == "" {
			t.Error("category with empty name")
		}
		if len(c.Actions) == 0 {
			t.Errorf("category %q has no actions", c.Name)
		}
		for _, a := range c.Actions {
			if a.Type == "" || a.Description == "" || a.RiskLevel == "" {
				t.Errorf("incomplete entry in %q: %+v", c.Name, a)
			}
			// Each type must belong to exactly one category — overwriting here
			// would hide a type duplicated across categories.
			if _, dup := byType[a.Type]; dup {
				t.Errorf("action type %q appears in more than one category", a.Type)
			}
			byType[a.Type] = a
		}
	}

	// Completeness: every built-in the SDK knows about must appear in exactly
	// one category. This turns an SDK upgrade that adds a new action type into a
	// failing test rather than a built-in silently missing from the reference
	// view and its tooltips.
	for _, want := range taxonomy.AllActions() {
		got, ok := byType[want.Type]
		if !ok {
			t.Errorf("AllActions entry %q missing from /api/taxonomy response", want.Type)
			continue
		}
		if got != want {
			t.Errorf("entry %q = %+v, want %+v", want.Type, got, want)
		}
	}

	// Spot-check representative built-ins and their default risk levels so a
	// regression in the SDK wiring is caught.
	wantRisk := map[string]receipt.RiskLevel{
		"filesystem.file.read":      receipt.RiskLow,
		"system.command.execute":    receipt.RiskHigh,
		taxonomy.UnknownAction.Type: receipt.RiskMedium,
	}
	for typ, risk := range wantRisk {
		got, ok := byType[typ]
		if !ok {
			t.Errorf("taxonomy missing action type %q", typ)
			continue
		}
		if got.RiskLevel != risk {
			t.Errorf("%s risk = %q, want %q", typ, got.RiskLevel, risk)
		}
	}
}

// ---------- /api/config experimental field ----------

func TestConfigEndpoint_ExperimentalField(t *testing.T) {
	dbPath := seedTestDB(t)
	reader, err := store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	t.Cleanup(func() { reader.Close() })

	for _, tc := range []struct {
		name         string
		experimental bool
	}{
		{"experimental false (default)", false},
		{"experimental true", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := New(reader, Config{Experimental: tc.experimental})
			req := httptest.NewRequest("GET", "/api/config", nil)
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("got status %d, want 200", w.Code)
			}
			var got struct {
				Experimental bool `json:"experimental"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.Experimental != tc.experimental {
				t.Errorf("experimental = %v, want %v", got.Experimental, tc.experimental)
			}
		})
	}
}

// ---------- /api/fleet/signatures endpoint ----------

// seedFleetDB builds a DB with two sessions seeded with mixed action types and
// agent types, suitable for testing the fleet signatures endpoint.
func seedFleetDB(t *testing.T) *store.Reader {
	t.Helper()
	dbPath := t.TempDir() + "/fleet-srv-test.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}

	insert := func(ar receipt.AgentReceipt) {
		h, err := receipt.HashReceipt(ar)
		if err != nil {
			t.Fatalf("hash: %v", err)
		}
		if err := s.Insert(ar, h); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	makeFleet := func(id, chainID string, seq int, actionType, ts, sessionID, agentType string) receipt.AgentReceipt {
		ar := makeReceipt(id, chainID, seq, actionType, receipt.RiskLow, receipt.StatusSuccess, ts, nil)
		ar.Issuer.SessionID = sessionID
		if agentType != "" {
			ar.Issuer.Runtime = &receipt.Runtime{AgentType: agentType}
		}
		return ar
	}

	// Session alpha — more recent (last_seen 10:04), comes first in SessionStats.
	insert(makeFleet("urn:receipt:fl1", "chain-fl1", 1, "claude-code.Bash", "2026-06-01T10:00:00Z", "alpha", ""))
	insert(makeFleet("urn:receipt:fl2", "chain-fl1", 2, "claude-code.Read", "2026-06-01T10:01:00Z", "alpha", "general-purpose"))
	insert(makeFleet("urn:receipt:fl3", "chain-fl1", 3, "mcp.github.read", "2026-06-01T10:04:00Z", "alpha", "Explore"))

	// Session beta — older (last_seen 09:01), comes second in SessionStats.
	insert(makeFleet("urn:receipt:fl4", "chain-fl2", 1, "claude-code.Edit", "2026-06-01T09:00:00Z", "beta", "general-purpose"))
	insert(makeFleet("urn:receipt:fl5", "chain-fl2", 2, "claude-code.ToolSearch", "2026-06-01T09:01:00Z", "beta", "general-purpose"))

	s.Close()

	reader, err := store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	t.Cleanup(func() { reader.Close() })
	return reader
}

func TestFleetSignaturesEndpoint_DisabledWithout_Experimental(t *testing.T) {
	reader := seedFleetDB(t)
	srv := New(reader, Config{Experimental: false})

	req := httptest.NewRequest("GET", "/api/fleet/signatures", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("got status %d, want 404 when experimental=false", w.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body["error"] == "" {
		t.Error("expected error message in response body")
	}
}

func TestFleetSignaturesEndpoint_OK(t *testing.T) {
	reader := seedFleetDB(t)
	srv := New(reader, Config{Experimental: true})

	req := httptest.NewRequest("GET", "/api/fleet/signatures", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", w.Code, w.Body.String())
	}

	var body struct {
		Signatures []store.SessionSignature `json:"signatures"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body.Signatures == nil {
		t.Error("signatures must be [] not null")
	}
	if len(body.Signatures) != 2 {
		t.Fatalf("got %d signatures, want 2", len(body.Signatures))
	}

	// SessionStats orders by last_seen DESC: alpha (10:04) before beta (09:01).
	if body.Signatures[0].SessionID != "alpha" {
		t.Errorf("signatures[0].session_id = %q, want alpha", body.Signatures[0].SessionID)
	}
	if body.Signatures[0].ReceiptCount != 3 {
		t.Errorf("alpha receipt_count = %d, want 3", body.Signatures[0].ReceiptCount)
	}
	if body.Signatures[0].Activity["bash"] != 1 {
		t.Errorf("alpha activity[bash] = %d, want 1", body.Signatures[0].Activity["bash"])
	}
	if body.Signatures[0].Activity["mcp"] != 1 {
		t.Errorf("alpha activity[mcp] = %d, want 1", body.Signatures[0].Activity["mcp"])
	}
	if body.Signatures[1].SessionID != "beta" {
		t.Errorf("signatures[1].session_id = %q, want beta", body.Signatures[1].SessionID)
	}
	if body.Signatures[1].ReceiptCount != 2 {
		t.Errorf("beta receipt_count = %d, want 2", body.Signatures[1].ReceiptCount)
	}
}

func TestFleetSignaturesEndpoint_LimitParam(t *testing.T) {
	reader := seedFleetDB(t)
	srv := New(reader, Config{Experimental: true})

	// limit=1 should return only the most-recent session (alpha).
	req := httptest.NewRequest("GET", "/api/fleet/signatures?limit=1", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("limit=1: got status %d, want 200", w.Code)
	}
	var body struct {
		Signatures []store.SessionSignature `json:"signatures"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Signatures) != 1 {
		t.Fatalf("limit=1: got %d signatures, want 1", len(body.Signatures))
	}
	if body.Signatures[0].SessionID != "alpha" {
		t.Errorf("limit=1: signatures[0].session_id = %q, want alpha", body.Signatures[0].SessionID)
	}

	// limit=0 and limit=-1 must return 400.
	for _, bad := range []string{"0", "-1", "abc"} {
		req = httptest.NewRequest("GET", "/api/fleet/signatures?limit="+bad, nil)
		w = httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("limit=%s: got status %d, want 400", bad, w.Code)
		}
	}

	// limit=99 must be silently capped at 24 (no error).
	req = httptest.NewRequest("GET", "/api/fleet/signatures?limit=99", nil)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("limit=99: got status %d, want 200 (cap not reject)", w.Code)
	}
}

// TestFleetSignaturesEndpoint_Enrichment covers the composed per-session
// enrichment: each session gets its own distinct Enrichment (never a shared
// or mixed-up one), and a session with no local transcript file (no entry in
// the map) gets no "enrichment" key at all in the raw JSON, not an explicit
// null — the omitempty tag keeps the common no-local-data case lean.
func TestFleetSignaturesEndpoint_Enrichment(t *testing.T) {
	reader := seedFleetDB(t)
	srv := New(reader, Config{Experimental: true})

	costAlpha := 1.5
	me := &mapEnricher{data: map[string]*enrich.Enrichment{
		"alpha": {
			Unverified:       true,
			Source:           "claude-code",
			TotalTokens:      128000,
			EstimatedCostUSD: &costAlpha,
		},
		// beta intentionally has no entry, simulating no local transcript file.
	}}
	srv.enricher = me

	req := httptest.NewRequest("GET", "/api/fleet/signatures", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", w.Code, w.Body.String())
	}

	var body struct {
		Signatures []fleetSignatureWithEnrichment `json:"signatures"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Signatures) != 2 {
		t.Fatalf("got %d signatures, want 2", len(body.Signatures))
	}

	// SessionStats orders by last_seen DESC: alpha (10:04) before beta (09:01).
	if body.Signatures[0].SessionID != "alpha" {
		t.Fatalf("signatures[0].session_id = %q, want alpha", body.Signatures[0].SessionID)
	}
	if body.Signatures[0].Enrichment == nil {
		t.Fatal("alpha enrichment = nil, want the mapped enrichment")
	}
	if body.Signatures[0].Enrichment.TotalTokens != 128000 {
		t.Errorf("alpha total_tokens = %d, want 128000", body.Signatures[0].Enrichment.TotalTokens)
	}

	if body.Signatures[1].SessionID != "beta" {
		t.Fatalf("signatures[1].session_id = %q, want beta", body.Signatures[1].SessionID)
	}
	if body.Signatures[1].Enrichment != nil {
		t.Errorf("beta enrichment = %+v, want nil (no local transcript file)", body.Signatures[1].Enrichment)
	}

	var raw struct {
		Signatures []map[string]json.RawMessage `json:"signatures"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	if _, ok := raw.Signatures[0]["enrichment"]; !ok {
		t.Error("alpha: expected an \"enrichment\" key in the raw JSON")
	}
	if _, ok := raw.Signatures[1]["enrichment"]; ok {
		t.Error("beta: \"enrichment\" key present in raw JSON, want omitted (omitempty)")
	}
}

// TestFleetSignaturesEndpoint_NilEnricher covers a server with no enricher
// configured at all: every signature must omit the "enrichment" key rather
// than error or emit an explicit null.
func TestFleetSignaturesEndpoint_NilEnricher(t *testing.T) {
	reader := seedFleetDB(t)
	srv := New(reader, Config{Experimental: true})
	srv.enricher = nil

	req := httptest.NewRequest("GET", "/api/fleet/signatures", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", w.Code, w.Body.String())
	}

	var raw struct {
		Signatures []map[string]json.RawMessage `json:"signatures"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	if len(raw.Signatures) != 2 {
		t.Fatalf("got %d signatures, want 2", len(raw.Signatures))
	}
	for i, sig := range raw.Signatures {
		if _, ok := sig["enrichment"]; ok {
			t.Errorf("signatures[%d]: \"enrichment\" key present with nil enricher, want omitted", i)
		}
	}
}
