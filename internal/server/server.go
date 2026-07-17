// Package server provides the HTTP server for the dashboard.
package server

import (
	"crypto/ed25519"
	"crypto/x509"
	"embed"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"obsigna.dev/sdk/go/receipt"
	"obsigna.dev/sdk/go/taxonomy"
	"github.com/agent-receipts/dashboard/internal/enrich"
	"github.com/agent-receipts/dashboard/internal/store"
	"github.com/agent-receipts/dashboard/internal/verify"
)

// DefaultPollInterval is the polling cadence used when none is configured.
const DefaultPollInterval = 5 * time.Second

// Config controls server behaviour exposed to the frontend.
type Config struct {
	// PollInterval is how often the dashboard polls /api/receipts for new rows.
	PollInterval time.Duration
	// DBPath is the path to the SQLite database being served, surfaced in the
	// header so users know which store they are looking at when running
	// multiple dashboards or pointing at a non-default file. Callers should
	// resolve this to an absolute path (e.g. via filepath.Abs) before passing
	// it in; the server uses it verbatim for display only.
	DBPath string
	// Version is the dashboard build version, shown in the header footer.
	Version string
	// Host is the address the server is bound to. It gates forensic key
	// operations: the loaded private key can decrypt every matching
	// disclosure, so a key is only accepted when the bind is loopback (see
	// forensicAvailable). An empty or all-interfaces bind (""/"0.0.0.0"/"::")
	// is not loopback and disables forensic operations.
	Host string
	// ForensicKeyPath is the default path probed at startup for an X25519
	// forensic private key. When non-empty and the file exists, the server
	// loads it automatically so operators with a single-user install do not
	// need to paste the key into the UI.
	ForensicKeyPath string
	// ForensicKeyDirs lists extra directories, in addition to the user's home
	// directory, from which the forensic key path endpoint may load a key.
	ForensicKeyDirs []string
	// Experimental enables experimental features. When false, experimental
	// API endpoints respond with 404.
	Experimental bool
}

//go:embed static
var staticFS embed.FS

// Server is the dashboard HTTP server.
type Server struct {
	reader          *store.Reader
	cfg             Config
	forensic        *forensicKeyStore
	forensicKeyDirs []string // cleaned, absolute allowed roots for the key path endpoint
	// enricher resolves display-only, unverified local session data joined to a
	// receipt by session id. It never touches the receipt or the store; see
	// docs/adr/0002-local-session-enrichment.md.
	enricher enrich.SessionEnricher
}

// New creates a new Server backed by the given reader. A zero PollInterval
// in cfg falls back to DefaultPollInterval. When cfg.ForensicKeyPath names an
// existing file and the server is bound to loopback, the forensic key is
// loaded automatically so solo operators need no manual UI step.
//
// The allowed roots for the forensic key path endpoint are computed once here:
// the user's home directory (silently omitted if unavailable) plus any
// absolute paths in cfg.ForensicKeyDirs (non-absolute entries are skipped).
func New(reader *store.Reader, cfg Config) *Server {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = DefaultPollInterval
	}

	var dirs []string
	if home, err := os.UserHomeDir(); err == nil {
		if cleaned := filepath.Clean(home); filepath.IsAbs(cleaned) {
			dirs = append(dirs, cleaned)
		}
	}
	for _, d := range cfg.ForensicKeyDirs {
		cleaned := filepath.Clean(d)
		if filepath.IsAbs(cleaned) {
			dirs = append(dirs, cleaned)
		}
	}

	s := &Server{
		reader:          reader,
		cfg:             cfg,
		forensic:        &forensicKeyStore{},
		forensicKeyDirs: dirs,
		enricher:        enrich.New(),
	}
	s.tryLoadDefaultForensicKey()
	return s
}

