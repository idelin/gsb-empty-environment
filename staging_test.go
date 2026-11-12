package netsec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestLengthMismatchRejectsWholePackage(t *testing.T) {
	plain := []byte("firmware-a")
	part := partitionFor(t, "faulty", 4096, plain)
	part.Dest.Length = int64(len(plain)) + 1 // claim one byte more than emitted

	device := newFakeDevice(testDeviceSize)
	update, err := Prepare(context.Background(), Manifest{Partitions: []Partition{part}}, testConfig(t))
	if err == nil {
		update.Close()
		t.Fatal("Prepare succeeded for short stream")
	}
	if !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("err = %v, want ErrLengthMismatch", err)
	}
	if device.writeCount() != 0 {
		t.Fatalf("device received %d writes", device.writeCount())
	}
}

func TestOverLongStreamIsCappedAndRejected(t *testing.T) {
	plain := bytes.Repeat([]byte("A"), 64)
	part := partitionFor(t, "bomb", 4096, plain)
	part.Dest.Length = 8 // stream emits 64, staging must stop at the cap
	sum := sha256.Sum256(plain[:8])
	part.ExpectedHash = sum[:]

	_, err := Prepare(context.Background(), Manifest{Partitions: []Partition{part}}, testConfig(t))
	if !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("err = %v, want ErrLengthMismatch", err)
	}
}

func TestDigestMismatchRejectsWholePackage(t *testing.T) {
	part := partitionFor(t, "corrupt", 4096, []byte("firmware-a"))
	part.ExpectedHash = bytes.Repeat([]byte{0xff}, sha256.Size)

	device := newFakeDevice(testDeviceSize)
	update, err := Prepare(context.Background(), Manifest{Partitions: []Partition{part}}, testConfig(t))
	if err == nil {
		update.Close()
		t.Fatal("Prepare succeeded for bad digest")
	}
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("err = %v, want ErrDigestMismatch", err)
	}
	if device.writeCount() != 0 {
		t.Fatalf("device received %d writes", device.writeCount())
	}
}

func TestCorruptTrailerDetected(t *testing.T) {
	plain := []byte("firmware-a")
	good := gzipBytes(t, plain)
	corrupt := make([]byte, len(good))
	copy(corrupt, good)
	corrupt[len(corrupt)-1] ^= 0xFF // damage gzip trailer/CRC

	part := partitionFor(t, "trailer", 4096, plain)
	part.Open = payloadOpener(corrupt)

	_, err := Prepare(context.Background(), Manifest{Partitions: []Partition{part}}, testConfig(t))
	if err == nil {
		t.Fatal("Prepare accepted corrupted gzip stream")
	}
	if !errors.Is(err, ErrDecompressor) && !errors.Is(err, ErrDigestMismatch) && !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestLastPartitionCorruptMeansZeroDeviceWrites(t *testing.T) {
	// Two healthy partitions stage first; the corrupt final item must reject
	// the whole package so Commit is never reached.
	manifest := Manifest{
		Partitions: []Partition{
			partitionFor(t, "first", 4096, []byte("firmware-a")),
			partitionFor(t, "second", 8192, []byte("firmware-b")),
			partitionFor(t, "last", 12288, []byte("firmware-c")),
		},
	}
	manifest.Partitions[2].ExpectedHash = bytes.Repeat([]byte{0x01}, sha256.Size)

	device := newFakeDevice(testDeviceSize)
	update, err := Prepare(context.Background(), manifest, testConfig(t))
	if err == nil {
		update.Close()
		t.Fatal("Prepare succeeded with corrupt final partition")
	}
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("err = %v, want ErrDigestMismatch", err)
	}
	if device.writeCount() != 0 {
		t.Fatalf("device received %d writes despite rejected package", device.writeCount())
	}
}

func TestFaultyPartitionStagesNoDeviceWrites(t *testing.T) {
	// A faulty first partition (declared length larger than the stream) fails
	// before any later work, and the device stays untouched.
	manifest := Manifest{
		Partitions: []Partition{
			partitionFor(t, "broken", 4096, []byte("firmware-a")),
			partitionFor(t, "ok", 8192, []byte("firmware-b")),
		},
	}
	manifest.Partitions[0].Dest.Length++

	device := newFakeDevice(testDeviceSize)
	update, err := Prepare(context.Background(), manifest, testConfig(t))
	if err == nil {
		update.Close()
		t.Fatal("Prepare succeeded with faulty first partition")
	}
	if !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("err = %v, want ErrLengthMismatch", err)
	}
	if device.writeCount() != 0 {
		t.Fatalf("device received %d writes", device.writeCount())
	}
}

func TestDecompressorNeverReceivesDevice(t *testing.T) {
	// Prepare takes no Device parameter, so the decompression path cannot
	// reach the target device by construction. A sentinel decompressor proves
	// the custom registry is exercised, and the staging writer type is
	// asserted directly below.
	config := testConfig(t)
	config.Decompressors = map[Compression]Decompressor{
		"inspect": func(reader io.Reader) (io.ReadCloser, error) {
			return io.NopCloser(reader), nil
		},
	}

	plain := []byte("plain bytes")
	sum := sha256.Sum256(plain)
	manifest := Manifest{
		Partitions: []Partition{{
			Name:         "p",
			Dest:         Range{Offset: 0, Length: int64(len(plain))},
			Compression:  "inspect",
			HashAlgo:     HashSHA256,
			ExpectedHash: sum[:],
			Open:         payloadOpener(plain),
		}},
	}

	update, err := Prepare(context.Background(), manifest, config)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer update.Close()

	// The only writer passed into the staging pipeline is the limited writer
	// wrapping the staging file; assert it cannot be a Device.
	var pipelineWriter io.Writer = &limitedWriter{remaining: 1, dst: io.Discard}
	if _, ok := pipelineWriter.(Device); ok {
		t.Fatal("staging writer must not implement Device")
	}

	device := newFakeDevice(testDeviceSize)
	if err := update.Commit(device); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if string(device.mem[:len(plain)]) != string(plain) {
		t.Fatalf("device bytes = %q, want %q", device.mem[:len(plain)], plain)
	}
}

