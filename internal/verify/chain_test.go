package verify

import (
	"testing"

	"github.com/agent-receipts/ar/sdk/go/receipt"
)

func makeReceipt(id, chainID string, seq int, prevHash *string) receipt.AgentReceipt {
	return receipt.AgentReceipt{
		Context:      receipt.Context(),
		ID:           id,
		Type:         receipt.CredentialType(),
		Version:      receipt.Version,
		Issuer:       receipt.Issuer{ID: "did:agent:test"},
		IssuanceDate: "2026-04-01T10:00:00Z",
		CredentialSubject: receipt.CredentialSubject{
			Principal: receipt.Principal{ID: "did:user:test"},
			Action: receipt.Action{
				ID:        "act_" + id,
				Type:      "filesystem.file.read",
				RiskLevel: receipt.RiskLow,
				Timestamp: "2026-04-01T10:00:00Z",
			},
			Outcome: receipt.Outcome{Status: receipt.StatusSuccess},
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

func strPtr(s string) *string { return &s }

func TestVerifyChainLinks_Empty(t *testing.T) {
	result := VerifyChainLinks(nil, "")
	if !result.Valid {
		t.Error("empty chain should be valid")
	}
	if result.Length != 0 {
		t.Errorf("got length %d, want 0", result.Length)
	}
}

func TestVerifyChainLinks_SingleReceipt(t *testing.T) {
	r := makeReceipt("urn:receipt:001", "chain-1", 1, nil)
	result := VerifyChainLinks([]receipt.AgentReceipt{r}, "")
	if !result.Valid {
		t.Errorf("single receipt should be valid, broken at %d", result.BrokenAt)
	}
	if result.Length != 1 {
		t.Errorf("got length %d, want 1", result.Length)
	}
	if len(result.Receipts) != 1 {
		t.Fatalf("got %d receipt results, want 1", len(result.Receipts))
	}
	if !result.Receipts[0].HashLinkValid {
		t.Error("first receipt hash link should be valid (nil previous)")
	}
	if !result.Receipts[0].SequenceValid {
		t.Error("first receipt sequence should be valid")
	}
}

func TestVerifyChainLinks_ValidChain(t *testing.T) {
	r1 := makeReceipt("urn:receipt:001", "chain-1", 1, nil)
	hash1, err := receipt.HashReceipt(r1)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	r2 := makeReceipt("urn:receipt:002", "chain-1", 2, &hash1)
	hash2, err := receipt.HashReceipt(r2)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	r3 := makeReceipt("urn:receipt:003", "chain-1", 3, &hash2)

	result := VerifyChainLinks([]receipt.AgentReceipt{r1, r2, r3}, "")
	if !result.Valid {
		t.Errorf("chain should be valid, broken at %d", result.BrokenAt)
	}
	if result.Length != 3 {
		t.Errorf("got length %d, want 3", result.Length)
	}
	for i, rv := range result.Receipts {
		if !rv.HashLinkValid {
			t.Errorf("receipt %d: hash link should be valid", i)
		}
		if !rv.SequenceValid {
			t.Errorf("receipt %d: sequence should be valid", i)
		}
	}
}

func TestVerifyChainLinks_BrokenHash(t *testing.T) {
	r1 := makeReceipt("urn:receipt:001", "chain-1", 1, nil)
	r2 := makeReceipt("urn:receipt:002", "chain-1", 2, strPtr("sha256:wrong"))

	result := VerifyChainLinks([]receipt.AgentReceipt{r1, r2}, "")
	if result.Valid {
		t.Error("chain with wrong hash should be invalid")
	}
	if result.BrokenAt != 1 {
		t.Errorf("got broken_at %d, want 1", result.BrokenAt)
	}
	if result.Receipts[1].HashLinkValid {
		t.Error("receipt 1 hash link should be invalid")
	}
}

func TestVerifyChainLinks_BrokenSequence(t *testing.T) {
	r1 := makeReceipt("urn:receipt:001", "chain-1", 1, nil)
	hash1, _ := receipt.HashReceipt(r1)
	r2 := makeReceipt("urn:receipt:002", "chain-1", 5, &hash1) // gap in sequence

	result := VerifyChainLinks([]receipt.AgentReceipt{r1, r2}, "")
	if result.Valid {
		t.Error("chain with sequence gap should be invalid")
	}
	if result.BrokenAt != 1 {
		t.Errorf("got broken_at %d, want 1", result.BrokenAt)
	}
	if result.Receipts[1].SequenceValid {
		t.Error("receipt 1 sequence should be invalid")
	}
	// Hash link should still be valid since hash matches.
	if !result.Receipts[1].HashLinkValid {
		t.Error("receipt 1 hash link should be valid despite sequence gap")
	}
}

func TestVerifyChainLinks_FirstReceiptNonNilPrevHash(t *testing.T) {
	r1 := makeReceipt("urn:receipt:001", "chain-1", 1, strPtr("sha256:shouldbenull"))

	result := VerifyChainLinks([]receipt.AgentReceipt{r1}, "")
	if result.Valid {
		t.Error("first receipt with non-nil prev hash should be invalid")
	}
	if !result.Receipts[0].SequenceValid {
		t.Error("sequence should be valid (seq=1)")
	}
	if result.Receipts[0].HashLinkValid {
		t.Error("hash link should be invalid (non-nil prev hash on first receipt)")
	}
}

func TestVerifyChainLinks_SignatureValid(t *testing.T) {
	kp, err := receipt.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}

	unsigned := receipt.UnsignedAgentReceipt{
		Context:      receipt.Context(),
		ID:           "urn:receipt:s1",
		Type:         receipt.CredentialType(),
		Version:      receipt.Version,
		Issuer:       receipt.Issuer{ID: "did:agent:test"},
		IssuanceDate: "2026-04-01T10:00:00Z",
		CredentialSubject: receipt.CredentialSubject{
			Principal: receipt.Principal{ID: "did:user:test"},
			Action: receipt.Action{
				ID:        "act_s1",
				Type:      "filesystem.file.read",
				RiskLevel: receipt.RiskLow,
				Timestamp: "2026-04-01T10:00:00Z",
			},
			Outcome: receipt.Outcome{Status: receipt.StatusSuccess},
			Chain:   receipt.Chain{Sequence: 1, ChainID: "chain-sig"},
		},
	}
	signed, err := receipt.Sign(unsigned, kp.PrivateKey, "did:agent:test#key-1")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	result := VerifyChainLinks([]receipt.AgentReceipt{signed}, kp.PublicKey)
	if len(result.Receipts) != 1 {
		t.Fatalf("got %d results, want 1", len(result.Receipts))
	}
	sv := result.Receipts[0].SignatureValid
	if sv == nil {
		t.Fatal("SignatureValid should not be nil when public key provided")
	}
	if !*sv {
		t.Error("signature should be valid for correctly signed receipt")
	}
}

func TestVerifyChainLinks_SignatureInvalid(t *testing.T) {
	kp, err := receipt.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	wrongKP, err := receipt.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate wrong key pair: %v", err)
	}

	unsigned := receipt.UnsignedAgentReceipt{
		Context:      receipt.Context(),
		ID:           "urn:receipt:s2",
		Type:         receipt.CredentialType(),
		Version:      receipt.Version,
		Issuer:       receipt.Issuer{ID: "did:agent:test"},
		IssuanceDate: "2026-04-01T10:00:00Z",
		CredentialSubject: receipt.CredentialSubject{
			Principal: receipt.Principal{ID: "did:user:test"},
			Action: receipt.Action{
				ID:        "act_s2",
				Type:      "filesystem.file.read",
				RiskLevel: receipt.RiskLow,
				Timestamp: "2026-04-01T10:00:00Z",
			},
			Outcome: receipt.Outcome{Status: receipt.StatusSuccess},
			Chain:   receipt.Chain{Sequence: 1, ChainID: "chain-sig2"},
		},
	}
	signed, err := receipt.Sign(unsigned, kp.PrivateKey, "did:agent:test#key-1")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	result := VerifyChainLinks([]receipt.AgentReceipt{signed}, wrongKP.PublicKey)
	if len(result.Receipts) != 1 {
		t.Fatalf("got %d results, want 1", len(result.Receipts))
	}
	sv := result.Receipts[0].SignatureValid
	if sv == nil {
		t.Fatal("SignatureValid should not be nil when public key provided")
	}
	if *sv {
		t.Error("signature should be invalid when verified with wrong key")
	}
}

func TestVerifyChainLinks_NoPublicKey_SignatureNotChecked(t *testing.T) {
	r1 := makeReceipt("urn:receipt:001", "chain-1", 1, nil)
	result := VerifyChainLinks([]receipt.AgentReceipt{r1}, "")
	if result.Receipts[0].SignatureValid != nil {
		t.Error("SignatureValid should be nil when no public key provided")
	}
}
