package netsec

import (
	"errors"
	"testing"
)

func TestDecodeHeadersSimple(t *testing.T) {
	got, err := DecodeHeaderString("Host: example.com\r\nX-Test:  v \r\n\r\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []Header{
		{Name: "Host", Value: "example.com"},
		{Name: "X-Test", Value: "v"},
	}
	assertHeaders(t, got, want)
}

func TestInteriorSpacesPreserved(t *testing.T) {
	got, err := DecodeHeaderString("X-Tag: a   b\t c \r\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Name != "X-Tag" || got[0].Value != "a   b\t c" {
		t.Fatalf("interior whitespace not preserved: %+v", got)
	}
}

func TestFoldInMiddleOfValueRejected(t *testing.T) {
	// CRLF + SP inserted in the middle of the value: classic obs-fold.
	block := "X-Long: part1\r\n part2\r\n"
	got, err := DecodeHeaderString(block)
	if err == nil {
		t.Fatalf("expected rejection, got headers: %+v", got)
	}
	if !errors.Is(err, ErrLineFolding) {
		t.Fatalf("expected ErrLineFolding, got: %v", err)
	}
	if got != nil {
		t.Fatalf("no partial headers may be returned on rejection, got: %+v", got)
	}
	var fe *FieldError
	if !errors.As(err, &fe) || fe.Line != 2 {
		t.Fatalf("expected FieldError at line 2, got: %v", err)
	}
}

func TestFoldWithTabRejected(t *testing.T) {
	block := "A: 1\r\n\tcontinuation\r\nB: 2\r\n"
	got, err := DecodeHeaderString(block)
	if !errors.Is(err, ErrLineFolding) {
		t.Fatalf("expected ErrLineFolding for HTAB fold, got: %v", err)
	}
	if got != nil {
		t.Fatalf("fold must not be spliced or kept as a new header: %+v", got)
	}
}

func TestFoldNotSplicedOrNewHeader(t *testing.T) {
	block := "A: one\r\n two\r\n"
	got, err := DecodeHeaderString(block)
	if err == nil {
		t.Fatalf("fold must be rejected, got: %+v", got)
	}
	// Even the valid first header is withheld: the gateway call fails entirely.
	if got != nil && len(got) != 0 {
		t.Fatalf("expected no decoded headers, got: %+v", got)
	}
}

func TestFoldOnFirstLineRejected(t *testing.T) {
	_, err := DecodeHeaderString(" orphan\r\n")
	if !errors.Is(err, ErrLineFolding) {
		t.Fatalf("leading fold line must be rejected, got: %v", err)
	}
}

func TestBareLFAndCRLF(t *testing.T) {
	got, err := DecodeHeaderString("A: 1\nB: 2\r\nC: 3\r\n\r\nignored")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []Header{
		{Name: "A", Value: "1"},
		{Name: "B", Value: "2"},
		{Name: "C", Value: "3"},
	}
	assertHeaders(t, got, want)
}

func TestBareLFFoldRejected(t *testing.T) {
	_, err := DecodeHeaderString("A: 1\n folded\n")
	if !errors.Is(err, ErrLineFolding) {
		t.Fatalf("fold after bare LF must be rejected, got: %v", err)
	}
}

func TestEmptyBlock(t *testing.T) {
	got, err := DecodeHeaderString("")
	if err != nil || len(got) != 0 {
		t.Fatalf("empty block: got %+v, %v", got, err)
	}
}

func TestDuplicateHeadersKeptInOrder(t *testing.T) {
	got, err := DecodeHeaderString("Set-Cookie: a=1\r\nSet-Cookie: b=2\r\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []Header{
		{Name: "Set-Cookie", Value: "a=1"},
		{Name: "Set-Cookie", Value: "b=2"},
	}
	assertHeaders(t, got, want)
}

func TestMalformedLines(t *testing.T) {
	cases := map[string]string{
		"no colon":          "BadLine\r\n",
		"colon first":       ": value\r\n",
		"space in name":     "Bad Name: value\r\n",
		"non-ascii in name": "Bäd: value\r\n",
	}
	for name, block := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeHeaderString(block); !errors.Is(err, ErrMalformedHeader) {
				t.Fatalf("expected ErrMalformedHeader, got: %v", err)
			}
		})
	}
}

func TestEmptyValueAllowed(t *testing.T) {
	got, err := DecodeHeaderString("X-Empty:\r\nX-Spaces:   \r\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []Header{
		{Name: "X-Empty", Value: ""},
		{Name: "X-Spaces", Value: ""},
	}
	assertHeaders(t, got, want)
}

func assertHeaders(t *testing.T, got, want []Header) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d headers %+v, want %d %+v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("header %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}
