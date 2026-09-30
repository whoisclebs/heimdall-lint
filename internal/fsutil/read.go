// Package fsutil holds the small filesystem helpers shared by loaders.
package fsutil

import (
	"fmt"
	"io"
	"os"
)

const (
	MaxSchemaBytes   = 1 << 20
	MaxManifestBytes = 1 << 20
	MaxEnvBytes      = 4 << 20
)

// ReadFile reads a regular file and refuses anything larger than limit, so
// hostile inputs (huge files, devices, FIFOs) cannot exhaust memory or hang.
func ReadFile(path string, limit int64) ([]byte, error) {
	// Stat before Open: opening a FIFO with no writer blocks forever.
	if info, err := os.Stat(path); err != nil {
		return nil, err
	} else if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("file is larger than %d bytes", limit)
	}

	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file is larger than %d bytes", limit)
	}
	return data, nil
}
