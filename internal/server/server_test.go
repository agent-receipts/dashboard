package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agent-receipts/ar/sdk/go/receipt"
	sdkstore "github.com/agent-receipts/ar/sdk/go/store"
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

	var r receipt.AgentReceipt
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if r.ID != "urn:receipt:001" {
		t.Errorf("got ID %q, want urn:receipt:001", r.ID)
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
