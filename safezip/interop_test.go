package safezip

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
)

// TestInteropWithStdLib proves the hand-built ZIP64 archive is a real,
// spec-conformant archive: the standard library must also read it.
func TestInteropWithStdLib(t *testing.T) {
	payload := strings.Repeat("interop-", 137)
	raw := buildRawZip64([]rawZip64Member{
		{name: "big.txt", data: []byte(payload)},
		{name: "d/", dir: true},
		{name: "small.txt", data: []byte("ok")},
	})
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("stdlib cannot read our zip64: %v (len=%d, tail=%x)", err, len(raw), raw[len(raw)-22:])
	}
	found := false
	for _, f := range zr.File {
		if f.Name != "big.txt" {
			continue
		}
		found = true
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		got, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if string(got) != payload {
			t.Fatalf("payload mismatch")
		}
	}
	if !found {
		t.Fatal("big.txt missing in stdlib view")
	}
}

// TestStdLibDescriptorsReadable confirms our descriptor archives are also
// accepted by the standard library.
func TestStdLibDescriptorsReadable(t *testing.T) {
	raw := buildZip(true, fixtureMember{name: "a.txt", data: []byte("desc-data")})
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("stdlib: %v", err)
	}
	if len(zr.File) != 1 {
		t.Fatalf("unexpected file count: %d", len(zr.File))
	}
}

// TestStoreMethodMember covers the zero-compression path explicitly.
func TestStoreMethodMember(t *testing.T) {
	raw := buildRawZip64Store("stored.txt", []byte("no-compression-here"))
	a, err := Open(BytesStore{B: raw}, testConfig())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if got := readMember(t, a, "stored.txt"); string(got) != "no-compression-here" {
		t.Fatalf("stored payload mismatch: %q", got)
	}
}
