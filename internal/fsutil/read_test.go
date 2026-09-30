//go:build unix

package fsutil

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestReadFile(t *testing.T) {
	dir := t.TempDir()
	small := filepath.Join(dir, "small")
	if err := os.WriteFile(small, []byte("A=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if data, err := ReadFile(small, 100); err != nil || string(data) != "A=1\n" {
		t.Fatalf("ReadFile = %q, %v", data, err)
	}
	if _, err := ReadFile(small, 2); err == nil || !strings.Contains(err.Error(), "larger") {
		t.Fatalf("oversized file accepted: %v", err)
	}
	if _, err := ReadFile(dir, 100); err == nil {
		t.Fatal("directory accepted")
	}
	if _, err := ReadFile(filepath.Join(dir, "missing"), 100); !os.IsNotExist(err) {
		t.Fatalf("err = %v", err)
	}
}

// A FIFO without a writer blocks os.Open forever; ReadFile must refuse it fast.
func TestReadFileRefusesFIFOWithoutHanging(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "pipe.env")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := ReadFile(fifo, 100)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO accepted")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ReadFile hung on a FIFO")
	}
}
