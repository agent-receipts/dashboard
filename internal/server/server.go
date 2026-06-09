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
	"time"

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
}

//go:embed static
var staticFS embed.FS

// Server is the dashboard HTTP server.
type Server struct {
	reader   *store.Reader
	cfg      Config
	forensic *forensicKeyStore
}

// New creates a new Server backed by the given reader. A zero PollInterval
// in cfg falls back to DefaultPollInterval. When cfg.ForensicKeyPath names an
// existing file and the server is bound to loopback, the forensic key is
// loaded automatically so solo operators need no manual UI step.
func New(reader *store.Reader, cfg Config) *Server {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = DefaultPollInterval
	}
	s := &Server{reader: reader, cfg: cfg, forensic: &forensicKeyStore{}}
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
	mux.HandleFunc("GET /api/receipts", s.handleReceipts)
	mux.HandleFunc("GET /api/receipts/{id...}", s.handleReceiptDetail)
	mux.HandleFunc("GET /api/chains", s.handleChains)
	mux.HandleFunc("GET /api/chains/{chainID}/verify", s.handleChainVerify)

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
		d, err := time.ParseDuration(rangeStr)
		if err != nil || d <= 0 {
			writeError(w, http.StatusBadRequest, "range must be a positive Go duration (e.g. 24h)")
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

func (s *Server) handleServerStats(w http.ResponseWriter, r *http.Request) {
	var since *string
	if rangeStr := r.URL.Query().Get("range"); rangeStr != "" {
		d, err := time.ParseDuration(rangeStr)
		if err != nil || d <= 0 {
			writeError(w, http.StatusBadRequest, "range must be a positive Go duration (e.g. 24h)")
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
	if v := q.Get("session_id"); v != "" {
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
	writeJSON(w, http.StatusOK, ar)
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

// readFileLimited reads up to limit+1 bytes from path. Returns an error if the
// file exceeds limit, so callers can reject oversized inputs without reading
// the whole file into memory first.
func readFileLimited(path string, limit int64) ([]byte, error) {
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
		return nil, fmt.Errorf("file exceeds %d byte limit", limit)
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
