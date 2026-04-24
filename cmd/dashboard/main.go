// Command dashboard serves a read-only web UI for browsing Agent Receipt SQLite databases.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime/debug"

	"github.com/agent-receipts/dashboard/internal/server"
	"github.com/agent-receipts/dashboard/internal/store"
)

// version is set at build time via -ldflags "-X main.version=vX.Y.Z".
// Falls back to the module version from Go's build info (set automatically
// for binaries installed with `go install`), then to "dev".
var version string

func resolveVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-version", "--version":
			fmt.Printf("dashboard %s\n", resolveVersion())
			return
		}
	}

	dbPath := flag.String("db", "", "path to receipts SQLite database")
	host := flag.String("host", "127.0.0.1", "address to bind to (use 0.0.0.0 for all interfaces)")
	port := flag.Int("port", 8080, "HTTP server port")
	flag.Parse()

	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, "usage: dashboard -db <path/to/receipts.db>")
		os.Exit(1)
	}

	reader, err := store.OpenReadOnly(*dbPath)
	if err != nil {
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
