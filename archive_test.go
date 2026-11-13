package netsec

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type rawEntry struct {
	name     string
	content  []byte
	declared uint64
}

func TestDeclaredSizeAboveLimitFailsBeforeDecode(t *testing.T) {
	content := []byte("tiny")
	payload := buildStoredZip(t, rawEntry{
		name:     "bomb.txt",
		content:  content,
		declared: 0xffff_fffe,
	})

	called := false
	decoder := func(io.Reader) io.ReadCloser {
		called = true
		return io.NopCloser(strings.NewReader("unexpected"))
	}

	reader, err := NewReaderWithDecompressors(bytes.NewReader(payload), int64(len(payload)), Limits{
		MaxEntrySize: 32 << 20,
	}, map[uint16]Decompressor{zip.Store: decoder})
	if reader != nil {
		t.Fatal("reader unexpectedly accepted an entry declaring 4 GiB")
	}
	if !errors.Is(err, ErrDeclaredSizeExceeded) {
		t.Fatalf("error = %v, want ErrDeclaredSizeExceeded", err)
	}
	if called {
		t.Fatal("decompressor was called before the declared size was rejected")
	}
}

func TestZip64DeclaredSizeAboveLimitFails(t *testing.T) {
	payload := buildZip64Bomb(t, "zip64-bomb.txt", []byte("tiny"))

	reader, err := NewReader(bytes.NewReader(payload), int64(len(payload)), Limits{
		MaxEntrySize:        32 << 20,
		MaxCompressionRatio: 1000,
	})
	if reader != nil {
		t.Fatal("reader unexpectedly accepted a ZIP64 entry declaring 2^64-1 bytes")
	}
	if !errors.Is(err, ErrDeclaredSizeExceeded) {
		t.Fatalf("error = %v, want ErrDeclaredSizeExceeded", err)
	}
}

func TestDeclaredRatioAboveCallerLimitFails(t *testing.T) {
	content := bytes.Repeat([]byte("a"), 4096)
	payload := buildStoredZip(t, rawEntry{
		name:     "high-ratio.txt",
		content:  content,
		declared: 1 << 20,
	})

	reader, err := NewReader(bytes.NewReader(payload), int64(len(payload)), Limits{
		MaxEntrySize:        8 << 20,
		MaxCompressionRatio: 2,
	})
	if reader != nil {
		t.Fatal("reader unexpectedly accepted a 256:1 declared ratio")
	}
	if !errors.Is(err, ErrCompressionRatioExceeded) {
		t.Fatalf("error = %v, want ErrCompressionRatioExceeded", err)
	}
}

func TestNormalSmallFilesExtract(t *testing.T) {
	payload := buildStandardZip(t, map[string][]byte{
		"small.txt": []byte("hello, bounded archive\n"),
		"notes.txt": []byte(strings.Repeat("normal text\n", 100)),
	})

	reader, err := NewReader(bytes.NewReader(payload), int64(len(payload)), Limits{
		MaxEntrySize:        1 << 20,
		MaxTotalSize:        2 << 20,
		MaxCompressionRatio: 1000,
	})
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	for name, expected := range map[string]string{
		"small.txt": "hello, bounded archive\n",
		"notes.txt": strings.Repeat("normal text\n", 100),
	} {
		got, err := reader.ReadFile(name)
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", name, err)
		}
		if string(got) != expected {
			t.Fatalf("ReadFile(%q) = %q, want %q", name, got, expected)
		}
	}
}

func TestRuntimeOutputCannotExceedDeclaredSize(t *testing.T) {
	content := []byte("0123456789abcdef")
	payload := buildStoredZip(t, rawEntry{
		name:     "liar.txt",
		content:  content,
		declared: uint64(len(content)),
	})

	reader, err := NewReaderWithDecompressors(bytes.NewReader(payload), int64(len(payload)), Limits{
		MaxEntrySize:        1 << 20,
		MaxCompressionRatio: 100,
	}, map[uint16]Decompressor{
		zip.Store: func(io.Reader) io.ReadCloser {
			return io.NopCloser(strings.NewReader(strings.Repeat("x", 1024)))
		},
	})
	if err != nil {
		t.Fatalf("NewReaderWithDecompressors: %v", err)
	}

	file := reader.Find("liar.txt")
	if file == nil {
		t.Fatal("entry not found")
	}
	data, err := file.ReadAll()
	if !errors.Is(err, ErrOutputSizeExceeded) {
		t.Fatalf("ReadAll error = %v, want ErrOutputSizeExceeded", err)
	}
	if len(data) != int(file.UncompressedSize64) {
		t.Fatalf("read %d bytes before failing, want %d", len(data), file.UncompressedSize64)
	}
}

func TestArchiveTotalDeclaredSizeLimit(t *testing.T) {
	payload := buildStandardZip(t, map[string][]byte{
		"a.txt": []byte(strings.Repeat("a", 100)),
		"b.txt": []byte(strings.Repeat("b", 100)),
	})

	reader, err := NewReader(bytes.NewReader(payload), int64(len(payload)), Limits{
		MaxEntrySize: 1 << 20,
		MaxTotalSize: 64,
	})
	if reader != nil {
		t.Fatal("reader unexpectedly accepted archive above total limit")
	}
	if !errors.Is(err, ErrTotalSizeExceeded) {
		t.Fatalf("error = %v, want ErrTotalSizeExceeded", err)
	}
}

