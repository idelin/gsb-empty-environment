package netsec

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"testing"
)

func TestGzipTrailingGarbageIsRejected(t *testing.T) {
	plain := []byte("firmware-a")
	good := gzipBytes(t, plain)
	tampered := append(append([]byte(nil), good...), 0xAB, 0xCD)

	// Sanity check of the standard library behavior this test pins down.
	gr, err := gzip.NewReader(bytes.NewReader(tampered))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	if _, err := gr.Read(make([]byte, 64)); err != nil {
		t.Logf("reader reports trailing data via Read: %v", err)
	}

	sum := sha256.Sum256(plain)
	part := Partition{
		Name:         "trail",
		Dest:         Range{Offset: 4096, Length: int64(len(plain))},
		Compression:  CompressGzip,
		HashAlgo:     HashSHA256,
		ExpectedHash: sum[:],
		Open:         payloadOpener(tampered),
	}

	update, err := Prepare(context.Background(), Manifest{Partitions: []Partition{part}}, testConfig(t))
	if err != nil {
		return // already rejected, which is the desired behavior
	}
	update.Close()
	t.Fatal("Prepare accepted gzip stream with trailing garbage")
}

func TestUncompressedTrailingBytesRejected(t *testing.T) {
	plain := []byte("firmware-a")
	withTrailer := append(append([]byte(nil), plain...), 0xAA, 0xBB)
	sum := sha256.Sum256(plain)
	part := Partition{
		Name:         "raw",
		Dest:         Range{Offset: 4096, Length: int64(len(plain))},
		Compression:  CompressNone,
		HashAlgo:     HashSHA256,
		ExpectedHash: sum[:],
		Open:         payloadOpener(withTrailer),
	}

	update, err := Prepare(context.Background(), Manifest{Partitions: []Partition{part}}, testConfig(t))
	if err == nil {
		update.Close()
		t.Fatal("Prepare accepted payload with unconsumed trailing bytes")
	}
	if !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("err = %v, want ErrLengthMismatch", err)
	}
}
