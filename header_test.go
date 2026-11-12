package netsec

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestValidateTLSRequirement(t *testing.T) {
	cases := []struct {
		name       string
		header     string
		requireTLS bool
		wantErr    error
	}{
		{name: "absent without parameter", header: "", requireTLS: false},
		{name: "absent with parameter", header: "", requireTLS: true},
		{name: "No without parameter", header: "TLS-Required: No\r\n", requireTLS: false},
		{name: "No with parameter", header: "TLS-Required: No\r\n", requireTLS: true, wantErr: ErrTLSRequiredHeaderConflict},
		{name: "Require with parameter", header: "TLS-Required: Require\r\n", requireTLS: true},
		{name: "Require without parameter", header: "TLS-Required: Require\r\n", requireTLS: false, wantErr: ErrTLSRequiredWithoutParameter},
		{name: "unknown value", header: "TLS-Required: Maybe\r\n", requireTLS: false, wantErr: ErrMalformedTLSRequiredHeader},
		{name: "duplicate fields", header: "TLS-Required: Require\r\nTLS-Required: Require\r\n", requireTLS: true, wantErr: ErrMalformedTLSRequiredHeader},
		{name: "folded No", header: "TLS-Required:\r\n No\r\n", requireTLS: false},
		{name: "unrelated folded field", header: "Subject: hello\r\n world\r\nTLS-Required: No\r\n", requireTLS: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte("From: sender@example.org\r\n" + tc.header + "\r\nbody\r\n")
			err := ValidateTLSRequirement(data, tc.requireTLS)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("ValidateTLSRequirement = %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ValidateTLSRequirement = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestPrepareMessageAddsRequireHeader(t *testing.T) {
	msg := []byte("From: sender@example.org\r\nSubject: x\r\n\r\nbody\r\n")
	out, err := PrepareMessage(msg, true)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte("TLS-Required: Require\r\n")) {
		t.Fatalf("header missing:\n%s", out)
	}
	if strings.Count(strings.ToLower(string(out)), "tls-required:") != 1 {
		t.Fatalf("unexpected header count:\n%s", out)
	}
	if !bytes.HasSuffix(out, []byte("\r\n\r\nbody\r\n")) {
		t.Fatalf("body changed:\n%s", out)
	}

	out2, err := PrepareMessage(out, true)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(bytes.ToLower(out2), []byte("tls-required:")) != 1 {
		t.Fatalf("PrepareMessage duplicated the header:\n%s", out2)
	}
}

func TestPrepareMessagePreservesNoHeaderWhenAllowed(t *testing.T) {
	msg := []byte("From: sender@example.org\r\nTLS-Required: No\r\n\r\nbody\r\n")
	out, err := PrepareMessage(msg, false)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, msg) {
		t.Fatalf("message changed unexpectedly:\n%s", out)
	}
}

func TestPrepareMessageUsesLFSeparator(t *testing.T) {
	msg := []byte("From: sender@example.org\nSubject: x\n\nbody\n")
	out, err := PrepareMessage(msg, true)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte("TLS-Required: Require\n")) {
		t.Fatalf("header missing:\n%s", out)
	}
}
