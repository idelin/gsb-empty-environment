package safezip

import (
	"bytes"
	"errors"
	"testing"
)

// TestDescriptorRegionOverlap builds a descriptor archive and points a
// second member's local header at the first member's data-descriptor area,
// proving descriptor bytes are covered by the overlap check.
func TestDescriptorRegionOverlap(t *testing.T) {
	raw := buildZip(true,
		fixtureMember{name: "a.txt", data: []byte("first-payload")},
		fixtureMember{name: "b.txt", data: []byte("second")},
	)
	tampered := append([]byte(nil), raw...)
	firstCD := bytes.Index(tampered, u32(sigCentral))
	secondCD := bytes.Index(tampered[firstCD+4:], u32(sigCentral)) + firstCD + 4

	// Find the first member's data descriptor and redirect b's local
	// header offset into it.
	descIdx := bytes.Index(tampered, u32(sigDataDesc))
	if descIdx < 0 {
		t.Fatal("data descriptor not found")
	}
	binaryPutU32(tampered[secondCD+42:], uint32(descIdx))
	_, err := Open(BytesStore{B: tampered}, testConfig())
	if err == nil {
		t.Fatal("expected rejection of descriptor-region overlap")
	}
	if !errors.Is(err, ErrOverlap) && !errors.Is(err, ErrBinding) && !errors.Is(err, ErrTruncated) {
		t.Fatalf("unexpected error class: %v", err)
	}
}
