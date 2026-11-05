package ipc

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestListenRefusesLiveSocketButReplacesStale(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.sock")
	first, err := Listen(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(path, ""); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second listen: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("live socket must survive a second instance: %v", err)
	}
	first.Close() // Go removes the file; recreate a stale one
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := Listen(path, "")
	if err != nil {
		t.Fatalf("stale socket should be replaced: %v", err)
	}
	ln.Close()
}
