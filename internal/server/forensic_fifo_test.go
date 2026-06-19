//go:build unix

package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"syscall"
	"testing"
)

// TestReadFileLimited_RejectsFIFO verifies that a named pipe (FIFO) is
// rejected before opening, preventing a hang that would occur if os.Open
// blocked waiting for a writer.
func TestReadFileLimited_RejectsFIFO(t *testing.T) {
	fifo := t.TempDir() + "/forensic.fifo"
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("cannot create FIFO: %v", err)
	}
	_, err := readFileLimited(fifo, 1024)
	if !errors.Is(err, errNotRegularFile) {
		t.Fatalf("got error %v, want errNotRegularFile", err)
	}
}

// TestForensicKeyLoadPath_RejectsFIFO verifies that pointing the path endpoint
// at a FIFO is rejected before open (preventing a hang waiting for a writer).
func TestForensicKeyLoadPath_RejectsFIFO(t *testing.T) {
	fifo := t.TempDir() + "/forensic.fifo"
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("cannot create FIFO: %v", err)
	}

	srv := seedReceipts(t, Config{Host: "127.0.0.1"})
	body, _ := json.Marshal(map[string]string{"path": fifo})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, localJSONReq("POST", "/api/forensic-key/path", bytes.NewReader(body)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", w.Code)
	}
}
