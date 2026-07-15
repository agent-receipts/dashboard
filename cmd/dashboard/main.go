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
	"strings"
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

// choosePollInterval applies the flag > env > default precedence. When the
// flag was set explicitly its value wins outright, so a malformed env var
// can't lock a user out of an otherwise valid configuration.
func choosePollInterval(flagValue time.Duration, flagSet bool, fromEnv func() (time.Duration, error)) (time.Duration, error) {
	if flagSet {
		if flagValue <= 0 {
			return 0, fmt.Errorf("poll-interval must be positive, got %s", flagValue)
		}
		return flagValue, nil
	}
	d, err := fromEnv()
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", pollIntervalEnv, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("poll-interval must be positive, got %s", d)
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

// defaultForensicKeyPath returns the conventional path where the daemon writes
// its X25519 forensic private key. Returns "" if the data home directory cannot
// be resolved.
func defaultForensicKeyPath() string {
	dh := xdgDataHome()
	if dh == "" {
		return ""
	}
	return filepath.Join(dh, "agent-receipts", "forensic.key")
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
	dbPath := flag.String("db", defaultDB, "path to receipts SQLite database")
	host := flag.String("host", "127.0.0.1", "address to bind to (use 0.0.0.0 for all interfaces)")
	port := flag.Int("port", 8080, "HTTP server port")
	pollInterval := flag.Duration("poll-interval", server.DefaultPollInterval, "interval between live receipt polls (e.g. 5s)")
	temporalProximity := flag.Duration("temporal-proximity", store.DefaultTemporalProximity, "max gap between two contending touches of a shared resource for a cross-session collision to count as concurrent (temporal_overlap)")
	forensicKeyDirsFlag := flag.String("forensic-key-dirs", "", "comma-separated list of extra absolute directories from which the forensic key path endpoint may load a key (the user's home directory is always allowed)")
	experimental := flag.Bool("experimental", false, "enable experimental features (e.g. /api/fleet/signatures)")
	flag.Parse()

	if *temporalProximity <= 0 {
		log.Fatalf("temporal-proximity must be positive, got %s", *temporalProximity)
	}

	pollFlagSet := false
	dbExplicit := false
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "poll-interval":
			pollFlagSet = true
		case "db":
			dbExplicit = true
		}
	})
	chosen, err := choosePollInterval(*pollInterval, pollFlagSet, resolvePollInterval)
	if err != nil {
		log.Fatal(err)
	}
	*pollInterval = chosen

	if *dbPath == "" {
		if defaultDB == "" {
			fmt.Fprintln(os.Stderr, "dashboard: cannot resolve home directory; pass -db <path/to/receipts.db>")
		} else {
			fmt.Fprintln(os.Stderr, "dashboard: -db cannot be empty")
		}
		os.Exit(1)
	}

	reader, err := store.OpenReadOnly(*dbPath, store.WithTemporalProximity(*temporalProximity))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) && !dbExplicit {
			log.Fatalf("no receipts database at default path %s\n\nPass -db <path/to/receipts.db>, or run mcp-proxy / an Obsigna SDK to create one.", defaultDB)
		}
		log.Fatalf("open database: %v", err)
	}
	defer reader.Close()

	// Resolve to an absolute path for the header display so users see a
	// stable, fully-qualified location even if -db was passed relatively.
	// Fall back to the raw value if resolution somehow fails; the dashboard
	// still works either way.
	displayDBPath := *dbPath
	if abs, err := filepath.Abs(*dbPath); err == nil {
		displayDBPath = abs
	}
	var forensicKeyDirs []string
	for _, d := range strings.Split(*forensicKeyDirsFlag, ",") {
		if d := strings.TrimSpace(d); d != "" {
			forensicKeyDirs = append(forensicKeyDirs, d)
		}
	}

	srv := server.New(reader, server.Config{
		PollInterval:    *pollInterval,
		DBPath:          displayDBPath,
		Version:         resolveVersion(),
		Host:            *host,
		ForensicKeyPath: defaultForensicKeyPath(),
		ForensicKeyDirs: forensicKeyDirs,
		Experimental:    *experimental,
	})
	addr := fmt.Sprintf("%s:%d", *host, *port)
	log.Printf("dashboard listening on http://%s", addr)
	log.Printf("reading from %s (read-only)", *dbPath)
	log.Printf("polling for new receipts every %s", *pollInterval)

	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		log.Fatalf("server: %v", err)
	}
}
