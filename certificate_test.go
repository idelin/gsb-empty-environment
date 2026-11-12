package netsec_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	netsec "example.org/netsec/q0049"
)

const (
	oidA       = "1111111111111111111111111111111111111111"
	oidB       = "2222222222222222222222222222222222222222"
	oidC       = "3333333333333333333333333333333333333333"
	zeroOID    = "0000000000000000000000000000000000000000"
	testNonce  = "nonce-0123456789abcdef"
	testPusher = "Alice Example <alice@example.com>"
	testPushee = "ssh://git.example.org/repo.git"
)

func TestParseCertificateV01(t *testing.T) {
	updates := []netsec.Update{
		{Old: oidA, New: oidB, Ref: "refs/heads/main"},
		{Old: oidC, New: zeroOID, Ref: "refs/heads/old"},
	}
	certificate := buildCertificate(t, testPusher, testPushee, testNonce, updates, testKey)

	cert, err := netsec.ParseCertificate(certificate)
	if err != nil {
		t.Fatalf("ParseCertificate() error = %v", err)
	}
	if cert.Version != netsec.CertificateVersion {
		t.Fatalf("Version = %q, want %q", cert.Version, netsec.CertificateVersion)
	}
	if cert.Pusher != testPusher {
		t.Fatalf("Pusher = %q, want %q", cert.Pusher, testPusher)
	}
	if cert.Pushee != testPushee {
		t.Fatalf("Pushee = %q, want %q", cert.Pushee, testPushee)
	}
	if cert.Nonce != testNonce {
		t.Fatalf("Nonce = %q, want %q", cert.Nonce, testNonce)
	}
	if len(cert.Updates) != len(updates) {
		t.Fatalf("Updates length = %d, want %d", len(cert.Updates), len(updates))
	}
	for i := range updates {
		if !cert.Updates[i].Equal(updates[i]) {
			t.Fatalf("Updates[%d] = %#v, want %#v", i, cert.Updates[i], updates[i])
		}
	}
	if !bytes.HasSuffix(certificate, cert.Signature) {
		t.Fatal("Signature is not a suffix of the certificate")
	}
	if !bytes.HasPrefix(certificate, cert.Payload) {
		t.Fatal("Payload is not a prefix of the certificate")
	}
	if !bytes.Contains(cert.Payload, []byte("nonce "+testNonce+"\n")) {
		t.Fatal("signed payload does not contain the nonce")
	}
}

func TestParseCertificateRejectsMalformedCertificates(t *testing.T) {
	validUpdates := []netsec.Update{{Old: oidA, New: oidB, Ref: "refs/heads/main"}}
	validPayload := certificatePayload(testPusher, testPushee, testNonce, validUpdates)
	validSignature := signPayload([]byte(validPayload), testKey)

	tests := []struct {
		name    string
		cert    []byte
		wantErr error
	}{
		{name: "empty", cert: nil, wantErr: netsec.ErrEmptyCertificate},
		{name: "missing signature", cert: []byte(validPayload), wantErr: netsec.ErrMissingSignature},
		{name: "signature only", cert: validSignature, wantErr: netsec.ErrMalformedCertificate},
		{
			name: "unsupported version",
			cert: append([]byte(strings.Replace(validPayload,
				"certificate version 0.1", "certificate version 0.2", 1)), validSignature...),
			wantErr: netsec.ErrUnsupportedVersion,
		},
		{
			name: "missing pusher",
			cert: append([]byte("certificate version 0.1\n"+
				"pushee "+testPushee+"\n"+
				"nonce "+testNonce+"\n"+
				oidA+" "+oidB+" refs/heads/main\n"), validSignature...),
			wantErr: netsec.ErrMissingPusher,
		},
		{
			name: "missing pushee",
			cert: append([]byte("certificate version 0.1\n"+
				"pusher "+testPusher+"\n"+
				"nonce "+testNonce+"\n"+
				oidA+" "+oidB+" refs/heads/main\n"), validSignature...),
			wantErr: netsec.ErrMissingPushee,
		},
		{
			name: "missing nonce",
			cert: append([]byte("certificate version 0.1\n"+
				"pusher "+testPusher+"\n"+
				"pushee "+testPushee+"\n"+
				oidA+" "+oidB+" refs/heads/main\n"), validSignature...),
			wantErr: netsec.ErrMissingNonce,
		},
		{
			name: "empty pusher",
			cert: append([]byte("certificate version 0.1\n"+
				"pusher \n"+
				"pushee "+testPushee+"\n"+
				"nonce "+testNonce+"\n"+
				oidA+" "+oidB+" refs/heads/main\n"), validSignature...),
			wantErr: netsec.ErrMalformedCertificate,
		},
		{
			name: "duplicate nonce",
			cert: append([]byte("certificate version 0.1\n"+
				"pusher "+testPusher+"\n"+
				"pushee "+testPushee+"\n"+
				"nonce "+testNonce+"\n"+
				"nonce "+testNonce+"\n"+
				oidA+" "+oidB+" refs/heads/main\n"), validSignature...),
			wantErr: netsec.ErrMalformedCertificate,
		},
		{
			name: "no updates",
			cert: append([]byte("certificate version 0.1\n"+
				"pusher "+testPusher+"\n"+
				"pushee "+testPushee+"\n"+
				"nonce "+testNonce+"\n"), validSignature...),
			wantErr: netsec.ErrMalformedCertificate,
		},
		{
			name: "bad update line",
			cert: append([]byte("certificate version 0.1\n"+
				"pusher "+testPusher+"\n"+
				"pushee "+testPushee+"\n"+
				"nonce "+testNonce+"\n"+
				"not-a-ref-update\n"), validSignature...),
			wantErr: netsec.ErrMalformedCertificate,
		},
		{
			name: "unqualified ref",
			cert: append([]byte("certificate version 0.1\n"+
				"pusher "+testPusher+"\n"+
				"pushee "+testPushee+"\n"+
				"nonce "+testNonce+"\n"+
				oidA+" "+oidB+" main\n"), validSignature...),
			wantErr: netsec.ErrMalformedCertificate,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := netsec.ParseCertificate(test.cert)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ParseCertificate() error = %v, want errors.Is(%v)", err, test.wantErr)
			}
		})
	}
}

func TestParseUpdates(t *testing.T) {
	input := oidA + " " + oidB + " refs/heads/main\r\n" +
		oidC + " " + zeroOID + " refs/heads/removed\n\n"

	updates, err := netsec.ParseUpdates(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseUpdates() error = %v", err)
	}
	want := []netsec.Update{
		{Old: oidA, New: oidB, Ref: "refs/heads/main"},
		{Old: oidC, New: zeroOID, Ref: "refs/heads/removed"},
	}
	if len(updates) != len(want) {
		t.Fatalf("ParseUpdates() length = %d, want %d", len(updates), len(want))
	}
	for i := range want {
		if !updates[i].Equal(want[i]) {
			t.Fatalf("updates[%d] = %#v, want %#v", i, updates[i], want[i])
		}
	}

	if _, err := netsec.ParseUpdates(strings.NewReader("too few fields\n")); !errors.Is(err, netsec.ErrMalformedUpdate) {
		t.Fatalf("ParseUpdates() error = %v, want ErrMalformedUpdate", err)
	}
}
