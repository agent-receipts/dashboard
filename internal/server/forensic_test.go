package server

import (
	"bytes"
	"crypto/ecdh"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/agent-receipts/ar/sdk/go/receipt"
	sdkstore "github.com/agent-receipts/ar/sdk/go/store"
	"github.com/agent-receipts/dashboard/internal/store"
)

// localReq builds a request whose Host header names loopback, matching what a
// browser on 127.0.0.1 sends. httptest.NewRequest defaults Host to
// "example.com", which the forensic endpoints reject as a DNS-rebinding guard.
func localReq(method, target string, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, target, body)
	r.Host = "127.0.0.1:8080"
	return r
}

// localJSONReq is like localReq but also sets Content-Type: application/json,
// matching what /api/forensic-key/path requires of legitimate clients.
func localJSONReq(method, target string, body io.Reader) *http.Request {
	r := localReq(method, target, body)
	r.Header.Set("Content-Type", "application/json")
	return r
}

// forensicKeyPair returns a fresh X25519 forensic key pair and its canonical
// sha256 fingerprint (the value the daemon writes as a recipient kid).
func forensicKeyPair(t *testing.T) (priv, pub []byte, fingerprint string) {
	t.Helper()
	kp, err := receipt.GenerateForensicKeyPair()
	if err != nil {
		t.Fatalf("generate forensic key pair: %v", err)
	}
	fp, err := receipt.ForensicKeyFingerprint(kp.PublicKey)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	return kp.PrivateKey, kp.PublicKey, fp
}

// encReceipt builds a receipt whose parameters_disclosure is an HPKE envelope
// encrypted to pub/kid.
func encReceipt(t *testing.T, id string, pub []byte, kid string, params map[string]any) receipt.AgentReceipt {
	t.Helper()
	env, err := receipt.EncryptDisclosure(params, pub, kid)
	if err != nil {
		t.Fatalf("encrypt disclosure: %v", err)
	}
	r := makeReceipt(id, "chain-enc", 1, "system.command.execute", receipt.RiskHigh, receipt.StatusSuccess, "2026-05-01T10:00:00Z", nil)
	r.CredentialSubject.Action.ParametersDisclosure = env
	return r
}

// seedReceipts writes the given receipts to a fresh read-only store and returns
// a Server reading from it.
func seedReceipts(t *testing.T, cfg Config, recs ...receipt.AgentReceipt) *Server {
	t.Helper()
	dbPath := t.TempDir() + "/forensic.db"
	s, err := sdkstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sdk store: %v", err)
	}
	for _, r := range recs {
		h, _ := receipt.HashReceipt(r)
		if err := s.Insert(r, h); err != nil {
			s.Close()
			t.Fatalf("insert %s: %v", r.ID, err)
		}
	}
	s.Close()

	reader, err := store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	t.Cleanup(func() { reader.Close() })
	return New(reader, cfg)
}

func decodeDisclosure(t *testing.T, body []byte) disclosureResponse {
	t.Helper()
	var resp disclosureResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode disclosure response: %v (body=%s)", err, body)
	}
	return resp
}