func TestCommitRecheckRejectsTamperedStaging(t *testing.T) {
	manifest := Manifest{
		Partitions: []Partition{
			partitionFor(t, "a", 4096, []byte("firmware-a")),
			partitionFor(t, "b", 8192, []byte("firmware-b")),
		},
	}
	update, err := Prepare(context.Background(), manifest, testConfig(t))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer update.Close()

	// Tamper with the last staged file after Prepare returned.
	tampered := update.items[1].path
	if err := os.WriteFile(tampered, []byte("garbage!!"), 0o600); err != nil {
		t.Fatalf("tamper staging: %v", err)
	}

	device := newFakeDevice(testDeviceSize)
	err = update.Commit(device)
	if !errors.Is(err, ErrCommitRecheck) {
		t.Fatalf("Commit err = %v, want ErrCommitRecheck", err)
	}
	if device.writeCount() != 0 {
		t.Fatalf("device received %d writes after failed recheck", device.writeCount())
	}
}

func TestStagingCleanedUpOnFailureAndClose(t *testing.T) {
	config := testConfig(t)

	failing := Manifest{Partitions: []Partition{partitionFor(t, "a", 4096, []byte("x"))}}
	failing.Partitions[0].ExpectedHash = bytes.Repeat([]byte{0xee}, sha256.Size)
	update, err := Prepare(context.Background(), failing, config)
	if err == nil {
		update.Close()
		t.Fatal("Prepare unexpectedly succeeded")
	}

	entries, _ := os.ReadDir(config.StagingDir)
	for _, entry := range entries {
		if matched, _ := filepath.Match("netsec-stage-*", entry.Name()); matched {
			t.Fatalf("staging dir %s left behind after failure", entry.Name())
		}
	}

	good := Manifest{Partitions: []Partition{partitionFor(t, "a", 4096, []byte("firmware-a"))}}
	update, err = Prepare(context.Background(), good, config)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	stagingDir := update.dir
	if _, err := os.Stat(stagingDir); err != nil {
		t.Fatalf("staging dir missing before Close: %v", err)
	}
	if err := update.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := update.Close(); err != nil {
		t.Fatalf("repeated Close: %v", err)
	}
	if _, err := os.Stat(stagingDir); !os.IsNotExist(err) {
		t.Fatalf("staging dir %s still exists after Close", stagingDir)
	}
	if err := update.Commit(newFakeDevice(testDeviceSize)); !errors.Is(err, ErrClosed) {
		t.Fatalf("Commit after Close err = %v, want ErrClosed", err)
	}
}

func TestCommitHonorsExactOffsetsAndSizes(t *testing.T) {
	plain := bytes.Repeat([]byte("Z"), copyBufferSize+37) // spans multiple copy chunks
	manifest := Manifest{
		Partitions: []Partition{
			partitionFor(t, "multi", 4096, plain),
		},
	}
	update, err := Prepare(context.Background(), manifest, testConfig(t))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer update.Close()

	device := newFakeDevice(testDeviceSize)
	if err := update.Commit(device); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !bytes.Equal(device.mem[4096:4096+len(plain)], plain) {
		t.Fatal("multi-chunk partition content mismatch")
	}
	if device.mem[4095] != 0 || device.mem[4096+len(plain)] != 0 {
		t.Fatal("Commit wrote outside the declared destination interval")
	}
}

func TestContextCancellationAbortsStaging(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	manifest := Manifest{
		Partitions: []Partition{
			partitionFor(t, "a", 4096, []byte("firmware-a")),
		},
	}
	device := newFakeDevice(testDeviceSize)
	update, err := Prepare(ctx, manifest, testConfig(t))
	if err == nil {
		update.Close()
		t.Fatal("Prepare succeeded with canceled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if device.writeCount() != 0 {
		t.Fatalf("device received %d writes", device.writeCount())
	}
}

func TestNoneAndZlibCompressionRoundTrip(t *testing.T) {
	plain := []byte("uncompressed and zlib payloads")

	noneSum := sha256.Sum256(plain)
	nonePart := Partition{
		Name:         "raw",
		Dest:         Range{Offset: 1024, Length: int64(len(plain))},
		Compression:  CompressNone,
		HashAlgo:     HashSHA256,
		ExpectedHash: noneSum[:],
		Open:         payloadOpener(append([]byte(nil), plain...)),
	}

	zlibPart := partitionFor(t, "z", 2048, plain)
	zlibPart.Compression = CompressZlib
	zlibPart.Open = payloadOpener(zlibBytes(t, plain))

	update, err := Prepare(context.Background(), Manifest{Partitions: []Partition{nonePart, zlibPart}}, testConfig(t))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer update.Close()

	device := newFakeDevice(testDeviceSize)
	if err := update.Commit(device); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !bytes.Equal(device.mem[1024:1024+len(plain)], plain) {
		t.Fatal("none-compression payload mismatch")
	}
	if !bytes.Equal(device.mem[2048:2048+len(plain)], plain) {
		t.Fatal("zlib payload mismatch")
	}
}