// tryLoadDefaultForensicKey probes cfg.ForensicKeyPath at startup and silently
// loads the key if found. A missing file is a normal condition; any other error
// or parse failure is logged but never fatal.
func (s *Server) tryLoadDefaultForensicKey() {
	if s.cfg.ForensicKeyPath == "" || !s.forensicAvailable() {
		return
	}
	data, err := readFileLimited(s.cfg.ForensicKeyPath, maxForensicKeyBody)
	if err != nil {
		if !isNotExist(err) {
			log.Printf("forensic key: could not read %s: %v", s.cfg.ForensicKeyPath, err)
		}
		return
	}
	defer zero(data)

	priv, err := parseForensicPrivateKey(data)
	if err != nil {
		log.Printf("forensic key: could not parse %s: %v", s.cfg.ForensicKeyPath, err)
		return
	}
	defer zero(priv)

	fp, err := s.forensic.load(priv)
	if err != nil {
		log.Printf("forensic key: load failed for %s: %v", s.cfg.ForensicKeyPath, err)
		return
	}
	log.Printf("forensic key loaded from %s (fingerprint %s)", s.cfg.ForensicKeyPath, fp)
}

// Handler returns the HTTP handler with all routes registered.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// API endpoints (JSON).
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/config", s.handleConfig)
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("GET /api/stats/timeseries", s.handleTimeseriesStats)
	mux.HandleFunc("GET /api/stats/actions", s.handleActionStats)
	mux.HandleFunc("GET /api/stats/servers", s.handleServerStats)
	mux.HandleFunc("GET /api/taxonomy", s.handleTaxonomy)
	mux.HandleFunc("GET /api/sessions", s.handleSessions)
	mux.HandleFunc("GET /api/sessions/{sessionID}/attribution", s.handleSessionAttribution)
	mux.HandleFunc("GET /api/sessions/{sessionID}/enrichment", s.handleSessionEnrichment)
	mux.HandleFunc("GET /api/fleet/attribution", s.handleFleetAttribution)
	mux.HandleFunc("GET /api/receipts", s.handleReceipts)
	mux.HandleFunc("GET /api/receipts/{id...}", s.handleReceiptDetail)
	mux.HandleFunc("GET /api/chains", s.handleChains)
	mux.HandleFunc("GET /api/chains/{chainID}/verify", s.handleChainVerify)

	// Experimental endpoints — gated by cfg.Experimental; return 404 when disabled.
	mux.HandleFunc("GET /api/fleet/signatures", s.handleFleetSignatures)

	// Forensic disclosure: load/clear the operator's X25519 private key (held
	// in memory only) and decrypt a receipt's parameters_disclosure envelope
	// inline. The decrypt route lives under its own prefix because the receipt
	// detail route uses a trailing {id...} wildcard, which cannot carry a
	// further path segment.
	mux.HandleFunc("GET /api/forensic-key", s.handleForensicKeyGet)
	mux.HandleFunc("POST /api/forensic-key", s.handleForensicKeyLoad)
	mux.HandleFunc("POST /api/forensic-key/path", s.handleForensicKeyLoadPath)
	mux.HandleFunc("DELETE /api/forensic-key", s.handleForensicKeyClear)
	mux.HandleFunc("GET /api/disclosure/{id...}", s.handleReceiptDisclosure)

	// Static files + index.
	mux.HandleFunc("GET /", s.handleIndex)

	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	// server_time gives the frontend an authoritative watermark for live polling
	// when the store is empty at load — relying on the client's wall clock would
	// silently drop receipts whenever the two clocks disagree.
	// filepath.Base("") returns ".", which would surface in the header as a
	// misleading file name; guard so an unset DBPath stays an empty string.
	dbName := ""
	if s.cfg.DBPath != "" {
		dbName = filepath.Base(s.cfg.DBPath)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"poll_interval_ms": s.cfg.PollInterval.Milliseconds(),
		"server_time":      time.Now().UTC().Format(time.RFC3339),
		"db_path":          s.cfg.DBPath,
		"db_name":          dbName,
		"version":          s.cfg.Version,
		"experimental":     s.cfg.Experimental,
	})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var after, before *string
	if v := q.Get("after"); v != "" {
		after = &v
	}
	if v := q.Get("before"); v != "" {
		before = &v
	}
	stats, err := s.reader.Stats(after, before)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stats query failed")
		log.Printf("stats error: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// parseFlexibleDuration parses a Go duration, additionally accepting an `Nd`
// (days) suffix — e.g. "7d", "30d" — which time.ParseDuration rejects. This
// matches the day-shorthand shown in the timeseries API examples.
func parseFlexibleDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

// formatBucketDuration renders a bucket duration without the redundant zero
// units time.Duration.String() emits (e.g. "1h" not "1h0m0s", "1d" not "24h0m0s").
func formatBucketDuration(d time.Duration) string {
	switch {
	case d <= 0:
		return "0s"
	case d%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	case d%time.Second == 0:
		return fmt.Sprintf("%ds", d/time.Second)
	default:
		return d.String()
	}
}

// autoBucket selects a sensible bucket duration for the given window duration.
// Thresholds: ≤2h → 5m, ≤24h → 1h, ≤7d → 6h, else → 1d.
func autoBucket(window time.Duration) time.Duration {
	switch {
	case window <= 2*time.Hour:
		return 5 * time.Minute
	case window <= 24*time.Hour:
		return time.Hour
	case window <= 7*24*time.Hour:
		return 6 * time.Hour
	default:
		return 24 * time.Hour
	}
}

func (s *Server) handleTimeseriesStats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	now := time.Now().UTC()

	var from, to time.Time
	to = now // default

	rangeStr := q.Get("range")
	fromStr := q.Get("from")
	toStr := q.Get("to")

	if rangeStr != "" {
		// range= takes precedence over from=/to=.
		d, err := parseFlexibleDuration(rangeStr)
		if err != nil || d <= 0 {
			writeError(w, http.StatusBadRequest, "range must be a positive duration (e.g. 24h, 7d)")
			return
		}
		from = now.Add(-d)
	} else {
		// from and to are honoured independently: `to=` alone bounds the upper
		// edge while from stays zero (earliest receipt); `from=` alone runs to now.
		if fromStr != "" {
			t, err := time.Parse(time.RFC3339, fromStr)
			if err != nil {
				writeError(w, http.StatusBadRequest, "from must be an ISO-8601 / RFC3339 timestamp")
				return
			}
			from = t
		}
		if toStr != "" {
			t2, err := time.Parse(time.RFC3339, toStr)
			if err != nil {
				writeError(w, http.StatusBadRequest, "to must be an ISO-8601 / RFC3339 timestamp")
				return
			}
			to = t2
		}
	}
	// If neither range nor from is given, from is the zero Time — TimeseriesStats
	// will resolve it to the earliest receipt timestamp (all-time).

	// Validate from < to when from is non-zero.
	if !from.IsZero() && !from.Before(to) {
		writeError(w, http.StatusBadRequest, "from must be before to")
		return
	}

	// Resolve bucket duration.
	var bucket time.Duration
	if bucketStr := q.Get("bucket"); bucketStr != "" {
		d, err := parseFlexibleDuration(bucketStr)
		if err != nil || d <= 0 {
			writeError(w, http.StatusBadRequest, "bucket must be a positive duration (e.g. 1h, 1d)")
			return
		}
		bucket = d
	} else {
		var window time.Duration
		if !from.IsZero() {
			window = to.Sub(from)
		} else {
			// All-time: default to 1d bucket.
			window = 8 * 24 * time.Hour
		}
		bucket = autoBucket(window)
	}

	buckets, err := s.reader.TimeseriesStats(from, to, bucket)
	if err != nil {
		// Surface the bucket-count guard as a 400.
		writeError(w, http.StatusBadRequest, err.Error())
		log.Printf("timeseries stats error: %v", err)
		return
	}

	rangeFrom := from.UTC().Format(time.RFC3339)
	if from.IsZero() && len(buckets) > 0 {
		rangeFrom = buckets[0].Ts
	} else if from.IsZero() {
		rangeFrom = ""
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"buckets":         buckets,
		"bucket_duration": formatBucketDuration(bucket),
		"range_from":      rangeFrom,
		"range_to":        to.UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleActionStats(w http.ResponseWriter, r *http.Request) {
	var since *string
	if rangeStr := r.URL.Query().Get("range"); rangeStr != "" {
		d, err := parseFlexibleDuration(rangeStr)
		if err != nil || d <= 0 {
			writeError(w, http.StatusBadRequest, "range must be a positive duration (e.g. 24h, 7d)")
			return
		}
		t := time.Now().UTC().Add(-d).Format(time.RFC3339)
		since = &t
	}

	stats, err := s.reader.ActionStats(since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "action stats query failed")
		log.Printf("action stats error: %v", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"actions": stats})
}

// taxonomyCategory groups built-in action types under a human-readable heading
// for the reference view.
type taxonomyCategory struct {
	Name    string                     `json:"name"`
	Actions []taxonomy.ActionTypeEntry `json:"actions"`
}

// handleTaxonomy serves the SDK's built-in action-type registry — every known
// action type with its description and default risk level, grouped by category.
// The frontend uses it to render a reference view and to annotate raw action
// types in receipt lists and detail with their meaning. It is static reference
// data (no store access), so it never depends on which receipts exist.
func (s *Server) handleTaxonomy(w http.ResponseWriter, r *http.Request) {
	categories := []taxonomyCategory{
		{Name: "Filesystem", Actions: taxonomy.FilesystemActions},
		{Name: "System", Actions: taxonomy.SystemActions},
		{Name: "Data", Actions: taxonomy.DataActions},
		{Name: "Network", Actions: taxonomy.NetworkActions},
		{Name: "Diagnostic", Actions: taxonomy.DiagnosticActions},
		{Name: "Other", Actions: []taxonomy.ActionTypeEntry{taxonomy.UnknownAction}},
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": categories})
}

func (s *Server) handleServerStats(w http.ResponseWriter, r *http.Request) {
	var since *string
	if rangeStr := r.URL.Query().Get("range"); rangeStr != "" {
		d, err := parseFlexibleDuration(rangeStr)
		if err != nil || d <= 0 {
			writeError(w, http.StatusBadRequest, "range must be a positive duration (e.g. 24h, 7d)")
			return
		}
		t := time.Now().UTC().Add(-d).Format(time.RFC3339)
		since = &t
	}

	stats, err := s.reader.ServerStats(since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server stats query failed")
		log.Printf("server stats error: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"servers": stats})
}

// maxConcurrentSessionEnrichment bounds how many enrichment lookups run at
// once when composing /api/sessions. Unlike /api/fleet/signatures (capped at
// maxLimit sessions), /api/sessions has no limit param and can return every
// session ever seen — an unbounded fan-out over a large history would spike
// file descriptor and memory usage across the whole server for one request,
// since each lookup is a filesystem read/parse of a local session transcript
// (up to 128 MiB).
const maxConcurrentSessionEnrichment = 8

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	var since *string
	if rangeStr := r.URL.Query().Get("range"); rangeStr != "" {
		d, err := parseFlexibleDuration(rangeStr)
		if err != nil || d <= 0 {
			writeError(w, http.StatusBadRequest, "range must be a positive duration (e.g. 24h, 7d)")
			return
		}
		t := time.Now().UTC().Add(-d).Format(time.RFC3339)
		since = &t
	}
	sessions, err := s.reader.SessionStats(since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "session stats query failed")
		log.Printf("session stats error: %v", err)
		return
	}

	// Same composition pattern as handleFleetSignatures: internal/store must
	// not depend on internal/enrich (see ADR-0002), so enrichment is attached
	// here, one goroutine per session, skipped entirely when no enricher is
	// configured. /api/sessions is unbounded (no limit param, unlike Fleet's
	// maxLimit=24), so unlike the Receipts table's per-row client fetches,
	// this list can be long — composing server-side avoids a client fanning
	// out one request per visible session. Concurrent FILE I/O is bounded by
	// the semaphore below (goroutine creation itself is cheap; a large
	// session history all reading/parsing local transcripts at once is not).
	results := make([]sessionRowWithEnrichment, len(sessions))
	for i, sess := range sessions {
		results[i] = sessionRowWithEnrichment{SessionRow: sess}
	}
	if s.enricher != nil {
		var wg sync.WaitGroup
		sem := make(chan struct{}, maxConcurrentSessionEnrichment)
		wg.Add(len(sessions))
		for i, sess := range sessions {
			go func(i int, sessionID string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				results[i].Enrichment = s.enrichSession(sessionID)
			}(i, sess.SessionID)
		}
		wg.Wait()
	}

	writeJSON(w, http.StatusOK, map[string]any{"sessions": results})
}

// sessionRowWithEnrichment pairs a store.SessionRow with its optional
// display-only, unverified local session enrichment. It exists so the
// Sessions tab can render a per-session cost figure without internal/store
// taking a dependency on internal/enrich.
type sessionRowWithEnrichment struct {
	store.SessionRow
	Enrichment *enrich.Enrichment `json:"enrichment,omitempty"`
}

func (s *Server) handleSessionAttribution(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("sessionID"))
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "sessionID is required")
		return
	}
	result, err := s.reader.SessionAttribution(sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "attribution query failed")
		log.Printf("session attribution error: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// enrichSession returns the display-only, unverified local session enrichment
// for a session id, or nil when no enricher is configured. Every call site
// that needs enrichment funnels through here so the nil-enricher guard lives
// in exactly one place. See ADR-0002.
func (s *Server) enrichSession(sessionID string) *enrich.Enrichment {
	if s.enricher == nil {
		return nil
	}
	return s.enricher.Enrich(sessionID)
}

// handleSessionEnrichment returns the display-only, unverified local session
// enrichment for a session id, or null when the enricher is unset or no local
// session data is available. See ADR-0002 and the analogous nil-safety used by
// handleReceiptDetail: enrichment never surfaces as an error, only as absence.
func (s *Server) handleSessionEnrichment(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("sessionID"))
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "sessionID is required")
		return
	}

	writeJSON(w, http.StatusOK, s.enrichSession(sessionID))
}