func TestParseForensicPrivateKey(t *testing.T) {
	priv, _, _ := forensicKeyPair(t)

	pemKey := func() []byte {
		ek, err := ecdh.X25519().NewPrivateKey(priv)
		if err != nil {
			t.Fatalf("ecdh key: %v", err)
		}
		der, err := x509.MarshalPKCS8PrivateKey(ek)
		if err != nil {
			t.Fatalf("marshal pkcs8: %v", err)
		}
		return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	}()

	cases := []struct {
		name string
		in   []byte
	}{
		{"raw", priv},
		{"raw with trailing newline", append(append([]byte(nil), priv...), '\n')},
		{"raw with trailing crlf", append(append([]byte(nil), priv...), '\r', '\n')},
		{"hex", []byte(hex.EncodeToString(priv))},
		{"hex with whitespace", []byte("  " + hex.EncodeToString(priv) + "\n")},
		{"base64 std", []byte(base64.StdEncoding.EncodeToString(priv))},
		{"base64 rawurl", []byte(base64.RawURLEncoding.EncodeToString(priv))},
		{"pem pkcs8", pemKey},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseForensicPrivateKey(tc.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !bytes.Equal(got, priv) {
				t.Fatalf("parsed key mismatch")
			}
		})
	}

	bad := []struct {
		name string
		in   []byte
	}{
		{"too short", priv[:31]},
		{"garbage text", []byte("not-a-key")},
		{"empty", []byte("")},
	}
	for _, tc := range bad {
		t.Run("reject/"+tc.name, func(t *testing.T) {
			if _, err := parseForensicPrivateKey(tc.in); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

func TestForensicKeyLoadStatusClear(t *testing.T) {
	priv, _, fp := forensicKeyPair(t)
	srv := seedReceipts(t, Config{Host: "127.0.0.1"})
	h := srv.Handler()

	// Load.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, localReq("POST", "/api/forensic-key", bytes.NewReader(priv)))
	if w.Code != http.StatusOK {
		t.Fatalf("load: got %d, want 200 (body=%s)", w.Code, w.Body)
	}
	var st forensicKeyStatus
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode load: %v", err)
	}
	if !st.Loaded || st.Fingerprint != fp {
		t.Fatalf("load status: loaded=%v fingerprint=%q want loaded=true fingerprint=%q", st.Loaded, st.Fingerprint, fp)
	}

	// Status reflects the loaded key.
	w = httptest.NewRecorder()
	h.ServeHTTP(w, localReq("GET", "/api/forensic-key", nil))
	st = forensicKeyStatus{}
	json.Unmarshal(w.Body.Bytes(), &st)
	if !st.Loaded || st.Fingerprint != fp || !st.Available {
		t.Fatalf("status after load: %+v", st)
	}

	// Clear.
	w = httptest.NewRecorder()
	h.ServeHTTP(w, localReq("DELETE", "/api/forensic-key", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("clear: got %d", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, localReq("GET", "/api/forensic-key", nil))
	st = forensicKeyStatus{}
	json.Unmarshal(w.Body.Bytes(), &st)
	if st.Loaded || st.Fingerprint != "" {
		t.Fatalf("status after clear: %+v", st)
	}
}

func TestForensicKeyLoadRejectsBadKey(t *testing.T) {
	srv := seedReceipts(t, Config{Host: "127.0.0.1"})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, localReq("POST", "/api/forensic-key", bytes.NewReader([]byte("nonsense"))))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", w.Code)
	}
}

func TestDisclosureDecryptsWithMatchingKey(t *testing.T) {
	priv, pub, fp := forensicKeyPair(t)
	params := map[string]any{"command": "rm -rf /tmp/scratch", "cwd": "/home/op"}
	srv := seedReceipts(t, Config{Host: "127.0.0.1"}, encReceipt(t, "urn:receipt:enc1", pub, fp, params))
	h := srv.Handler()

	// Load the key, then decrypt.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, localReq("POST", "/api/forensic-key", bytes.NewReader(priv)))
	if w.Code != http.StatusOK {
		t.Fatalf("load: %d", w.Code)
	}

	w = httptest.NewRecorder()
	req := localReq("GET", "/api/disclosure/"+"urn:receipt:enc1", nil)
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("disclosure: %d", w.Code)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	resp := decodeDisclosure(t, w.Body.Bytes())
	if resp.State != "decrypted" {
		t.Fatalf("state = %q, want decrypted", resp.State)
	}
	if resp.Parameters["command"] != "rm -rf /tmp/scratch" || resp.Parameters["cwd"] != "/home/op" {
		t.Fatalf("decrypted params mismatch: %+v", resp.Parameters)
	}
}

// A correct key must decrypt even when the envelope's kid is written in a
// non-canonical form (e.g. a did:key URL) that does not equal our sha256
// fingerprint — the kid is non-authoritative and DecryptDisclosure ignores it.
func TestDisclosureDecryptsWithNonCanonicalKid(t *testing.T) {
	priv, pub, _ := forensicKeyPair(t)
	params := map[string]any{"command": "whoami"}
	// Encrypt to the right public key but stamp a did:key-style kid.
	r := encReceipt(t, "urn:receipt:enc1", pub, "did:key:z6MkExampleForensicRecipientKeyId", params)
	srv := seedReceipts(t, Config{Host: "127.0.0.1"}, r)
	h := srv.Handler()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, localReq("POST", "/api/forensic-key", bytes.NewReader(priv)))
	if w.Code != http.StatusOK {
		t.Fatalf("load: %d", w.Code)
	}

	w = httptest.NewRecorder()
	h.ServeHTTP(w, localReq("GET", "/api/disclosure/urn:receipt:enc1", nil))
	resp := decodeDisclosure(t, w.Body.Bytes())
	if resp.State != "decrypted" {
		t.Fatalf("state = %q, want decrypted (kid form must not block a correct key)", resp.State)
	}
	if resp.Parameters["command"] != "whoami" {
		t.Fatalf("decrypted params mismatch: %+v", resp.Parameters)
	}
}

func TestDisclosureLockedWithoutKey(t *testing.T) {
	_, pub, fp := forensicKeyPair(t)
	srv := seedReceipts(t, Config{Host: "127.0.0.1"}, encReceipt(t, "urn:receipt:enc1", pub, fp, map[string]any{"command": "ls"}))

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, localReq("GET", "/api/disclosure/urn:receipt:enc1", nil))
	resp := decodeDisclosure(t, w.Body.Bytes())
	if resp.State != "locked" {
		t.Fatalf("state = %q, want locked", resp.State)
	}
	if len(resp.Kids) != 1 || resp.Kids[0] != fp {
		t.Fatalf("kids = %v, want [%s]", resp.Kids, fp)
	}
}

