// Package verify provides hash linkage and sequence verification for receipt chains.
// Unlike the full SDK verification, this does not require a public key —
// it verifies structural integrity (hash links and sequence ordering) only.
package verify

import (
	"log"

	"obsigna.dev/sdk/go/receipt"

	"github.com/agent-receipts/dashboard/internal/store"
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
//
// Both hash linkage and signatures are recomputed from each receipt's verbatim
// wire bytes — receipt.HashRawReceipt and receipt.VerifyRaw — the canonical
// forms the collector stored and an auditor would reproduce. Round-tripping
// through the Go struct instead (receipt.HashReceipt / receipt.Verify) drops any
// forward-compat field a newer SDK emitted inside the signed payload before
// hashing or canonicalizing, making a valid chain look broken (issue #719) or a
// valid signature look forged (issue #73).
func VerifyChainLinks(receipts []store.ChainReceipt, publicKeyPEM string) ChainLinkResult {
	if len(receipts) == 0 {
		return ChainLinkResult{Valid: true, Length: 0, BrokenAt: -1}
	}

	results := make([]LinkVerification, 0, len(receipts))
	brokenAt := -1

	for i, cr := range receipts {
		r := cr.Receipt
		chain := r.CredentialSubject.Chain

		hashValid := true
		if i == 0 {
			hashValid = chain.PreviousReceiptHash == nil
		} else {
			prevHash, err := receipt.HashRawReceipt(receipts[i-1].Raw)
			if err != nil {
				// A hashing failure is not a chain break; log it so the
				// operator can tell an internal error apart from real tampering.
				log.Printf("chain hash recompute error for receipt %s: %v", receipts[i-1].Receipt.ID, err)
				hashValid = false
			} else {
				hashValid = chain.PreviousReceiptHash != nil && *chain.PreviousReceiptHash == prevHash
			}
		}

		seqValid := true
		if i == 0 {
			seqValid = chain.Sequence >= 1
		} else {
			seqValid = chain.Sequence == receipts[i-1].Receipt.CredentialSubject.Chain.Sequence+1
		}

		lv := LinkVerification{
			Index:         i,
			ReceiptID:     r.ID,
			HashLinkValid: hashValid,
			SequenceValid: seqValid,
		}

		if publicKeyPEM != "" {
			ok, err := receipt.VerifyRaw(cr.Raw, publicKeyPEM)
			if err != nil {
				log.Printf("signature verify error for receipt %s: %v", r.ID, err)
			}
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
