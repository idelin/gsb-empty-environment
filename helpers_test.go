package netsec

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const pinnedCommit = "0123456789abcdef0123456789abcdef01234567"

func testCatalog(t *testing.T, files []FileInfo) *FileCatalog {
	t.Helper()
	catalog, err := NewFileCatalog(files)
	if err != nil {
		t.Fatalf("NewFileCatalog: %v", err)
	}
	return catalog
}

func assertViolation(t *testing.T, err error, reason Reason) Violation {
	t.Helper()
	if err == nil {
		t.Fatalf("expected admission error with reason %q, got nil", reason)
	}
	var admissionErr *AdmissionError
	if !errors.As(err, &admissionErr) {
		t.Fatalf("expected *AdmissionError, got %T: %v", err, err)
	}
	if !admissionErr.HasReason(reason) {
		t.Fatalf("expected violation reason %q, got %+v", reason, admissionErr.Violations)
	}
	return admissionErr.Violations[0]
}

func assertAllowed(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("expected admission to pass, got: %v", err)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return encoded
}

func fieldPrefix(field string) string {
	if index := strings.Index(field, "["); index >= 0 {
		return field[:index]
	}
	return field
}
