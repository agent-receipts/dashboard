package server

import (
	"bytes"
	"crypto/ecdh"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/agent-receipts/ar/sdk/go/receipt"
)

// maxForensicKeyBody bounds the request body accepted by the key-load endpoint.
// A raw X25519 key is 32 bytes; a PKCS#8 PEM wrapper is a few hundred. 8 KiB is
// comfortably above any legitimate input and caps abuse.
const maxForensicKeyBody = 8 << 10

// forensicKeyStore holds the operator's X25519 forensic private key in process
// memory only. Per the parameter-disclosure threat model the key is never
// persisted, never logged, and is zeroed when cleared or replaced. All access
// is guarded by mu so concurrent HTTP handlers stay safe.
type forensicKeyStore struct {
	mu          sync.RWMutex
	privateKey  []byte // 32-byte raw X25519 private key; nil when unloaded
	fingerprint string // sha256:<hex> of the derived public key; "" when unloaded
}

// load derives the public key and fingerprint from priv, then stores a private
// copy. The previously held key (if any) is zeroed first. Returns the canonical
// sha256 fingerprint the operator can verify against the daemon's startup log.
func (f *forensicKeyStore) load(priv []byte) (string, error) {
	pub, err := receipt.ForensicPublicFromPrivate(priv)
	if err != nil {
		return "", err
	}
	fp, err := receipt.ForensicKeyFingerprint(pub)
	if err != nil {
		return "", err
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	zero(f.privateKey)
	f.privateKey = append([]byte(nil), priv...)
	f.fingerprint = fp
	return fp, nil
}

// clear zeroes and forgets the loaded key. Safe to call when nothing is loaded.
func (f *forensicKeyStore) clear() {
	f.mu.Lock()
	defer f.mu.Unlock()
	zero(f.privateKey)
	f.privateKey = nil
	f.fingerprint = ""
}

// status reports whether a key is loaded and its fingerprint.
func (f *forensicKeyStore) status() (loaded bool, fingerprint string) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.privateKey != nil, f.fingerprint
}

// current returns a copy of the loaded private key and its fingerprint, or
// (nil, "") when no key is loaded. The caller owns the returned slice and MUST
// zero it after use so the plaintext key does not linger.
func (f *forensicKeyStore) current() (priv []byte, fingerprint string) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.privateKey == nil {
		return nil, ""
	}
	return append([]byte(nil), f.privateKey...), f.fingerprint
}

// zero overwrites b with zeros. A no-op for nil/empty slices.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// forensicKeyStatus is the JSON returned by the key status and load endpoints.
type forensicKeyStatus struct {
	// Loaded is true when a forensic private key is held in memory.
	Loaded bool `json:"loaded"`
	// Fingerprint is the sha256:<hex> fingerprint of the loaded key's public
	// half, omitted when no key is loaded.
	Fingerprint string `json:"fingerprint,omitempty"`
	// Available is false when the dashboard is not bound to a loopback address,
	// in which case loading a key is refused (see forensicAvailable).
	Available bool `json:"available"`
	// DefaultPath is the conventional path the server probes at startup for an
	// X25519 forensic private key. The UI pre-fills its path input with this
	// value so operators can load the key with a single click.
	DefaultPath string `json:"default_path,omitempty"`
}

// disclosureResponse is the JSON returned by the per-receipt disclosure endpoint.
type disclosureResponse struct {
	// State is one of: none, locked, mismatch, decrypted, failed.
	State string `json:"state"`
	// Alg echoes the envelope ciphersuite tag for display, when present.
	Alg string `json:"alg,omitempty"`
	// Kids are the recipient fingerprints the envelope was encrypted to.
	Kids []string `json:"kids,omitempty"`
	// Fingerprint is the loaded key's fingerprint, included for the mismatch
	// state so the UI can show "expected <kid> vs loaded <fingerprint>".
	Fingerprint string `json:"fingerprint,omitempty"`
	// Parameters is the recovered plaintext, present only in the decrypted state.
	Parameters map[string]any `json:"parameters,omitempty"`
}

