// Package server provides the HTTP server for the dashboard.
package server

import (
	"embed"
	"encoding/json"
	"log"
	"net/http"
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
	writeJSON(w, http.StatusOK, map[string]any{
		"poll_interval_ms": s.cfg.PollInterval.Milliseconds(),
		"server_time":      time.Now().UTC().Format(time.RFC3339),
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

	receipts, err := s.reader.GetChain(chainID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "chain query failed")
		log.Printf("chain verify error: %v", err)
		return
	}

	result := verify.VerifyChainLinks(receipts)
	writeJSON(w, http.StatusOK, result)
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
