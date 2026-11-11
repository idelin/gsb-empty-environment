package gittree

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
)

func TestTamperedNamesModesAndTypes(t *testing.T) {
	f := newFixture(t, SHA1)
	v := NewVerifier(f.src)
	ctx := context.Background()

	missing := f.proof("file1.txt")
	missing.Path[0].Name = "file1-evil.txt"
	if _, err := v.VerifyFile(ctx, missing); !errors.Is(err, ErrNameMismatch) {
		t.Fatalf("missing name error = %v, want ErrNameMismatch", err)
	}

	broken := f.proof("a", "sub", "file2.txt")
	broken.Path[1].TreeID = f.root
	if _, err := v.VerifyFile(ctx, broken); !errors.Is(err, ErrInvalidProof) {
		t.Fatalf("broken chain error = %v, want ErrInvalidProof", err)
	}

	tampered := &tamperSource{
		inner: f.src,
		override: map[ObjectID]RawObject{
			f.file1: {Type: TypeBlob, Content: []byte("tampered contents")},
		},
	}
	evil := NewVerifier(tampered)
	if _, err := evil.VerifyFile(ctx, f.proof("file1.txt")); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("tampered blob error = %v, want ErrDigestMismatch", err)
	}

	wrongType := &tamperSource{
		inner: f.src,
		override: map[ObjectID]RawObject{
			f.file1: {Type: TypeTree, Content: f.file1Content},
		},
	}
	typed := NewVerifier(wrongType)
	if _, err := typed.VerifyFile(ctx, f.proof("file1.txt")); !errors.Is(err, ErrObjectType) {
		t.Fatalf("wrong object type error = %v, want ErrObjectType", err)
	}
}

func TestProofValidation(t *testing.T) {
	f := newFixture(t, SHA1)
	ctx := context.Background()
	v := NewVerifier(f.src)

	cases := []struct {
		name  string
		proof *MembershipProof
		want  error
	}{
		{"empty path", &MembershipProof{RootID: f.root}, ErrInvalidProof},
		{"bad root id", &MembershipProof{RootID: "abcd", Path: []PathStep{{Name: "x", TreeID: f.root}}}, ErrInvalidProof},
		{"bad step id", &MembershipProof{RootID: f.root, Path: []PathStep{{Name: "x", TreeID: "nope"}}}, ErrInvalidProof},
		{"slash in name", &MembershipProof{RootID: f.root, Path: []PathStep{{Name: "a/b", TreeID: f.root}}}, ErrInvalidProof},
		{"dotdot name", &MembershipProof{RootID: f.root, Path: []PathStep{{Name: "..", TreeID: f.root}}}, ErrInvalidProof},
		{"empty name", &MembershipProof{RootID: f.root, Path: []PathStep{{Name: "", TreeID: f.root}}}, ErrInvalidProof},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := v.VerifyEntry(ctx, tc.proof); !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestMixedAlgorithmsRejected(t *testing.T) {
	f1 := newFixture(t, SHA1)
	f256 := newFixture(t, SHA256)

	mixed := &MembershipProof{
		RootID: f1.root,
		Path: []PathStep{
			{Name: "a", TreeID: f1.root},
			{Name: "sub", TreeID: f256.dirA},
		},
	}
	v := NewVerifier(f1.src)
	if _, err := v.VerifyEntry(context.Background(), mixed); !errors.Is(err, ErrAlgorithmMismatch) {
		t.Fatalf("mixed algorithm proof error = %v, want ErrAlgorithmMismatch", err)
	}
}

func TestMissingObjectsAndSubtreeLeaf(t *testing.T) {
	f := newFixture(t, SHA1)
	empty := NewVerifier(NewMemorySource())
	ctx := context.Background()

	if _, err := empty.VerifyFile(ctx, f.proof("file1.txt")); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("missing root error = %v, want ErrObjectNotFound", err)
	}

	v := NewVerifier(f.src)
	dir, err := v.VerifyEntry(ctx, f.proof("a"))
	if err != nil {
		t.Fatalf("VerifyEntry(a): %v", err)
	}
	if !dir.IsTree || dir.ID != f.dirA {
		t.Fatalf("directory leaf = %+v", dir)
	}
	if _, err := v.VerifyFile(ctx, f.proof("a")); !errors.Is(err, ErrSymlinkNotFile) {
		t.Fatalf("VerifyFile(directory) error = %v, want ErrSymlinkNotFile", err)
	}
}

func TestMalformedTreesRejected(t *testing.T) {
	blob := HashObject(SHA1, TypeBlob, []byte("x"))
	idBytes, err := hex.DecodeString(string(blob))
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string][]byte{
		"missing space":     append([]byte("100644"), 0),
		"unknown mode":      append(append([]byte("100666 x"), 0), idBytes...),
		"leading zero mode": append(append([]byte("0100644 x"), 0), idBytes...),
		"truncated id":      append(append([]byte("100644 x"), 0), idBytes[:10]...),
		"missing nul":       append([]byte("100644 x"), idBytes...),
		"out of order":      outOfOrderTree(blob),
	}

	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			id := HashObject(SHA1, TypeTree, content)
			if _, err := ParseTree(id, content); err == nil {
				t.Fatalf("ParseTree accepted malformed content for %s", name)
			}
		})
	}
}

func outOfOrderTree(blob ObjectID) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "%s z\x00", ModeRegular)
	idBytes, _ := hex.DecodeString(string(blob))
	buf.Write(idBytes)
	fmt.Fprintf(&buf, "%s a\x00", ModeRegular)
	buf.Write(idBytes)
	return buf.Bytes()
}

func TestParseModeAndValidateName(t *testing.T) {
	if m, err := parseMode("100755"); err != nil || m != ModeExecutable {
		t.Fatalf("parseMode(100755) = %v, %v", m, err)
	}
	if m, err := parseMode("40000"); err != nil || m != ModeTree {
		t.Fatalf("parseMode(40000) = %v, %v", m, err)
	}
	for _, bad := range []string{"040000", "100666", "123", ""} {
		if _, err := parseMode(bad); !errors.Is(err, ErrUnknownMode) {
			t.Fatalf("parseMode(%q) error = %v, want ErrUnknownMode", bad, err)
		}
	}
	for _, bad := range []string{"", ".", "..", "a/b", "with\x00nul", stringsRepeat(256)} {
		if err := ValidateName(bad); !errors.Is(err, ErrInvalidName) {
			t.Fatalf("ValidateName(%q) error = %v, want ErrInvalidName", bad, err)
		}
	}
	if err := ValidateName("normal-name_1.0"); err != nil {
		t.Fatalf("valid name rejected: %v", err)
	}
}

func stringsRepeat(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}

func TestTreeOrderingWithTrailingSlash(t *testing.T) {
	// Git sorts tree "foo" as "foo/": foo.txt (file) must come after foo/.
	blob := HashObject(SHA1, TypeBlob, []byte("x"))
	entries := []Entry{
		{Mode: ModeRegular, Name: "foo.txt", ID: blob},
		{Mode: ModeTree, Name: "foo", ID: EmptyTreeID},
	}
	raw, err := MarshalTree(SHA1, entries)
	if err != nil {
		t.Fatalf("MarshalTree: %v", err)
	}
	id := HashObject(SHA1, TypeTree, raw)
	tree, err := ParseTree(id, raw)
	if err != nil {
		t.Fatalf("ParseTree: %v", err)
	}
	if tree.Entries[0].Name != "foo.txt" || tree.Entries[1].Name != "foo" {
		t.Fatalf("unexpected order: %+v", tree.Entries)
	}
}