// defaultFleetSessions is how many recently-active sessions the fleet view
// renders when no limit is given; maxFleetSessions caps it so the combined
// graph stays legible and the IN(...) query bounded.
const (
	defaultFleetSessions = 6
	maxFleetSessions     = 12
)

// handleFleetAttribution returns one combined attribution payload across the N
// most recently-active sessions (by last_seen). State-dependency edges whose
// endpoints span two sessions carry cross_session=true — the fleet collision
// signal. N defaults to defaultFleetSessions and is capped at maxFleetSessions.
func (s *Server) handleFleetAttribution(w http.ResponseWriter, r *http.Request) {
	limit := defaultFleetSessions
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		if n > maxFleetSessions {
			n = maxFleetSessions
		}
		limit = n
	}

	sessions, err := s.reader.SessionStats(nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "session stats query failed")
		log.Printf("fleet attribution session stats error: %v", err)
		return
	}
	if len(sessions) > limit {
		sessions = sessions[:limit]
	}
	ids := make([]string, len(sessions))
	for i, sess := range sessions {
		ids[i] = sess.SessionID
	}

	result, err := s.reader.FleetAttribution(ids)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "fleet attribution query failed")
		log.Printf("fleet attribution error: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleReceipts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.Filter{}

	if v := q.Get("chain_id"); v != "" {
		f.ChainID = &v
	}
	if v := q.Get("action_type"); v != "" {
		f.ActionType = &v
	}
	if v := q.Get("risk_level"); v != "" {
		f.RiskLevel = &v
	}
	if v := q.Get("status"); v != "" {
		f.Status = &v
	}
	if v := q.Get("after"); v != "" {
		f.After = &v
	}
	if v := q.Get("before"); v != "" {
		f.Before = &v
	}
	if v := q.Get("since"); v != "" {
		f.Since = &v
	}
	if v := q.Get("server"); v != "" {
		f.Server = &v
	}
	if v := q.Get("tool_name"); v != "" {
		f.ToolName = &v
	}
	if v := strings.TrimSpace(q.Get("session_id")); v != "" {
		f.SessionID = &v
	}
	if v := q.Get("q"); v != "" {
		f.Q = &v
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		const maxLimit = 10000
		if n > maxLimit {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("limit must not exceed %d", maxLimit))
			return
		}
		f.Limit = &n
	}

	rows, err := s.reader.ListReceipts(f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		log.Printf("receipts error: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) handleReceiptDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// Receipt IDs are always "urn:receipt:<uuid>". The {id...} wildcard
	// captures everything after /api/receipts/, but some clients may
	// URL-encode the colon. The JS frontend uses encodeURIComponent(id)
	// which preserves the "urn:" prefix, so this is a safety fallback
	// rather than the normal path.
	if !strings.HasPrefix(id, "urn:") {
		id = "urn:" + id
	}

	ar, err := s.reader.GetByID(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		log.Printf("receipt detail error: %v", err)
		return
	}
	if ar == nil {
		writeError(w, http.StatusNotFound, "receipt not found")
		return
	}

	// Enrichment is a display-only, unverified sibling of the signed receipt —
	// never merged into it. It is nil when the receipt carries no session id or
	// no local session data is available. See ADR-0002.
	var enrichment *enrich.Enrichment
	if ar.Issuer.SessionID != "" {
		enrichment = s.enrichSession(ar.Issuer.SessionID)
	}
	writeJSON(w, http.StatusOK, receiptDetailResponse{Receipt: ar, Enrichment: enrichment})
}

// receiptDetailResponse is the receipt-detail payload: the signed receipt and,
// alongside it, optional unverified local enrichment. The two are siblings —
// enrichment is never nested inside the receipt or its credentialSubject.
type receiptDetailResponse struct {
	Receipt    *receipt.AgentReceipt `json:"receipt"`
	Enrichment *enrich.Enrichment    `json:"enrichment"`
}

func (s *Server) handleChains(w http.ResponseWriter, r *http.Request) {
	chains, err := s.reader.ListChains()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		log.Printf("chains error: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, chains)
}

func (s *Server) handleChainVerify(w http.ResponseWriter, r *http.Request) {
	chainID := r.PathValue("chainID")
	publicKeyPEM := r.URL.Query().Get("public_key")

	if publicKeyPEM != "" {
		const maxPEMLen = 4096
		if len(publicKeyPEM) > maxPEMLen {
			writeError(w, http.StatusBadRequest, "public_key must be a PEM-encoded Ed25519 public key")
			return
		}
		if err := validateEd25519PEM(publicKeyPEM); err != nil {
			log.Printf("chain verify: invalid public_key: %v", err)
			writeError(w, http.StatusBadRequest, "public_key must be a PEM-encoded Ed25519 public key")
			return
		}
	}

	receipts, err := s.reader.GetChain(chainID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "chain query failed")
		log.Printf("chain verify error: %v", err)
		return
	}

	result := verify.VerifyChainLinks(receipts, publicKeyPEM)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleFleetSignatures(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Experimental {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	const defaultLimit = 12
	const maxLimit = 24

	limit := defaultLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		if n > maxLimit {
			n = maxLimit
		}
		limit = n
	}

	sessions, err := s.reader.SessionStats(nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "session stats query failed")
		log.Printf("fleet signatures: session stats error: %v", err)
		return
	}
	if len(sessions) > limit {
		sessions = sessions[:limit]
	}

	ids := make([]string, len(sessions))
	for i, sess := range sessions {
		ids[i] = sess.SessionID
	}

	sigs, err := s.reader.FleetSignatures(ids)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "fleet signatures query failed")
		log.Printf("fleet signatures error: %v", err)
		return
	}

	// internal/store must not depend on internal/enrich (see ADR-0002), so the
	// per-session enrichment is composed here, at the one layer that already
	// imports both. A session with no local transcript file simply gets a nil
	// Enrichment, which the "omitempty" tag drops from the response entirely.
	results := make([]fleetSignatureWithEnrichment, len(sigs))
	for i, sig := range sigs {
		results[i] = fleetSignatureWithEnrichment{SessionSignature: sig}
	}

	// Enrichment lookups run concurrently: each is a filesystem read/parse of a
	// local session transcript (internal/enrich), so serializing up to maxLimit
	// of them would make one Fleet page load pay for N sequential file scans.
	// Each goroutine only ever writes its own results[i], so no locking is
	// needed beyond the WaitGroup. Skipped entirely when no enricher is
	// configured — the common "no local data" deployment — rather than paying
	// goroutine setup for N calls that would each just return nil.
	if s.enricher != nil {
		var wg sync.WaitGroup
		wg.Add(len(sigs))
		for i, sig := range sigs {
			go func(i int, sessionID string) {
				defer wg.Done()
				results[i].Enrichment = s.enrichSession(sessionID)
			}(i, sig.SessionID)
		}
		wg.Wait()
	}

	writeJSON(w, http.StatusOK, map[string]any{"signatures": results})
}