func TestDisclosureMismatchWithWrongKey(t *testing.T) {
	_, pub, fp := forensicKeyPair(t)
	wrongPriv, _, _ := forensicKeyPair(t)
	srv := seedReceipts(t, Config{Host: "127.0.0.1"}, encReceipt(t, "urn:receipt:enc1", pub, fp, map[string]any{"command": "ls"}))
	h := srv.Handler()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, localReq("POST", "/api/forensic-key", bytes.NewReader(wrongPriv)))
	if w.Code != http.StatusOK {
		t.Fatalf("load: %d", w.Code)
	}

	w = httptest.NewRecorder()
	h.ServeHTTP(w, localReq("GET", "/api/disclosure/urn:receipt:enc1", nil))
	resp := decodeDisclosure(t, w.Body.Bytes())
	if resp.State != "mismatch" {
		t.Fatalf("state = %q, want mismatch", resp.State)
	}
}

func TestDisclosureFailedWithTamperedCiphertext(t *testing.T) {
	priv, pub, fp := forensicKeyPair(t)
	r := encReceipt(t, "urn:receipt:enc1", pub, fp, map[string]any{"command": "ls"})

	// Corrupt the AEAD ciphertext while keeping it valid base64url, so the
	// envelope still parses but the AEAD open fails.
	env := r.CredentialSubject.Action.ParametersDisclosure
	ctBytes, err := base64.RawURLEncoding.DecodeString(env.CT)
	if err != nil {
		t.Fatalf("decode ct: %v", err)
	}
	ctBytes[0] ^= 0xff
	env.CT = base64.RawURLEncoding.EncodeToString(ctBytes)

	srv := seedReceipts(t, Config{Host: "127.0.0.1"}, r)
	h := srv.Handler()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, localReq("POST", "/api/forensic-key", bytes.NewReader(priv)))
	if w.Code != http.StatusOK {
		t.Fatalf("load: %d", w.Code)
	}

	w = httptest.NewRecorder()
	h.ServeHTTP(w, localReq("GET", "/api/disclosure/urn:receipt:enc1", nil))
	resp := decodeDisclosure(t, w.Body.Bytes())
	if resp.State != "failed" {
		t.Fatalf("state = %q, want failed", resp.State)
	}
}

func TestDisclosureNoneForPlainReceipt(t *testing.T) {
	// A receipt with no parameters_disclosure reports state "none".
	r := makeReceipt("urn:receipt:plain1", "chain-plain", 1, "filesystem.file.read", receipt.RiskLow, receipt.StatusSuccess, "2026-05-01T10:00:00Z", nil)
	srv := seedReceipts(t, Config{Host: "127.0.0.1"}, r)

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, localReq("GET", "/api/disclosure/urn:receipt:plain1", nil))
	resp := decodeDisclosure(t, w.Body.Bytes())
	if resp.State != "none" {
		t.Fatalf("state = %q, want none", resp.State)
	}
}

