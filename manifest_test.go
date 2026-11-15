package netsec

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"strconv"
	"testing"
)

func TestParseManifestRejectsDuplicatePath(t *testing.T) {
	t.Parallel()

	digestA := sha256.Sum256([]byte("a"))
	digestB := sha256.Sum256([]byte("b"))
	manifest := append(testRecord("same", digestA), testRecord("same", digestB)...)

	_, err := ParseManifest(manifest)
	if !errors.Is(err, ErrDuplicateManifestPath) {
		t.Fatalf("ParseManifest error = %v, want %v", err, ErrDuplicateManifestPath)
	}
}

func TestParseManifestRejectsUnsortedPaths(t *testing.T) {
	t.Parallel()

	digest := sha256.Sum256(nil)
	manifest := append(testRecord("b", digest), testRecord("a", digest)...)

	_, err := ParseManifest(manifest)
	if !errors.Is(err, ErrManifestOrder) {
		t.Fatalf("ParseManifest error = %v, want %v", err, ErrManifestOrder)
	}
}

func TestParseManifestRejectsNonCanonicalAndTruncatedData(t *testing.T) {
	t.Parallel()

	digest := sha256.Sum256(nil)
	tests := []struct {
		name string
		data []byte
	}{
		{name: "non-canonical path", data: testRecord("a//b", digest)},
		{name: "truncated digest", data: testRecord("a", digest)[:len(testRecord("a", digest))-1]},
		{name: "leading zero length", data: []byte("02:ab" + string(digest[:]))},
		{name: "invalid UTF-8", data: testRecord(string([]byte{0xff}), digest)},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := ParseManifest(test.data)
			if !errors.Is(err, ErrInvalidManifest) {
				t.Fatalf("ParseManifest error = %v, want %v", err, ErrInvalidManifest)
			}
		})
	}
}

func TestManifestUsesPathByteLength(t *testing.T) {
	t.Parallel()

	path := "文档/é.txt"
	digest := sha256.Sum256([]byte("content"))
	manifest, err := EncodeManifest([]ManifestEntry{{Path: path, SHA256: digest}})
	if err != nil {
		t.Fatal(err)
	}

	prefix := strconv.Itoa(len([]byte(path))) + ":"
	if !bytes.HasPrefix(manifest, []byte(prefix)) {
		t.Fatalf("manifest prefix = %q, want %q", manifest[:len(prefix)], prefix)
	}

	entries, err := ParseManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Path != path || entries[0].SHA256 != digest {
		t.Fatalf("round trip = %#v", entries)
	}
}

func testRecord(path string, digest [sha256.Size]byte) []byte {
	var record bytes.Buffer
	record.WriteString(strconv.Itoa(len([]byte(path))))
	record.WriteByte(':')
	record.WriteString(path)
	record.Write(digest[:])
	return record.Bytes()
}