// isLoopbackHost reports whether host names a loopback address. An empty or
// unspecified host (e.g. "" or "0.0.0.0"/"::", which bind every interface) is
// NOT loopback: callers use this to decide whether the HTTP surface is reachable
// only from the local machine, and an all-interfaces bind is reachable from the
// network.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// forensicAvailable reports whether forensic key operations are permitted. The
// loaded private key can decrypt every matching disclosure, so it must never be
// reachable from the network: we refuse to hold a key unless the dashboard is
// bound to a loopback address.
func (s *Server) forensicAvailable() bool {
	return isLoopbackHost(s.cfg.Host)
}

// requestHostIsLocal reports whether the request's Host header names a loopback
// address. Forensic endpoints reject non-local Host values as a defense against
// DNS-rebinding, where a remote page resolves its own domain to 127.0.0.1 to
// reach this loopback-bound server from the operator's browser as if it were
// same-origin. The bind gate (forensicAvailable) and this per-request Host
// check are complementary: the former keeps the socket off the network, the
// latter blocks rebinding when the socket is on loopback.
func requestHostIsLocal(r *http.Request) bool {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	// Strip IPv6 brackets left when there was no port (e.g. "[::1]").
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	return isLoopbackHost(host)
}

// rejectNonLocalHost writes a 403 and returns true when the request's Host
// header does not name loopback (the DNS-rebinding guard shared by every
// forensic endpoint). Returns false when the request may proceed.
func (s *Server) rejectNonLocalHost(w http.ResponseWriter, r *http.Request) bool {
	if requestHostIsLocal(r) {
		return false
	}
	writeError(w, http.StatusForbidden, "forensic endpoints are restricted to local (loopback) access")
	return true
}

// guardForensic enforces both the Host-header check and the bind gate for
// endpoints that load or use a forensic key. It returns true when the request
// may proceed; on rejection it has already written the response.
func (s *Server) guardForensic(w http.ResponseWriter, r *http.Request) bool {
	if s.rejectNonLocalHost(w, r) {
		return false
	}
	if !s.forensicAvailable() {
		writeError(w, http.StatusForbidden, "forensic decryption is only available when the dashboard is bound to a loopback address")
		return false
	}
	return true
}

func (s *Server) handleForensicKeyGet(w http.ResponseWriter, r *http.Request) {
	if s.rejectNonLocalHost(w, r) {
		return
	}
	loaded, fp := s.forensic.status()
	writeJSON(w, http.StatusOK, forensicKeyStatus{
		Loaded:      loaded,
		Fingerprint: fp,
		Available:   s.forensicAvailable(),
		DefaultPath: s.cfg.ForensicKeyPath,
	})
}

func (s *Server) handleForensicKeyLoad(w http.ResponseWriter, r *http.Request) {
	if !s.guardForensic(w, r) {
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxForensicKeyBody+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read request body")
		return
	}
	// A raw-file upload puts the private key bytes in body; zero it once we're
	// done so the only retained copy is the one held (and later cleared) by the
	// key store, keeping the key's in-memory lifetime minimal.
	defer zero(body)
	if len(body) > maxForensicKeyBody {
		writeError(w, http.StatusRequestEntityTooLarge, "forensic key payload is too large")
		return
	}

	priv, err := parseForensicPrivateKey(body)
	if err != nil {
		// The error is deliberately generic and the key material is never
		// logged — only the parse outcome.
		log.Printf("forensic key load rejected: %v", err)
		writeError(w, http.StatusBadRequest, "invalid forensic key: provide a 32-byte X25519 private key (raw, hex, base64, or PKCS#8 PEM)")
		return
	}
	defer zero(priv)

	fp, err := s.forensic.load(priv)
	if err != nil {
		log.Printf("forensic key load failed: %v", err)
		writeError(w, http.StatusBadRequest, "could not derive a forensic key fingerprint from the supplied key")
		return
	}

	writeJSON(w, http.StatusOK, forensicKeyStatus{Loaded: true, Fingerprint: fp, Available: true})
}

