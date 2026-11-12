package zdebug

import (
	"bytes"
	"compress/zlib"
	"errors"
	"testing"
)

func TestShortReadIsRetried(t *testing.T) {
	payload := bytes.Repeat([]byte("short-read-"), 50) // 550 bytes
	encoded := EncodeFrames(payload[:200], payload[200:])

	section := Section{
		Name:            "flaky",
		Method:          MethodFrame,
		PhysicalOffset:  0,
		PhysicalLength:  int64(len(encoded)),
		MaxDecompressed: int64(len(payload)),
	}
	factory := NewCountingFactory(frameFactory)
	cfg := testConfig(factory, 128, 0)

	host := mustHost(t, &flakyReader{data: encoded}, int64(len(encoded)), []Section{section}, cfg)
	t.Cleanup(func() { host.Close() })

	got, err := readAll(t, host, section.Name)
	if err != nil {
		t.Fatalf("flaky backing reader must be retried, got %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("content mismatch under short reads")
	}
}

func TestTruncatedPhysicalStreamRejected(t *testing.T) {
	payload := bytes.Repeat([]byte("truncate-"), 40) // 400 bytes
	encoded := EncodeFrames(payload[:160], payload[160:])

	// The directory claims the full range, but the backing source stops
	// halfway: every physical read past the limit returns io.EOF.
	truncatedAt := int64(len(encoded) / 2)
	section := Section{
		Name:            "truncated",
		Method:          MethodFrame,
		PhysicalOffset:  0,
		PhysicalLength:  int64(len(encoded)),
		MaxDecompressed: int64(len(payload)),
	}
	factory := NewCountingFactory(frameFactory)
	cfg := testConfig(factory, 128, 0)

	host := mustHost(t,
		&truncatingReader{data: encoded, limit: truncatedAt},
		int64(len(encoded)), []Section{section}, cfg)
	t.Cleanup(func() { host.Close() })

	if _, err := readAll(t, host, section.Name); err == nil {
		t.Fatal("truncated stream must fail verification")
	}

	state := host.entries[section.Name]
	if cached := cachedPageCount(host, state); cached != 0 || state.size != 0 {
		t.Fatalf("partial stream must not be cached, got pages=%d size=%d", cached, state.size)
	}
	if host.BudgetUsed() != 0 {
		t.Fatalf("failed stream must not consume budget, used=%d", host.BudgetUsed())
	}

	// After the backing source is repaired, a retry must succeed.
	host.data = bytes.NewReader(encoded)
	got, err := readAll(t, host, section.Name)
	if err != nil {
		t.Fatalf("retry after repair: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("repaired stream content mismatch")
	}
}

func TestDecompressionCapEnforced(t *testing.T) {
	payload := bytes.Repeat([]byte("bomb"), 100) // 400 bytes
	encoded := EncodeFrames(payload)

	section := Section{
		Name:            "bomb",
		Method:          MethodFrame,
		PhysicalOffset:  0,
		PhysicalLength:  int64(len(encoded)),
		MaxDecompressed: int64(len(payload) - 1),
	}
	factory := NewCountingFactory(frameFactory)
	cfg := testConfig(factory, 64, 0)

	host := mustHost(t, bytes.NewReader(encoded), int64(len(encoded)), []Section{section}, cfg)
	t.Cleanup(func() { host.Close() })

	_, err := readAll(t, host, section.Name)
	if !errors.Is(err, ErrCapExceeded) {
		t.Fatalf("want ErrCapExceeded, got %v", err)
	}

	state := host.entries[section.Name]
	if cached := cachedPageCount(host, state); cached != 0 {
		t.Fatalf("over-cap stream must not cache pages, got %d", cached)
	}
}

func TestCorruptChecksumRejected(t *testing.T) {
	payload := bytes.Repeat([]byte("check-"), 60) // 360 bytes
	encoded := EncodeFrames(payload)
	corrupt := corruptAt(encoded, len(encoded)/2)

	section := Section{
		Name:            "corrupt",
		Method:          MethodFrame,
		PhysicalOffset:  0,
		PhysicalLength:  int64(len(corrupt)),
		MaxDecompressed: int64(len(payload)),
	}
	factory := NewCountingFactory(frameFactory)
	cfg := testConfig(factory, 64, 0)

	host := mustHost(t, bytes.NewReader(corrupt), int64(len(corrupt)), []Section{section}, cfg)
	t.Cleanup(func() { host.Close() })

	if _, err := readAll(t, host, section.Name); err == nil {
		t.Fatal("checksum mismatch must fail verification")
	}
	if host.BudgetUsed() != 0 {
		t.Fatalf("corrupt stream must not consume budget, used=%d", host.BudgetUsed())
	}
}

func TestTrailingJunkRejected(t *testing.T) {
	payload := []byte("trailing-junk-test")
	encoded := EncodeFrames(payload)
	junk := append(append([]byte(nil), encoded...), 0xAB, 0xCD)

	section := Section{
		Name:            "junk",
		Method:          MethodFrame,
		PhysicalOffset:  0,
		PhysicalLength:  int64(len(junk)),
		MaxDecompressed: int64(len(payload)),
	}
	factory := NewCountingFactory(frameFactory)
	cfg := testConfig(factory, 32, 0)

	host := mustHost(t, bytes.NewReader(junk), int64(len(junk)), []Section{section}, cfg)
	t.Cleanup(func() { host.Close() })

	if _, err := readAll(t, host, section.Name); err == nil {
		t.Fatal("trailing bytes must be rejected instead of ignored")
	}

	// The exact declared length without junk verifies successfully.
	clean := Section{
		Name:            "clean",
		Method:          MethodFrame,
		PhysicalOffset:  0,
		PhysicalLength:  int64(len(encoded)),
		MaxDecompressed: int64(len(payload)),
	}
	host2 := mustHost(t, bytes.NewReader(encoded), int64(len(encoded)), []Section{clean}, cfg)
	t.Cleanup(func() { host2.Close() })

	got, err := readAll(t, host2, clean.Name)
	if err != nil {
		t.Fatalf("exact-length stream: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("clean stream content mismatch")
	}
}

func TestZlibRoundTripAndCorruption(t *testing.T) {
	payload := bytes.Repeat([]byte("zlib debug section "), 100) // 1900 bytes

	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	if _, err := writer.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	encoded := compressed.Bytes()

	section := Section{
		Name:            ".zdebug_zlib",
		Method:          MethodZlib,
		PhysicalOffset:  0,
		PhysicalLength:  int64(len(encoded)),
		MaxDecompressed: int64(len(payload)),
	}

	host, err := NewHostConfig(bytes.NewReader(encoded), int64(len(encoded)),
		[]Section{section}, Config{PageSize: 256})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { host.Close() })

	got, err := readAll(t, host, section.Name)
	if err != nil {
		t.Fatalf("zlib round trip: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("zlib content mismatch")
	}

	// Flip a byte in the middle and expect the checksum verification to fail.
	corrupt := corruptAt(encoded, len(encoded)/2)
	badHost, err := NewHostConfig(bytes.NewReader(corrupt), int64(len(corrupt)),
		[]Section{section}, Config{PageSize: 256})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { badHost.Close() })

	if _, err := readAll(t, badHost, section.Name); err == nil {
		t.Fatal("corrupt zlib stream must fail checksum verification")
	}
	if badHost.BudgetUsed() != 0 {
		t.Fatalf("corrupt zlib stream must not consume budget, used=%d", badHost.BudgetUsed())
	}
}