func TestForensicDisabledOnNonLoopbackBind(t *testing.T) {
	priv, _, _ := forensicKeyPair(t)
	srv := seedReceipts(t, Config{Host: "0.0.0.0"})
	h := srv.Handler()

	// Status reports unavailable.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, localReq("GET", "/api/forensic-key", nil))
	var st forensicKeyStatus
	json.Unmarshal(w.Body.Bytes(), &st)
	if st.Available {
		t.Fatalf("expected Available=false on 0.0.0.0 bind")
	}

	// Loading a key is refused.
	w = httptest.NewRecorder()
	h.ServeHTTP(w, localReq("POST", "/api/forensic-key", bytes.NewReader(priv)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("load on non-loopback: got %d, want 403", w.Code)
	}
}

// An empty bind host binds all interfaces, so it must NOT count as loopback —
// otherwise the forensic key would be loadable over the network (regression
// guard for the "" fail-open).
func TestForensicDisabledOnEmptyHostBind(t *testing.T) {
	priv, _, _ := forensicKeyPair(t)
	srv := seedReceipts(t, Config{Host: ""})

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, localReq("GET", "/api/forensic-key", nil))
	var st forensicKeyStatus
	json.Unmarshal(w.Body.Bytes(), &st)
	if st.Available {
		t.Fatalf("expected Available=false on empty (all-interfaces) bind")
	}

	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, localReq("POST", "/api/forensic-key", bytes.NewReader(priv)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("load on empty bind: got %d, want 403", w.Code)
	}
}

// Requests whose Host header is not loopback are rejected even when the bind is
// loopback — a DNS-rebinding guard. Covers all forensic endpoints.
func TestForensicRejectsNonLocalHost(t *testing.T) {
	priv, pub, fp := forensicKeyPair(t)
	srv := seedReceipts(t, Config{Host: "127.0.0.1"}, encReceipt(t, "urn:receipt:enc1", pub, fp, map[string]any{"command": "ls"}))
	h := srv.Handler()

	// Load a key via a legitimate local request first.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, localReq("POST", "/api/forensic-key", bytes.NewReader(priv)))
	if w.Code != http.StatusOK {
		t.Fatalf("local load: %d", w.Code)
	}

	rebind := func(method, target string, body io.Reader) *http.Request {
		r := httptest.NewRequest(method, target, body)
		r.Host = "evil.example.com" // attacker domain rebound to 127.0.0.1
		return r
	}
	pathBody, _ := json.Marshal(map[string]string{"path": "/some/key"})
	endpoints := []struct {
		method, target string
		body           io.Reader
	}{
		{"GET", "/api/forensic-key", nil},
		{"POST", "/api/forensic-key", bytes.NewReader(priv)},
		{"POST", "/api/forensic-key/path", bytes.NewReader(pathBody)},
		{"DELETE", "/api/forensic-key", nil},
		{"GET", "/api/disclosure/urn:receipt:enc1", nil},
	}
	for _, ep := range endpoints {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, rebind(ep.method, ep.target, ep.body))
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s %s with foreign Host: got %d, want 403", ep.method, ep.target, w.Code)
		}
	}

	// The key was not cleared by the rejected DELETE, and a local request still
	// decrypts — i.e. the foreign-Host requests had no effect on state.
	w = httptest.NewRecorder()
	h.ServeHTTP(w, localReq("GET", "/api/disclosure/urn:receipt:enc1", nil))
	resp := decodeDisclosure(t, w.Body.Bytes())
	if resp.State != "decrypted" {
		t.Fatalf("after rejected rebind requests, state = %q, want decrypted", resp.State)
	}
}

func TestForensicKeyLoadPath(t *testing.T) {
	priv, _, fp := forensicKeyPair(t)

	// Write the raw private key to a temp file.
	keyFile := t.TempDir() + "/forensic.key"
	if err := os.WriteFile(keyFile, priv, 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}

	srv := seedReceipts(t, Config{Host: "127.0.0.1"})
	h := srv.Handler()

	body, _ := json.Marshal(map[string]string{"path": keyFile})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, localJSONReq("POST", "/api/forensic-key/path", bytes.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("load from path: got %d, want 200 (body=%s)", w.Code, w.Body)
	}

	var st forensicKeyStatus
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !st.Loaded || st.Fingerprint != fp {
		t.Fatalf("status: loaded=%v fingerprint=%q want loaded=true fingerprint=%q", st.Loaded, st.Fingerprint, fp)
	}
}

func TestForensicKeyLoadPathNotFound(t *testing.T) {
	srv := seedReceipts(t, Config{Host: "127.0.0.1"})
	body, _ := json.Marshal(map[string]string{"path": "/nonexistent/forensic.key"})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, localJSONReq("POST", "/api/forensic-key/path", bytes.NewReader(body)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", w.Code)
	}
}

