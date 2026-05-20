// Package server provides the HTTP server for the dashboard.
package server

import (
	"crypto/ed25519"
	"crypto/x509"
	"embed"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log"
	"net/http"
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
}

//go:embed static
var staticFS embed.FS

// Server is the dashboard HTTP server.
type Server struct {
	reader *store.Reader
	cfg    Config
}

// New creates a new Server backed by the given reader. A zero PollInterval
// in cfg falls back to DefaultPollInterval.
func New(reader *store.Reader, cfg Config) *Server {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = DefaultPollInterval
	}
	return &Server{reader: reader, cfg: cfg}
}

// Handler returns the HTTP handler with all routes registered.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// API endpoints (JSON).
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/config", s.handleConfig)
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("GET /api/receipts", s.handleReceipts)
	mux.HandleFunc("GET /api/receipts/{id...}", s.handleReceiptDetail)
	mux.HandleFunc("GET /api/chains", s.handleChains)
	mux.HandleFunc("GET /api/chains/{chainID}/verify", s.handleChainVerify)

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
	stats, err := s.reader.Stats()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stats query failed")
		log.Printf("stats error: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
