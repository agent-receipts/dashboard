// Package verify provides hash linkage and sequence verification for receipt chains.
// Unlike the full SDK verification, this does not require a public key —
// it verifies structural integrity (hash links and sequence ordering) only.
package verify

import (
	"github.com/agent-receipts/ar/sdk/go/receipt"
)

// LinkVerification holds the result for a single receipt in a chain.
type LinkVerification struct {
	Index         int    `json:"index"`
	ReceiptID     string `json:"receipt_id"`
	HashLinkValid bool   `json:"hash_link_valid"`
	SequenceValid bool   `json:"sequence_valid"`
	// SignatureValid is nil when no public key was provided (not checked),
	// true when the Ed25519 signature is valid, false when invalid.
	SignatureValid *bool `json:"signature_valid,omitempty"`
}

// ChainLinkResult holds the verification result for an entire chain.
type ChainLinkResult struct {
	Valid    bool               `json:"valid"`
	Length   int                `json:"length"`
	Receipts []LinkVerification `json:"receipts"`
	BrokenAt int                `json:"broken_at"` // -1 if valid
}

// VerifyChainLinks verifies the hash linkage and sequence ordering of a chain.
// When publicKeyPEM is non-empty, each receipt's Ed25519 signature is also
// verified using the ar SDK; SignatureValid will be set per receipt.
// When publicKeyPEM is empty, signature checks are skipped (SignatureValid is nil).
func VerifyChainLinks(receipts []receipt.AgentReceipt, publicKeyPEM string) ChainLinkResult {
	if len(receipts) == 0 {
		return ChainLinkResult{Valid: true, Length: 0, BrokenAt: -1}
	}

	results := make([]LinkVerification, 0, len(receipts))
	brokenAt := -1

	for i, r := range receipts {
		chain := r.CredentialSubject.Chain

		hashValid := true
		if i == 0 {
			hashValid = chain.PreviousReceiptHash == nil
		} else {
			prevHash, err := receipt.HashReceipt(receipts[i-1])
			if err != nil {
				hashValid = false
			} else {
				hashValid = chain.PreviousReceiptHash != nil && *chain.PreviousReceiptHash == prevHash
			}
		}

		seqValid := true
		if i == 0 {
			seqValid = chain.Sequence >= 1
		} else {
			seqValid = chain.Sequence == receipts[i-1].CredentialSubject.Chain.Sequence+1
		}

		lv := LinkVerification{
			Index:         i,
			ReceiptID:     r.ID,
			HashLinkValid: hashValid,
			SequenceValid: seqValid,
		}

		if publicKeyPEM != "" {
			ok, _ := receipt.Verify(r, publicKeyPEM)
			lv.SignatureValid = &ok
		}

		results = append(results, lv)

		if brokenAt == -1 && (!hashValid || !seqValid) {
			brokenAt = i
		}
	}

	return ChainLinkResult{
		Valid:    brokenAt == -1,
		Length:   len(receipts),
		Receipts: results,
		BrokenAt: brokenAt,
	}
}
