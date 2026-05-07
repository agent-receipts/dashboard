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

	"github.com/agent-receipts/dashboard/internal/server"
	"github.com/agent-receipts/dashboard/internal/store"
)

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

// defaultDBPath returns the conventional `~/.agent-receipts/receipts.db`
// shared by mcp-proxy and the daemon. Returns "" if the home directory
// cannot be resolved to an absolute path; main surfaces a clear error in
// that case.
func defaultDBPath() string {
	home, err := userHomeDir()
	if err != nil || home == "" || !filepath.IsAbs(home) {
		return ""
	}
	return filepath.Join(home, ".agent-receipts", "receipts.db")
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
	flag.Parse()

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

	srv := server.New(reader)
	addr := fmt.Sprintf("%s:%d", *host, *port)
	log.Printf("dashboard listening on http://%s", addr)
	log.Printf("reading from %s (read-only)", *dbPath)

	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		log.Fatalf("server: %v", err)
	}
}