// fleetSignatureWithEnrichment pairs a store.SessionSignature with its
// optional display-only, unverified local session enrichment. It exists so
// the fleet view can render a per-session cost/token caption without
// internal/store taking a dependency on internal/enrich.
type fleetSignatureWithEnrichment struct {
	store.SessionSignature
	Enrichment *enrich.Enrichment `json:"enrichment,omitempty"`
}

func validateEd25519PEM(pemStr string) error {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return fmt.Errorf("not a valid PEM block")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("parse public key: %w", err)
	}
	if _, ok := key.(ed25519.PublicKey); !ok {
		return fmt.Errorf("not an Ed25519 public key")
	}
	return nil
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	data, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "index not found")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

// errFileTooLarge is returned by readFileLimited when a file exceeds the byte
// limit. Callers can test for it with errors.Is to distinguish an over-size
// file from other read errors and map it to an appropriate HTTP status (413).
var errFileTooLarge = errors.New("file too large")

// errNotRegularFile is returned by readFileLimited when the path is not a
// regular file (symlink, FIFO, device, directory). Callers can test for it with
// errors.Is to surface a precise message instead of a raw read error.
var errNotRegularFile = errors.New("not a regular file")

// readFileLimited reads up to limit+1 bytes from path — one past the limit, so
// an over-size file can be detected without reading it whole — and returns
// errFileTooLarge when the file exceeds limit. Non-regular files (symlinks, FIFOs, devices,
// directories) are rejected via Lstat before opening, so a FIFO at the path
// cannot hang startup and a device cannot be read through the path endpoint.
// A residual Lstat/Open TOCTOU window remains, which is acceptable here: the
// path is operator-supplied on a loopback-only, read-only dashboard.
func readFileLimited(path string, limit int64) ([]byte, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errNotRegularFile
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		zero(data)
		return nil, errFileTooLarge
	}
	return data, nil
}

// isNotExist reports whether err is a file-not-found error from the os package.
func isNotExist(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
