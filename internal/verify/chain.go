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
}

// ChainLinkResult holds the verification result for an entire chain.
type ChainLinkResult struct {
	Valid    bool               `json:"valid"`
	Length   int                `json:"length"`
	Receipts []LinkVerification `json:"receipts"`
	BrokenAt int               `json:"broken_at"` // -1 if valid
}

// VerifyChainLinks verifies the hash linkage and sequence ordering of a chain.
// It does NOT verify signatures (no public key required).
func VerifyChainLinks(receipts []receipt.AgentReceipt) ChainLinkResult {
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

		results = append(results, LinkVerification{
			Index:         i,
			ReceiptID:     r.ID,
			HashLinkValid: hashValid,
			SequenceValid: seqValid,
		})

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