// handleForensicKeyLoadPath loads the forensic private key from an absolute
// path on the server's filesystem. The path is supplied as JSON in the request
// body and may use a leading ~ for the current user's home directory. Relative
// paths are rejected so a typo like "forensic.key" does not silently resolve
// against the dashboard's working directory.
func (s *Server) handleForensicKeyLoadPath(w http.ResponseWriter, r *http.Request) {
	if !s.guardForensic(w, r) {
		return
	}

	// Require Content-Type: application/json so cross-origin browser POSTs
	// trigger a CORS preflight instead of being treated as "simple" requests.
	// The existing loopback + Host-header guards block network attackers and
	// DNS rebinding; this closes the remaining same-browser CSRF gap that
	// would otherwise let a hostile page drive arbitrary local file reads.
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if ct != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return
	}

	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	req.Path = strings.TrimSpace(req.Path)
	if req.Path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}

	expanded := expandHomePath(req.Path)
	if !filepath.IsAbs(expanded) {
		writeError(w, http.StatusBadRequest, "path must be absolute (or start with ~/)")
		return
	}
	data, err := readFileLimited(expanded, maxForensicKeyBody)
	if err != nil {
		if isNotExist(err) {
			writeError(w, http.StatusBadRequest, "key file not found: "+req.Path)
		} else {
			writeError(w, http.StatusBadRequest, "could not read key file: "+err.Error())
		}
		return
	}
	defer zero(data)

	priv, err := parseForensicPrivateKey(data)
	if err != nil {
		log.Printf("forensic key load from path rejected: %v", err)
		writeError(w, http.StatusBadRequest, "invalid forensic key: provide a 32-byte X25519 private key (raw, hex, base64, or PKCS#8 PEM)")
		return
	}
	defer zero(priv)

	fp, err := s.forensic.load(priv)
	if err != nil {
		log.Printf("forensic key load from path failed: %v", err)
		writeError(w, http.StatusBadRequest, "could not derive a forensic key fingerprint from the supplied key")
		return
	}

	writeJSON(w, http.StatusOK, forensicKeyStatus{Loaded: true, Fingerprint: fp, Available: true})
}

// expandHomePath replaces a leading ~ with the current user's home directory.
func expandHomePath(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}

func (s *Server) handleForensicKeyClear(w http.ResponseWriter, r *http.Request) {
	if s.rejectNonLocalHost(w, r) {
		return
	}
	s.forensic.clear()
	writeJSON(w, http.StatusOK, forensicKeyStatus{Loaded: false, Available: s.forensicAvailable()})
}

