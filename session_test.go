package featureneg

import (
	"encoding/binary"
	"errors"
	"testing"
)

func newSession(t *testing.T) (*Session, Outcome) {
	t.Helper()
	mk := func(party string) *Offer {
		return &Offer{
			Party:      party,
			Protocol:   Range{1, 4},
			Required:   []string{"base"},
			Optional:   []string{"zip", "telemetry"},
			Preference: []string{"zip"},
			Features: map[string]Feature{
				"base":      feat("base", 1, 3, nil, nil),
				"zip":       feat("zip", 1, 2, nil, nil),
				"telemetry": feat("telemetry", 1, 1, nil, nil),
			},
		}
	}
	sess := &Session{ID: [16]byte{9, 9, 9}, Local: mk("a"), Remote: mk("b")}
	out := sess.Negotiate(0)
	if out.Status != OK {
		t.Fatalf("setup negotiation failed: %v %v", out.Status, out.Reasons)
	}
	return sess, out
}

func frame(payload []byte) []byte {
	buf := make([]byte, 0, 4+len(payload))
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(payload)))
	return append(buf, payload...)
}

func TestDecodeBoundToCertificate(t *testing.T) {
	sess, out := newSession(t)
	dec, err := sess.BindDecoder(out.Certificate)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	got, err := dec.Decode(frame([]byte("hello")))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("payload = %q", got)
	}
	if _, err := dec.Decode([]byte{0, 0, 1}); err == nil {
		t.Fatal("expected error for truncated frame")
	}
}

// A retry or downgrade produces a new transcript; certificates issued
// for the old parameters must not decode new data.
func TestOldCertificateRejectedAfterRetry(t *testing.T) {
	sess, out := newSession(t)
	oldCert := out.Certificate

	revised := *sess.Local
	revised.Optional = []string{"zip"} // drop telemetry
	if err := sess.RequestDowngrade(&revised); err != nil {
		t.Fatalf("downgrade: %v", err)
	}
	if err := sess.VerifyCertificate(oldCert); !errors.Is(err, ErrStaleCertificate) {
		t.Fatalf("old certificate: err = %v, want ErrStaleCertificate", err)
	}
	if _, err := sess.BindDecoder(oldCert); !errors.Is(err, ErrStaleCertificate) {
		t.Fatalf("bind with old certificate: err = %v", err)
	}

	out2 := sess.Negotiate(0)
	if out2.Status != OK {
		t.Fatalf("renegotiation failed: %v", out2.Status)
	}
	if out2.Certificate.Transcript == oldCert.Transcript {
		t.Fatal("transcript did not change after downgrade")
	}
	if err := sess.VerifyCertificate(out2.Certificate); err != nil {
		t.Fatalf("new certificate rejected: %v", err)
	}
	for _, s := range out2.Selected {
		if s.Name == "telemetry" {
			t.Fatal("downgraded session still selects telemetry")
		}
	}
}

func TestDowngradeCannotRemoveRequired(t *testing.T) {
	sess, _ := newSession(t)
	revised := *sess.Local
	revised.Required = nil // silently dropping required capability
	if err := sess.RequestDowngrade(&revised); !errors.Is(err, ErrDowngradeRemovesRequired) {
		t.Fatalf("err = %v, want ErrDowngradeRemovesRequired", err)
	}
}

func TestDowngradeCannotAddOptional(t *testing.T) {
	sess, _ := newSession(t)
	revised := *sess.Local
	revised.Optional = append(append([]string{}, sess.Local.Optional...), "extra")
	if err := sess.RequestDowngrade(&revised); !errors.Is(err, ErrDowngradeAddsOptional) {
		t.Fatalf("err = %v, want ErrDowngradeAddsOptional", err)
	}
}