func TestForensicKeyLoadPathInvalidKey(t *testing.T) {
	keyFile := t.TempDir() + "/bad.key"
	if err := os.WriteFile(keyFile, []byte("not a valid key"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	srv := seedReceipts(t, Config{Host: "127.0.0.1"})
	body, _ := json.Marshal(map[string]string{"path": keyFile})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, localJSONReq("POST", "/api/forensic-key/path", bytes.NewReader(body)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", w.Code)
	}
}

// The Content-Type check is the CSRF guard: a cross-origin POST without
// Content-Type: application/json is "CORS-simple" and would skip preflight,
// letting a hostile page trigger arbitrary file reads on the loopback API.
// Reject anything that isn't an explicit application/json content type.
func TestForensicKeyLoadPathRequiresJSONContentType(t *testing.T) {
	srv := seedReceipts(t, Config{Host: "127.0.0.1"})
	body := []byte(`{"path":"/some/key"}`)

	cases := []struct {
		name        string
		contentType string
	}{
		{"missing", ""},
		{"text/plain", "text/plain"},
		{"form", "application/x-www-form-urlencoded"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := localReq("POST", "/api/forensic-key/path", bytes.NewReader(body))
			if tc.contentType != "" {
				r.Header.Set("Content-Type", tc.contentType)
			}
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, r)
			if w.Code != http.StatusUnsupportedMediaType {
				t.Fatalf("Content-Type %q: got %d, want 415", tc.contentType, w.Code)
			}
		})
	}

	// Sanity-check: the helper that adds Content-Type: application/json reaches
	// the path-validation branch (400) rather than being bounced at 415.
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, localJSONReq("POST", "/api/forensic-key/path", bytes.NewReader(body)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("with application/json: got %d, want 400 (file not found)", w.Code)
	}
}

func TestForensicKeyLoadPathRejectsNonLocal(t *testing.T) {
	srv := seedReceipts(t, Config{Host: "127.0.0.1"})
	body, _ := json.Marshal(map[string]string{"path": "/some/key"})
	r := httptest.NewRequest("POST", "/api/forensic-key/path", bytes.NewReader(body))
	r.Host = "evil.example.com"
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("got %d, want 403", w.Code)
	}
}

func TestForensicKeyLoadPathRejectsNonLoopbackBind(t *testing.T) {
	srv := seedReceipts(t, Config{Host: "0.0.0.0"})
	body, _ := json.Marshal(map[string]string{"path": "/some/key"})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, localJSONReq("POST", "/api/forensic-key/path", bytes.NewReader(body)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("got %d, want 403", w.Code)
	}
}

func TestForensicStatusIncludesDefaultPath(t *testing.T) {
	srv := seedReceipts(t, Config{Host: "127.0.0.1", ForensicKeyPath: "/some/forensic.key"})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, localReq("GET", "/api/forensic-key", nil))
	var st forensicKeyStatus
	json.Unmarshal(w.Body.Bytes(), &st)
	if st.DefaultPath != "/some/forensic.key" {
		t.Fatalf("default_path = %q, want /some/forensic.key", st.DefaultPath)
	}
}

func TestForensicAutoLoadAtStartup(t *testing.T) {
	priv, _, fp := forensicKeyPair(t)

	keyFile := t.TempDir() + "/forensic.key"
	if err := os.WriteFile(keyFile, priv, 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}

	// Server with a ForensicKeyPath pointing at our temp file should auto-load.
	srv := seedReceipts(t, Config{Host: "127.0.0.1", ForensicKeyPath: keyFile})

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, localReq("GET", "/api/forensic-key", nil))
	var st forensicKeyStatus
	json.Unmarshal(w.Body.Bytes(), &st)
	if !st.Loaded || st.Fingerprint != fp {
		t.Fatalf("auto-load: loaded=%v fingerprint=%q want loaded=true fingerprint=%q", st.Loaded, st.Fingerprint, fp)
	}
}

func TestForensicAutoLoadSkipsNonLoopback(t *testing.T) {
	priv, _, _ := forensicKeyPair(t)

	keyFile := t.TempDir() + "/forensic.key"
	if err := os.WriteFile(keyFile, priv, 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}

	// A non-loopback bind must not auto-load even when the key file exists.
	srv := seedReceipts(t, Config{Host: "0.0.0.0", ForensicKeyPath: keyFile})

	loaded, _ := srv.forensic.status()
	if loaded {
		t.Fatal("key was auto-loaded on non-loopback bind, should have been skipped")
	}
}
