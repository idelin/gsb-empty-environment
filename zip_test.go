package safeunpack

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
	"testing"
)

type zipTestEntry struct {
	name   string
	mode   os.FileMode
	method uint16
	data   []byte
}

func makeZip(t *testing.T, entries ...zipTestEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		method := e.method
		if method == 0 && !e.mode.IsDir() {
			method = zip.Deflate
		}
		header := &zip.FileHeader{Name: e.name, Method: method}
		header.SetMode(e.mode)
		w, err := zw.CreateHeader(header)
		if err != nil {
			t.Fatalf("create ZIP entry %q: %v", e.name, err)
		}
		if _, err := w.Write(e.data); err != nil {
			t.Fatalf("write ZIP entry %q: %v", e.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close ZIP: %v", err)
	}
	return buf.Bytes()
}

func TestLegalZipTree(t *testing.T) {
	entries := []zipTestEntry{
		{name: "dir/", mode: os.ModeDir | 0o755},
		{name: "hello.txt", mode: 0o644, data: []byte("hello")},
		{name: "dir/link", mode: os.ModeSymlink | 0o777, data: []byte("../hello.txt")},
	}
	data := makeZip(t, entries...)
	result, err := UnpackZIPBytes(data)
	if err != nil {
		t.Fatalf("legal ZIP rejected: %v", err)
	}
	if result.Files != 1 || result.Dirs != 1 || result.Symlinks != 1 {
		t.Fatalf("unexpected ZIP counts: files=%d dirs=%d symlinks=%d", result.Files, result.Dirs, result.Symlinks)
	}
	f, ok := result.Lookup("hello.txt")
	if !ok || string(f.Data) != "hello" {
		t.Fatalf("ZIP file missing or corrupt: %#v", f)
	}
	l, ok := result.Lookup("dir/link")
	if !ok || l.ResolvedTarget != "hello.txt" {
		t.Fatalf("ZIP symlink not resolved safely: %#v", l)
	}
}

func TestZipLocalCentralNameMismatch(t *testing.T) {
	data := makeZip(t, zipTestEntry{name: "hello.txt", mode: 0o644, method: zip.Store, data: []byte("hello")})
	local := bytes.Index(data, []byte{'P', 'K', 3, 4})
	if local < 0 {
		t.Fatal("no local header")
	}
	nameOffset := local + 30
	copy(data[nameOffset:nameOffset+9], []byte("HELLO.txt"))
	_, err := UnpackZIPBytes(data)
	if !errors.Is(err, ErrZIPConsistency) {
		t.Fatalf("local/central filename mismatch not rejected: %v", err)
	}
}

func TestZipPathTraversalRejected(t *testing.T) {
	data := makeZip(t, zipTestEntry{name: "../../escape", mode: 0o644, method: zip.Store, data: []byte("x")})
	_, err := UnpackZIPBytes(data)
	if !errors.Is(err, ErrPathTraversal) {
		t.Fatalf("ZIP traversal not rejected: %v", err)
	}
}

func TestZipDuplicateCaseInsensitive(t *testing.T) {
	data := makeZip(t,
		zipTestEntry{name: "Readme", mode: 0o644, data: []byte("a")},
		zipTestEntry{name: "readme", mode: 0o644, data: []byte("b")},
	)
	_, err := UnpackZIPBytes(data)
	if !errors.Is(err, ErrDuplicatePath) {
		t.Fatalf("ZIP case-insensitive duplicate not rejected: %v", err)
	}
}

func TestZipBombRatioRejected(t *testing.T) {
	data := makeZip(t, zipTestEntry{name: "bomb.bin", mode: 0o644, data: make([]byte, 4<<20)})
	_, err := UnpackZIPBytes(data,
		WithMaxFileBytes(8<<20),
		WithMaxTotalBytes(8<<20),
		WithMaxCompressionRatio(10),
	)
	if !errors.Is(err, ErrCompressionBomb) {
		t.Fatalf("ZIP bomb ratio not rejected: %v", err)
	}
}

func TestZipCRCMismatchRejected(t *testing.T) {
	data := makeZip(t, zipTestEntry{name: "file.txt", mode: 0o644, method: zip.Store, data: []byte("hello")})
	local := bytes.Index(data, []byte{'P', 'K', 3, 4})
	if local < 0 {
		t.Fatal("no local header")
	}
	extra := int(binaryUint16(data[local+28 : local+30]))
	dataStart := local + 30 + 9 + extra
	data[dataStart] ^= 0x20
	_, err := UnpackZIPBytes(data)
	if !errors.Is(err, ErrZIPConsistency) && !errors.Is(err, ErrMalformed) {
		t.Fatalf("ZIP CRC mismatch not rejected: %v", err)
	}
}

func TestZipResourceLimits(t *testing.T) {
	data := makeZip(t,
		zipTestEntry{name: "a", mode: 0o644, data: []byte("1234")},
		zipTestEntry{name: "b", mode: 0o644, data: []byte("5678")},
	)
	_, err := UnpackZIPBytes(data, WithMaxTotalBytes(6))
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("ZIP total limit not enforced: %v", err)
	}
	_, err = UnpackZIPBytes(data, WithMaxEntries(1))
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("ZIP entry limit not enforced: %v", err)
	}
}

