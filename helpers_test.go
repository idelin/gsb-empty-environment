package netsec

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
)

const testDeviceSize = 1 << 16 // 64 KiB

// fakeDevice is an in-memory target that records every WriteAt call. A zero
// call count proves the package never touched the device, which is the central
// security assertion for rejected packages.
type fakeDevice struct {
	mu      sync.Mutex
	mem     []byte
	calls   int
	failAt  int // 1-based call index that returns an error; 0 disables
	failErr error
}

func newFakeDevice(size int) *fakeDevice {
	return &fakeDevice{mem: make([]byte, size), failErr: errors.New("simulated device fault")}
}

func (d *fakeDevice) WriteAt(p []byte, off int64) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls++
	if d.failAt != 0 && d.calls == d.failAt {
		return 0, d.failErr
	}
	if off < 0 || int(off)+len(p) > len(d.mem) {
		return 0, fmt.Errorf("write [%d,%d) outside device of %d", off, off+int64(len(p)), len(d.mem))
	}
	return copy(d.mem[off:], p), nil
}

func (d *fakeDevice) writeCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

func testConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		DeviceSize:   testDeviceSize,
		MaxExpansion: testDeviceSize,
		StagingDir:   t.TempDir(),
	}
}

func gzipBytes(t *testing.T, plain []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(plain); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func zlibBytes(t *testing.T, plain []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(plain); err != nil {
		t.Fatalf("zlib write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zlib close: %v", err)
	}
	return buf.Bytes()
}

func payloadOpener(payload []byte) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(payload)), nil
	}
}

// partitionFor builds a valid gzip partition whose manifest hash and length
// match plain exactly. Tests mutate the manifest or payload to create failure
// cases.
func partitionFor(t *testing.T, name string, offset int64, plain []byte) Partition {
	t.Helper()
	sum := sha256Sum(plain)
	return Partition{
		Name:         name,
		Dest:         Range{Offset: offset, Length: int64(len(plain))},
		Compression:  CompressGzip,
		HashAlgo:     HashSHA256,
		ExpectedHash: sum[:],
		Open:         payloadOpener(gzipBytes(t, plain)),
	}
}

func sha256Sum(plain []byte) [32]byte {
	return sha256.Sum256(plain)
}
