package archive

import (
	"bytes"
	"context"
	"testing"
)

func TestImportEntryRoundTrip(t *testing.T) {
	src := bytes.NewReader([]byte{0x03, 'a', 0x82, 0x00, 'b', 0x00, 'x', 0x01, 'c'})
	out, err := ImportEntry(context.Background(), src, 16)
	if err != nil {
		t.Fatalf("ImportEntry: %v", err)
	}
	if string(out) != "aaabbc" {
		t.Fatalf("out = %q, want %q", out, "aaabbc")
	}
}

func TestImportEntryTooLarge(t *testing.T) {
	src := bytes.NewReader([]byte{0x64, 'z'}) // 100 z's, cap is 10
	if _, err := ImportEntry(context.Background(), src, 10); err != ErrEntryTooLarge {
		t.Fatalf("err = %v, want ErrEntryTooLarge", err)
	}
}

func TestImportEntryTruncated(t *testing.T) {
	src := bytes.NewReader([]byte{0x05})
	if _, err := ImportEntry(context.Background(), src, 10); err == nil {
		t.Fatal("expected error for truncated entry")
	}
}
