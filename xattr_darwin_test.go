//go:build darwin && cgo

package netsec

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestDarwinOSAttributeRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "xattr-file")
	writeFile(t, path, "data")

	want := makeAttribute(TypeSignature, IMAVersion2, AlgorithmSHA256, 7, []byte("sig"))
	if err := SetAttribute(path, XattrName, want); err != nil {
		t.Skipf("cannot set %s on this system: %v", XattrName, err)
	}
	defer RemoveAttribute(path, XattrName)

	got, err := (OSReader{}).ReadAttribute(path, XattrName)
	if err != nil {
		t.Fatalf("read attribute: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("attribute = %x, want %x", got, want)
	}
}
