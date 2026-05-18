// Command dashboard serves a read-only web UI for browsing Agent Receipt SQLite databases.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"

	"github.com/agent-receipts/dashboard/internal/server"
	"github.com/agent-receipts/dashboard/internal/store"
)

// pollIntervalEnv is the environment variable used to override the default
// live-polling cadence. Parsed as a Go time.Duration.
const pollIntervalEnv = "AR_DASHBOARD_POLL_INTERVAL"

// version is set at build time via -ldflags "-X main.version=vX.Y.Z".
// Falls back to the module version from Go's build info (set automatically
// for binaries installed with `go install`), then to "dev".
var version string

// userHomeDir is overridable in tests.
var userHomeDir = os.UserHomeDir

func resolveVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

// xdgDataHome returns the XDG data home directory. It honours $XDG_DATA_HOME
// when set to an absolute path, and falls back to ~/.local/share.
func xdgDataHome() string {
	if dataHome := os.Getenv("XDG_DATA_HOME"); dataHome != "" && filepath.IsAbs(dataHome) {
		return dataHome
	}
	home, err := userHomeDir()
	if err != nil || home == "" || !filepath.IsAbs(home) {
		return ""
	}
	return filepath.Join(home, ".local", "share")
}

// resolvePollInterval returns the polling cadence from the environment if set,
// otherwise the server default. An empty env var is treated as unset.
func resolvePollInterval() (time.Duration, error) {
	v := os.Getenv(pollIntervalEnv)
	if v == "" {
		return server.DefaultPollInterval, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("must be positive, got %s", d)
	}
	return d, nil
}

// defaultDBPath returns the conventional ~/.local/share/agent-receipts/receipts.db
// shared by mcp-proxy, daemon, and hook. Returns "" if the data home directory
// cannot be resolved; main surfaces a clear error in that case.
func defaultDBPath() string {
	dh := xdgDataHome()
	if dh == "" {
		return ""
	}
	return filepath.Join(dh, "agent-receipts", "receipts.db")
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-version", "--version":
			fmt.Printf("dashboard %s\n", resolveVersion())
			return
		}
	}

	defaultDB := defaultDBPath()
	defaultPoll, err := resolvePollInterval()
	if err != nil {
		log.Fatalf("invalid %s: %v", pollIntervalEnv, err)
	}
	dbPath := flag.String("db", defaultDB, "path to receipts SQLite database")
	host := flag.String("host", "127.0.0.1", "address to bind to (use 0.0.0.0 for all interfaces)")
	port := flag.Int("port", 8080, "HTTP server port")
	pollInterval := flag.Duration("poll-interval", defaultPoll, "interval between live receipt polls (e.g. 5s)")
	flag.Parse()

	if *pollInterval <= 0 {
		log.Fatalf("poll-interval must be positive, got %s", *pollInterval)
	}

	dbExplicit := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "db" {
			dbExplicit = true
		}
	})

	if *dbPath == "" {
		if defaultDB == "" {
			fmt.Fprintln(os.Stderr, "dashboard: cannot resolve home directory; pass -db <path/to/receipts.db>")
		} else {
			fmt.Fprintln(os.Stderr, "dashboard: -db cannot be empty")
		}
		os.Exit(1)
	}

	reader, err := store.OpenReadOnly(*dbPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) && !dbExplicit {
			log.Fatalf("no receipts database at default path %s\n\nPass -db <path/to/receipts.db>, or run mcp-proxy / an Agent Receipts SDK to create one.", defaultDB)
		}
		log.Fatalf("open database: %v", err)
	}
	defer reader.Close()

	srv := server.New(reader, server.Config{PollInterval: *pollInterval})
	addr := fmt.Sprintf("%s:%d", *host, *port)
	log.Printf("dashboard listening on http://%s", addr)
	log.Printf("reading from %s (read-only)", *dbPath)
	log.Printf("polling for new receipts every %s", *pollInterval)

	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		log.Fatalf("server: %v", err)
	}
}
