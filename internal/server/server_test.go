package server

import (
	"encoding/json"
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
	// since is exclusive: only the receipt strictly newer than the watermark.
	if len(rows) != 1 {
		t.Errorf("got %d rows, want 1", len(rows))
	}
	if len(rows) > 0 && rows[0].ID != "urn:receipt:003" {
		t.Errorf("got ID %q, want urn:receipt:003", rows[0].ID)
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
		name   string
		cfg    Config
		wantMs int64
	}{
		{"zero falls back to default", Config{}, DefaultPollInterval.Milliseconds()},
		{"explicit interval is echoed", Config{PollInterval: 2500 * time.Millisecond}, 2500},
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
				PollIntervalMs int64 `json:"poll_interval_ms"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.PollIntervalMs != tc.wantMs {
				t.Errorf("poll_interval_ms = %d, want %d", got.PollIntervalMs, tc.wantMs)
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