// handleReceiptDisclosure reports the decryption state of a single receipt's
// parameters_disclosure envelope using the loaded forensic key. It never blocks
// the receipt view: every outcome — including a missing key, a key mismatch, or
// a decryption failure — is a 200 with a state field the UI renders inline.
func (s *Server) handleReceiptDisclosure(w http.ResponseWriter, r *http.Request) {
	if s.rejectNonLocalHost(w, r) {
		return
	}

	id := r.PathValue("id")
	if !strings.HasPrefix(id, "urn:") {
		id = "urn:" + id
	}

	ar, err := s.reader.GetByID(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		log.Printf("disclosure lookup error: %v", err)
		return
	}
	if ar == nil {
		writeError(w, http.StatusNotFound, "receipt not found")
		return
	}

	// Plaintext must never be cached by the webview or an intermediary.
	w.Header().Set("Cache-Control", "no-store")

	env := ar.CredentialSubject.Action.ParametersDisclosure
	if env == nil {
		writeJSON(w, http.StatusOK, disclosureResponse{State: "none"})
		return
	}

	kids := recipientKIDs(env)

	priv, fp := s.forensic.current()
	if priv == nil {
		writeJSON(w, http.StatusOK, disclosureResponse{State: "locked", Alg: env.Alg, Kids: kids})
		return
	}
	defer zero(priv)

	// Attempt decryption regardless of the kid value. The kid is
	// non-authoritative metadata that DecryptDisclosure ignores; an emitter may
	// legitimately write it in a non-canonical form (e.g. a did:key URL) while
	// our key is still the correct recipient. Only after a failure do we use the
	// kid to classify it: if the envelope names our key the ciphertext is
	// corrupt/tampered (failed); otherwise our key is for a different recipient
	// (mismatch).
	params, err := receipt.DecryptDisclosure(env, priv)
	if err != nil {
		if slices.Contains(kids, fp) {
			// Log the failure but never the key or plaintext.
			log.Printf("disclosure decrypt failed for %s: %v", id, err)
			writeJSON(w, http.StatusOK, disclosureResponse{State: "failed", Alg: env.Alg, Kids: kids, Fingerprint: fp})
			return
		}
		writeJSON(w, http.StatusOK, disclosureResponse{State: "mismatch", Alg: env.Alg, Kids: kids, Fingerprint: fp})
		return
	}

	writeJSON(w, http.StatusOK, disclosureResponse{
		State:       "decrypted",
		Alg:         env.Alg,
		Kids:        kids,
		Fingerprint: fp,
		Parameters:  params,
	})
}

// recipientKIDs extracts the recipient fingerprints from an envelope.
func recipientKIDs(env *receipt.DisclosureEnvelope) []string {
	kids := make([]string, 0, len(env.Recipients))
	for _, rcpt := range env.Recipients {
		kids = append(kids, rcpt.KID)
	}
	return kids
}

// parseForensicPrivateKey accepts the forensic private key in any of the forms a
// solo operator is likely to hand it over:
//
//   - the raw 32-byte key as `--init-forensic-key` writes it (file upload),
//     optionally with surrounding whitespace such as an editor-appended newline,
//   - hex (64 chars),
//   - base64 (standard or URL alphabet, padded or unpadded),
//   - a PKCS#8 PEM wrapper around an X25519 key.
//
// It returns the raw 32-byte key. The exact-32-byte check comes first so a binary
// upload is never reinterpreted as text.
func parseForensicPrivateKey(raw []byte) ([]byte, error) {
	if len(raw) == 32 {
		return append([]byte(nil), raw...), nil
	}

	// A raw key file commonly arrives with a trailing newline or CRLF added by
	// text editors or shell redirections. Strip trailing line-ending bytes only
	// while the slice is longer than 32, so we never consume a real key byte
	// (which could itself be 0x0A or 0x0D).
	stripped := raw
	for len(stripped) > 32 && (stripped[len(stripped)-1] == '\n' || stripped[len(stripped)-1] == '\r') {
		stripped = stripped[:len(stripped)-1]
	}
	if len(stripped) == 32 {
		return append([]byte(nil), stripped...), nil
	}

	trimmed := bytes.TrimSpace(raw)

	if block, _ := pem.Decode(trimmed); block != nil {
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PKCS#8 PEM: %w", err)
		}
		xkey, ok := key.(*ecdh.PrivateKey)
		if !ok || xkey.Curve() != ecdh.X25519() {
			return nil, fmt.Errorf("PEM key is not an X25519 private key")
		}
		return xkey.Bytes(), nil
	}

	s := string(trimmed)

	if len(s) == 64 {
		if b, err := hex.DecodeString(s); err == nil && len(b) == 32 {
			return b, nil
		}
	}

	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil && len(b) == 32 {
			return b, nil
		}
	}

	return nil, fmt.Errorf("unrecognised key encoding or wrong length")
}
