package netsec

import (
	"errors"
	"testing"
)

func TestNormalizePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr error
	}{
		{name: "empty segments and dot", input: "a//./b///c/", want: "a/b/c"},
		{name: "leading separators", input: "///a/b", want: "a/b"},
		{name: "parent segment", input: "a/b/../c", want: "a/c"},
		{name: "parent at root", input: "../a", wantErr: ErrPathEscapesRoot},
		{name: "parent past root", input: "a/../../b", wantErr: ErrPathEscapesRoot},
		{name: "resolves to root", input: "a/..", wantErr: ErrInvalidPath},
		{name: "dot only", input: ".", wantErr: ErrInvalidPath},
		{name: "empty", input: "", wantErr: ErrInvalidPath},
		{name: "backslash", input: `a\b`, wantErr: ErrInvalidPath},
		{name: "NUL", input: "a\x00b", wantErr: ErrInvalidPath},
		{name: "invalid UTF-8", input: "\xff", wantErr: ErrInvalidPath},
		{name: "colon is ordinary", input: "a:b/c", want: "a:b/c"},
		{name: "unicode preserved", input: "文档/é.txt", want: "文档/é.txt"},
		{name: "no unicode normalization", input: "e\u0301.txt", want: "e\u0301.txt"},
		{name: "no case folding", input: "Bin/Tool", want: "Bin/Tool"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := NormalizePath(test.input)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("NormalizePath(%q) error = %v, want %v", test.input, err, test.wantErr)
			}
			if err == nil && got != test.want {
				t.Fatalf("NormalizePath(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestNormalizePathDoesNotFoldEquivalentLookingNames(t *testing.T) {
	t.Parallel()

	composed, err := NormalizePath("é")
	if err != nil {
		t.Fatal(err)
	}
	decomposed, err := NormalizePath("e\u0301")
	if err != nil {
		t.Fatal(err)
	}
	if composed == decomposed {
		t.Fatalf("NormalizePath unexpectedly folded %q and %q", composed, decomposed)
	}
}