func TestZipEmptyArchive(t *testing.T) {
	data := makeZip(t)
	result, err := UnpackZIPBytes(data)
	if err != nil {
		t.Fatalf("empty ZIP rejected: %v", err)
	}
	if result.Files != 0 || result.Root == nil {
		t.Fatalf("empty ZIP result wrong: %#v", result)
	}
}

func TestZipTruncatedEOCDRejected(t *testing.T) {
	data := makeZip(t, zipTestEntry{name: "a", mode: 0o644, data: []byte("x")})
	_, err := UnpackZIPBytes(data[:len(data)-10])
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("truncated ZIP not rejected: %v", err)
	}
}

func binaryUint16(b []byte) uint16 {
	return uint16(b[0]) | uint16(b[1])<<8
}

func TestZIP64SmallArchive(t *testing.T) {
	name := []byte("file.txt")
	payload := []byte("zip64 payload")
	crc := crc32.ChecksumIEEE(payload)
	local := make([]byte, 30)
	binary.LittleEndian.PutUint32(local[0:4], zipLocalSignature)
	binary.LittleEndian.PutUint16(local[4:6], 45)
	binary.LittleEndian.PutUint16(local[6:8], 0)
	binary.LittleEndian.PutUint16(local[8:10], 0)
	binary.LittleEndian.PutUint32(local[14:18], crc)
	binary.LittleEndian.PutUint32(local[18:22], uint32(len(payload)))
	binary.LittleEndian.PutUint32(local[22:26], uint32(len(payload)))
	binary.LittleEndian.PutUint16(local[26:28], uint16(len(name)))
	local = append(local, name...)
	local = append(local, payload...)
	cdOffset := uint64(len(local))

	var extra []byte
	extra = append(extra, 0x01, 0x00, 0x00, 0x00)
	binary.LittleEndian.PutUint16(extra[2:4], 24)
	for _, v := range []uint64{uint64(len(payload)), uint64(len(payload)), 0} {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], v)
		extra = append(extra, b[:]...)
	}

	var central []byte
	central = append(central, make([]byte, 46)...)
	binary.LittleEndian.PutUint32(central[0:4], zipCentralSignature)
	binary.LittleEndian.PutUint16(central[4:6], 0x0314)
	binary.LittleEndian.PutUint16(central[6:8], 45)
	binary.LittleEndian.PutUint16(central[10:12], 0)
	binary.LittleEndian.PutUint32(central[16:20], crc)
	binary.LittleEndian.PutUint32(central[20:24], 0xffffffff)
	binary.LittleEndian.PutUint32(central[24:28], 0xffffffff)
	binary.LittleEndian.PutUint16(central[28:30], uint16(len(name)))
	binary.LittleEndian.PutUint16(central[30:32], uint16(len(extra)))
	binary.LittleEndian.PutUint32(central[38:42], uint32(0o100644)<<16)
	binary.LittleEndian.PutUint32(central[42:46], 0xffffffff)
	central = append(central, name...)
	central = append(central, extra...)

	zip64Off := cdOffset + uint64(len(central))
	var zip64EOCD []byte
	zip64EOCD = append(zip64EOCD, make([]byte, 56)...)
	binary.LittleEndian.PutUint32(zip64EOCD[0:4], zip64EOCDSignature)
	binary.LittleEndian.PutUint64(zip64EOCD[4:12], 44)
	binary.LittleEndian.PutUint16(zip64EOCD[12:14], 0x0314)
	binary.LittleEndian.PutUint16(zip64EOCD[14:16], 45)
	binary.LittleEndian.PutUint64(zip64EOCD[24:32], 1)
	binary.LittleEndian.PutUint64(zip64EOCD[32:40], 1)
	binary.LittleEndian.PutUint64(zip64EOCD[40:48], uint64(len(central)))
	binary.LittleEndian.PutUint64(zip64EOCD[48:56], cdOffset)

	var locator []byte
	locator = append(locator, make([]byte, 20)...)
	binary.LittleEndian.PutUint32(locator[0:4], zip64LocatorSignature)
	binary.LittleEndian.PutUint64(locator[8:16], zip64Off)
	binary.LittleEndian.PutUint32(locator[16:20], 1)

	var eocd []byte
	eocd = append(eocd, make([]byte, 22)...)
	binary.LittleEndian.PutUint32(eocd[0:4], zipEOCDSignature)
	binary.LittleEndian.PutUint16(eocd[8:10], 0xffff)
	binary.LittleEndian.PutUint16(eocd[10:12], 0xffff)
	binary.LittleEndian.PutUint32(eocd[12:16], 0xffffffff)
	binary.LittleEndian.PutUint32(eocd[16:20], 0xffffffff)

	data := append(append(append(append(local, central...), zip64EOCD...), locator...), eocd...)
	result, err := UnpackZIPBytes(data)
	if err != nil {
		t.Fatalf("small ZIP64 archive rejected: %v", err)
	}
	file, ok := result.Lookup("file.txt")
	if !ok || string(file.Data) != string(payload) {
		t.Fatalf("ZIP64 payload missing: %#v", file)
	}
}