func TestExtractAllRejectsEscapingPath(t *testing.T) {
	payload := buildStandardZip(t, map[string][]byte{
		"../escape.txt": []byte("should not be written"),
	})
	reader, err := NewReader(bytes.NewReader(payload), int64(len(payload)), Limits{
		MaxEntrySize:        1 << 20,
		MaxCompressionRatio: 1000,
	})
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	dir := t.TempDir()
	if err := reader.ExtractAll(dir, 0o644); err == nil {
		t.Fatal("ExtractAll accepted an escaping path")
	}
	if _, err := os.Stat(filepath.Join(dir, "escape.txt")); err == nil {
		t.Fatal("escaping entry was written")
	}
}

func buildStoredZip(t *testing.T, entries ...rawEntry) []byte {
	t.Helper()
	var out bytes.Buffer
	type central struct {
		name     string
		offset   uint32
		declared uint64
		compSize uint32
		crc      uint32
	}
	var centrals []central

	for _, entry := range entries {
		offset := uint32(out.Len())
		crc := crc32.ChecksumIEEE(entry.content)
		declared := entry.declared
		if declared == 0 && len(entry.content) > 0 {
			declared = uint64(len(entry.content))
		}
		if declared > 0xffff_fffe {
			t.Fatalf("test helper only supports 32-bit declared sizes, got %d", declared)
		}
		compSize := uint32(len(entry.content))

		write32(&out, 0x04034b50)
		write16(&out, 20)
		write16(&out, 0)
		write16(&out, zip.Store)
		write16(&out, 0)
		write16(&out, 0)
		write32(&out, crc)
		write32(&out, compSize)
		write32(&out, uint32(declared))
		write16(&out, uint16(len(entry.name)))
		write16(&out, 0)
		out.WriteString(entry.name)
		out.Write(entry.content)
		centrals = append(centrals, central{
			name:     entry.name,
			offset:   offset,
			declared: declared,
			compSize: compSize,
			crc:      crc,
		})
	}

	cdOffset := uint32(out.Len())
	for _, entry := range centrals {
		write32(&out, 0x02014b50)
		write16(&out, 20)
		write16(&out, 20)
		write16(&out, 0)
		write16(&out, zip.Store)
		write16(&out, 0)
		write16(&out, 0)
		write32(&out, entry.crc)
		write32(&out, entry.compSize)
		write32(&out, uint32(entry.declared))
		write16(&out, uint16(len(entry.name)))
		write16(&out, 0)
		write16(&out, 0)
		write16(&out, 0)
		write16(&out, 0)
		write32(&out, 0)
		write32(&out, entry.offset)
		out.WriteString(entry.name)
	}
	cdSize := uint32(out.Len()) - cdOffset

	write32(&out, 0x06054b50)
	write16(&out, 0)
	write16(&out, 0)
	write16(&out, uint16(len(centrals)))
	write16(&out, uint16(len(centrals)))
	write32(&out, cdSize)
	write32(&out, cdOffset)
	write16(&out, 0)
	return out.Bytes()
}

func buildStandardZip(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("Create(%q): %v", name, err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatalf("Write(%q): %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return out.Bytes()
}

func buildZip64Bomb(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	const (
		zip64ExtraID = 0x0001
		zip64Extra   = 20
	)

	write32(&out, 0x04034b50)
	write16(&out, 45)
	write16(&out, 0)
	write16(&out, zip.Store)
	write16(&out, 0)
	write16(&out, 0)
	write32(&out, 0)
	write32(&out, 0xffff_ffff)
	write32(&out, 0xffff_ffff)
	write16(&out, uint16(len(name)))
	write16(&out, zip64Extra)
	out.WriteString(name)
	write16(&out, zip64ExtraID)
	write16(&out, 16)
	write64(&out, ^uint64(0))
	write64(&out, uint64(len(content)))
	out.Write(content)

	cdOffset := uint32(out.Len())
	write32(&out, 0x02014b50)
	write16(&out, 45)
	write16(&out, 45)
	write16(&out, 0)
	write16(&out, zip.Store)
	write16(&out, 0)
	write16(&out, 0)
	write32(&out, 0)
	write32(&out, 0xffff_ffff)
	write32(&out, 0xffff_ffff)
	write16(&out, uint16(len(name)))
	write16(&out, zip64Extra)
	write16(&out, 0)
	write16(&out, 0)
	write16(&out, 0)
	write32(&out, 0)
	write32(&out, 0)
	out.WriteString(name)
	write16(&out, zip64ExtraID)
	write16(&out, 16)
	write64(&out, ^uint64(0))
	write64(&out, uint64(len(content)))
	cdSize := uint32(out.Len()) - cdOffset

	write32(&out, 0x06054b50)
	write16(&out, 0)
	write16(&out, 0)
	write16(&out, 1)
	write16(&out, 1)
	write32(&out, cdSize)
	write32(&out, cdOffset)
	write16(&out, 0)
	return out.Bytes()
}

func write16(w io.Writer, value uint16) {
	_ = binary.Write(w, binary.LittleEndian, value)
}

func write32(w io.Writer, value uint32) {
	_ = binary.Write(w, binary.LittleEndian, value)
}

func write64(w io.Writer, value uint64) {
	_ = binary.Write(w, binary.LittleEndian, value)
}
