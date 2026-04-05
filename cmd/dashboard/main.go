// Command dashboard serves a read-only web UI for browsing Agent Receipt SQLite databases.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/agent-receipts/dashboard/internal/server"
	"github.com/agent-receipts/dashboard/internal/store"
)

func main() {
	dbPath := flag.String("db", "", "path to receipts SQLite database")
	port := flag.Int("port", 8080, "HTTP server port")
	flag.Parse()

	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, "usage: dashboard --db <path/to/receipts.db>")
		os.Exit(1)
	}

	reader, err := store.OpenReadOnly(*dbPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer reader.Close()

	srv := server.New(reader)
	addr := fmt.Sprintf(":%d", *port)
	log.Printf("dashboard listening on http://localhost%s", addr)
	log.Printf("reading from %s (read-only)", *dbPath)

	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		log.Fatalf("server: %v", err)
	}
}
