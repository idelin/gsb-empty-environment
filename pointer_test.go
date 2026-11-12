package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func canonicalPointer(content []byte) string {
	return fmt.Sprintf("version %s\noid sha256:%x\nsize %d\n",
		PointerVersion, sha256.Sum256(content), len(content))
}

func objectID(content []byte) string {
	digest := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func TestParsePointer(t *testing.T) {
	content := []byte("large object contents")
	body := canonicalPointer(content)

	pointer, err := ParsePointer([]byte(body))
	if err != nil {
		t.Fatalf("ParsePointer() error = %v", err)
	}
	if pointer.Version != PointerVersion {
		t.Errorf("Version = %q, want %q", pointer.Version, PointerVersion)
	}
	if pointer.OID != objectID(content) {
		t.Errorf("OID = %q, want %q", pointer.OID, objectID(content))
	}
	if pointer.Size != int64(len(content)) {
		t.Errorf("Size = %d, want %d", pointer.Size, len(content))
	}
}

func TestParsePointerWithoutFinalNewline(t *testing.T) {
	content := []byte("contents")
	body := strings.TrimSuffix(canonicalPointer(content), "\n")

	if _, err := ParsePointer([]byte(body)); err != nil {
		t.Fatalf("ParsePointer() without final newline error = %v", err)
	}
}

func TestParsePointerRejectsNonCanonicalForms(t *testing.T) {
	content := []byte("contents")
	validOID := objectID(content)
	validSize := fmt.Sprint(len(content))
	contentDigest := sha256.Sum256(content)

	tests := []struct {
		name string
		body string
	}{
		{
			name: "version is not first",
			body: fmt.Sprintf("oid %s\nversion %s\nsize %s\n", validOID, PointerVersion, validSize),
		},
		{
			name: "size before oid",
			body: fmt.Sprintf("version %s\nsize %s\noid %s\n", PointerVersion, validSize, validOID),
		},
		{
			name: "unknown field",
			body: fmt.Sprintf("version %s\noid %s\nsize %s\nx-extra value\n", PointerVersion, validOID, validSize),
		},
		{
			name: "missing field",
			body: fmt.Sprintf("version %s\noid %s\n", PointerVersion, validOID),
		},
		{
			name: "duplicate field",
			body: fmt.Sprintf("version %s\noid %s\noid %s\n", PointerVersion, validOID, validOID),
		},
		{
			name: "unsupported version",
			body: fmt.Sprintf("version https://example.invalid/v2\noid %s\nsize %s\n", validOID, validSize),
		},
		{
			name: "tab separator",
			body: fmt.Sprintf("version %s\noid\t%s\nsize %s\n", PointerVersion, validOID, validSize),
		},
		{
			name: "extra whitespace",
			body: fmt.Sprintf("version %s\noid  %s\nsize %s\n", PointerVersion, validOID, validSize),
		},
		{
			name: "CRLF",
			body: fmt.Sprintf("version %s\r\noid %s\r\nsize %s\r\n", PointerVersion, validOID, validSize),
		},
		{
			name: "uppercase oid",
			body: fmt.Sprintf("version %s\noid SHA256:%X\nsize %s\n", PointerVersion, contentDigest, validSize),
		},
		{
			name: "short oid",
			body: fmt.Sprintf("version %s\noid sha256:%x\nsize %s\n", PointerVersion, contentDigest[:8], validSize),
		},
		{
			name: "negative size",
			body: fmt.Sprintf("version %s\noid %s\nsize -1\n", PointerVersion, validOID),
		},
		{
			name: "leading zero",
			body: fmt.Sprintf("version %s\noid %s\nsize 01\n", PointerVersion, validOID),
		},
		{
			name: "size overflow",
			body: fmt.Sprintf("version %s\noid %s\nsize 9223372036854775808\n", PointerVersion, validOID),
		},
		{
			name: "trailing blank line",
			body: canonicalPointer(content) + "\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParsePointer([]byte(test.body))
			if !errors.Is(err, ErrInvalidPointer) {
				t.Fatalf("ParsePointer() error = %v, want ErrInvalidPointer", err)
			}
		})
	}
}

func TestValidatePointerRejectsCrossRepositoryOID(t *testing.T) {
	repoAContent := []byte("repo-a private object")
	repoBContent := []byte("repo-b private object")
	allowed := NewOIDSet(objectID(repoAContent))

	_, err := ValidatePointer([]byte(canonicalPointer(repoBContent)), allowed)
	if !errors.Is(err, ErrOIDNotAuthorized) {
		t.Fatalf("ValidatePointer() error = %v, want ErrOIDNotAuthorized", err)
	}
}

func TestValidatePointerAcceptsAuthorizedOID(t *testing.T) {
	content := []byte("current repository object")
	allowed := NewOIDSet(objectID(content))

	pointer, err := ValidatePointer([]byte(canonicalPointer(content)), allowed)
	if err != nil {
		t.Fatalf("ValidatePointer() error = %v", err)
	}
	if pointer.OID != objectID(content) {
		t.Errorf("OID = %q, want %q", pointer.OID, objectID(content))
	}
}

func TestValidatePointerRejectsNilOIDSet(t *testing.T) {
	content := []byte("contents")
	_, err := ValidatePointer([]byte(canonicalPointer(content)), nil)
	if !errors.Is(err, ErrOIDNotAuthorized) {
		t.Fatalf("ValidatePointer() error = %v, want ErrOIDNotAuthorized", err)
	}
}

func TestPointerSizeLimit(t *testing.T) {
	atLimit := bytes.Repeat([]byte("x"), MaxPointerSize)
	oversized := bytes.Repeat([]byte("x"), MaxPointerSize+1)

	if _, err := ParsePointer(atLimit); !errors.Is(err, ErrInvalidPointer) {
		t.Fatalf("ParsePointer() at size limit error = %v, want ErrInvalidPointer", err)
	}
	if _, err := ParsePointer(oversized); !errors.Is(err, ErrPointerTooLarge) {
		t.Fatalf("ParsePointer() error = %v, want ErrPointerTooLarge", err)
	}
	if _, err := ReadPointer(bytes.NewReader(oversized)); !errors.Is(err, ErrPointerTooLarge) {
		t.Fatalf("ReadPointer() error = %v, want ErrPointerTooLarge", err)
	}
}

func TestReadPointerPropagatesReaderError(t *testing.T) {
	wantErr := errors.New("read failed")
	reader := &errorReader{err: wantErr}

	_, err := ReadPointer(reader)
	if !errors.Is(err, wantErr) {
		t.Fatalf("ReadPointer() error = %v, want wrapped reader error", err)
	}
}

type errorReader struct {
	err error
}

func (r *errorReader) Read([]byte) (int, error) {
	return 0, r.err
}
