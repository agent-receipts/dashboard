package verify

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"testing"

	"obsigna.dev/sdk/go/receipt"

	"github.com/agent-receipts/dashboard/internal/store"
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

// chainReceipt pairs a receipt with the verbatim wire bytes the store would
// hold, mirroring store.GetChain. Verification recomputes hashes from Raw.
func chainReceipt(t *testing.T, r receipt.AgentReceipt) store.ChainReceipt {
	t.Helper()
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal receipt: %v", err)
	}
	return store.ChainReceipt{Receipt: r, Raw: raw}
}

// rawHash returns the canonical hash of a chain receipt's wire bytes — the
// value a producer would store in the next receipt's previous_receipt_hash.
func rawHash(t *testing.T, cr store.ChainReceipt) string {
	t.Helper()
	h, err := receipt.HashRawReceipt(cr.Raw)
	if err != nil {
		t.Fatalf("hash raw receipt: %v", err)
	}
	return h
}

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
	cr := chainReceipt(t, makeReceipt("urn:receipt:001", "chain-1", 1, nil))
	result := VerifyChainLinks([]store.ChainReceipt{cr}, "")
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
	cr1 := chainReceipt(t, makeReceipt("urn:receipt:001", "chain-1", 1, nil))
	hash1 := rawHash(t, cr1)
	cr2 := chainReceipt(t, makeReceipt("urn:receipt:002", "chain-1", 2, &hash1))
	hash2 := rawHash(t, cr2)
	cr3 := chainReceipt(t, makeReceipt("urn:receipt:003", "chain-1", 3, &hash2))

	result := VerifyChainLinks([]store.ChainReceipt{cr1, cr2, cr3}, "")
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

// TestVerifyChainLinks_ForwardCompatFields is the regression test for issue
// #719. A chain whose stored hashes were computed over wire bytes carrying a
// field the Go struct does not know about must still verify as valid: the
// recompute must hash the raw bytes, not a re-marshal of the struct.
func TestVerifyChainLinks_ForwardCompatFields(t *testing.T) {
	// Build chain receipts whose Raw bytes carry a forward-compat top-level
	// field the AgentReceipt struct drops on Unmarshal.
	withFutureField := func(t *testing.T, r receipt.AgentReceipt) store.ChainReceipt {
		t.Helper()
		var generic map[string]any
		raw, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := json.Unmarshal(raw, &generic); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		generic["_future_field"] = "v2"
		enriched, err := json.Marshal(generic)
		if err != nil {
			t.Fatalf("marshal enriched: %v", err)
		}
		return store.ChainReceipt{Receipt: r, Raw: enriched}
	}

	cr1 := withFutureField(t, makeReceipt("urn:receipt:001", "default", 1, nil))
	hash1 := rawHash(t, cr1)
	cr2 := withFutureField(t, makeReceipt("urn:receipt:002", "default", 2, &hash1))
	hash2 := rawHash(t, cr2)
	cr3 := withFutureField(t, makeReceipt("urn:receipt:003", "default", 3, &hash2))

	result := VerifyChainLinks([]store.ChainReceipt{cr1, cr2, cr3}, "")
	if !result.Valid {
		t.Fatalf("chain with forward-compat fields should be valid, broken at %d", result.BrokenAt)
	}
	for i, rv := range result.Receipts {
		if !rv.HashLinkValid {
			t.Errorf("receipt %d: hash link should be valid", i)
		}
	}

	// Sanity check: hashing the re-marshalled struct (the old behaviour)
	// would drop _future_field and disagree, proving the test exercises the
	// real divergence rather than a no-op.
	structHash, err := receipt.HashReceipt(cr1.Receipt)
	if err != nil {
		t.Fatalf("hash receipt: %v", err)
	}
	if structHash == hash1 {
		t.Fatal("expected struct hash to differ from raw hash with forward-compat field present")
	}
}

