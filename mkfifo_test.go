//go:build darwin || linux

package safearch

import (
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestPackRejectsSpecialFiles(t *testing.T) {
	src := t.TempDir()
	fifo := filepath.Join(src, "fifo")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("cannot create fifo: %v", err)
	}
	mustErrIs(t, Pack(src, &bytes.Buffer{}), ErrUnsupported)
	_ = os.Remove(fifo)
}
