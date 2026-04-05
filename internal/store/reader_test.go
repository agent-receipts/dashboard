package store

import (
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