func TestVerifyChainLinks_BrokenHash(t *testing.T) {
	cr1 := chainReceipt(t, makeReceipt("urn:receipt:001", "chain-1", 1, nil))
	cr2 := chainReceipt(t, makeReceipt("urn:receipt:002", "chain-1", 2, strPtr("sha256:wrong")))

	result := VerifyChainLinks([]store.ChainReceipt{cr1, cr2}, "")
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
	cr1 := chainReceipt(t, makeReceipt("urn:receipt:001", "chain-1", 1, nil))
	hash1 := rawHash(t, cr1)
	cr2 := chainReceipt(t, makeReceipt("urn:receipt:002", "chain-1", 5, &hash1)) // gap in sequence

	result := VerifyChainLinks([]store.ChainReceipt{cr1, cr2}, "")
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
	cr1 := chainReceipt(t, makeReceipt("urn:receipt:001", "chain-1", 1, strPtr("sha256:shouldbenull")))

	result := VerifyChainLinks([]store.ChainReceipt{cr1}, "")
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

	result := VerifyChainLinks([]store.ChainReceipt{chainReceipt(t, signed)}, kp.PublicKey)
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

	result := VerifyChainLinks([]store.ChainReceipt{chainReceipt(t, signed)}, wrongKP.PublicKey)
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
	cr := chainReceipt(t, makeReceipt("urn:receipt:001", "chain-1", 1, nil))
	result := VerifyChainLinks([]store.ChainReceipt{cr}, "")
	if result.Receipts[0].SignatureValid != nil {
		t.Error("SignatureValid should be nil when no public key provided")
	}
}

// signRawPayload signs a generic receipt payload (without a proof block) and
// returns the full wire bytes with the proof spliced in, the way an SDK newer
// than this build would. It lets the test carry a signed field that the
// AgentReceipt struct does not model.
func signRawPayload(t *testing.T, payload map[string]any, privateKeyPEM string) []byte {
	t.Helper()
	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		t.Fatal("decode private key PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}
	priv, ok := key.(ed25519.PrivateKey)
	if !ok {
		t.Fatal("private key is not Ed25519")
	}
	canonical, err := receipt.Canonicalize(payload)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	sig := ed25519.Sign(priv, []byte(canonical))
	payload["proof"] = map[string]any{
		"type":               "Ed25519Signature2020",
		"proofValue":         "u" + base64.RawURLEncoding.EncodeToString(sig),
		"verificationMethod": "did:agent:test#key-1",
		"proofPurpose":       "assertionMethod",
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return raw
}

func TestVerifyChainLinks_SignatureValid_ForwardCompatNested(t *testing.T) {
	// Issue #73: a newer SDK can sign over a field nested inside the payload
	// (e.g. under credentialSubject) that this build's struct does not model.
	// VerifyChainLinks must verify the signature against the verbatim wire bytes
	// (receipt.VerifyRaw), not a re-marshal of the struct (receipt.Verify) that
	// drops the field and false-negatives a genuinely valid signature.
	kp, err := receipt.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}

	r := makeReceipt("urn:receipt:fc-sig", "chain-fc-sig", 1, nil)
	// Derive the signed payload from the struct so field names match the wire,
	// then splice in one field nested under credentialSubject.
	ub, err := json.Marshal(receipt.UnsignedAgentReceipt{
		Context:           r.Context,
		ID:                r.ID,
		Type:              r.Type,
		Version:           r.Version,
		Issuer:            r.Issuer,
		IssuanceDate:      r.IssuanceDate,
		CredentialSubject: r.CredentialSubject,
	})
	if err != nil {
		t.Fatalf("marshal unsigned: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(ub, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	payload["credentialSubject"].(map[string]any)["_future_nested"] = "v2"

	raw := signRawPayload(t, payload, kp.PrivateKey)
	cr := store.ChainReceipt{Receipt: r, Raw: raw}

	result := VerifyChainLinks([]store.ChainReceipt{cr}, kp.PublicKey)
	sv := result.Receipts[0].SignatureValid
	if sv == nil {
		t.Fatal("SignatureValid should not be nil when public key provided")
	}
	if !*sv {
		t.Fatal("signature over a nested forward-compat field should verify via raw bytes (issue #73)")
	}

	// Sanity check: the old struct-based path drops the nested field and
	// false-negatives, proving the test exercises the real divergence the swap
	// to VerifyRaw fixes — not a no-op.
	var parsed receipt.AgentReceipt
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal parsed: %v", err)
	}
	structOK, err := receipt.Verify(parsed, kp.PublicKey)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if structOK {
		t.Fatal("expected struct-based Verify to false-negative on the nested field")
	}
}
