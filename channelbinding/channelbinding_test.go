package channelbinding

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"testing"
)

// fakeExporter is a test double for the TLS exporter (RFC 5705). Each
// instance carries its own per-connection secret, so material and
// handshake keys differ across connections like real TLS sessions.
type fakeExporter struct {
	connID        string
	secret        []byte
	emptyMaterial bool
	failExport    bool
}

func newFakeExporter(connID string) *fakeExporter {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		panic(err)
	}
	return &fakeExporter{connID: connID, secret: secret}
}

// derive mimics TLS EKM: output is a keyed function of label, context and
// length, bound to this connection's secret.
func (f *fakeExporter) derive(label string, context []byte, length int) []byte {
	mac := hmac.New(sha256.New, f.secret)
	mac.Write([]byte(label))
	mac.Write([]byte{0})
	mac.Write(context)
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(length))
	mac.Write(lenBuf[:])
	return mac.Sum(nil)[:length]
}

func (f *fakeExporter) ExportKeyingMaterial(label string, context []byte, length int) ([]byte, error) {
	if f.failExport {
		return nil, fmt.Errorf("fake exporter %s: forced failure", f.connID)
	}
	if f.emptyMaterial {
		return nil, nil
	}
	return f.derive(label, context, length), nil
}

func (f *fakeExporter) HandshakeKey() []byte {
	if f.emptyMaterial {
		return nil
	}
	return f.derive("EXPORTER-handshake-instance", nil, materialLen)
}

type fakeTransport struct {
	typ Type
	exp Exporter
}

func (t *fakeTransport) BindingType() Type  { return t.typ }
func (t *fakeTransport) Exporter() Exporter { return t.exp }

// recordingExporter captures the label/context handed to the exporter so
// tests can assert the type-to-label mapping and identity binding.
type recordingExporter struct {
	*fakeExporter
	gotLabel   string
	gotContext []byte
	gotLength  int
}

func (r *recordingExporter) ExportKeyingMaterial(label string, context []byte, length int) ([]byte, error) {
	r.gotLabel = label
	r.gotContext = append([]byte(nil), context...)
	r.gotLength = length
	return r.fakeExporter.ExportKeyingMaterial(label, context, length)
}

func newConnection(typ Type, connID string) (*fakeTransport, *fakeExporter) {
	exp := newFakeExporter(connID)
	return &fakeTransport{typ: typ, exp: exp}, exp
}

const serviceID = "imap/host.example.org"

func mustBind(t *testing.T, tr Transport, service string) *Evidence {
	t.Helper()
	ev, err := Bind(tr, service)
	if err != nil {
		t.Fatalf("Bind(%s) failed: %v", tr.BindingType(), err)
	}
	return ev
}

func mustBound(t *testing.T, status Status, err error) {
	t.Helper()
	if err != nil || status != StatusBound {
		t.Fatalf("want bound, got status=%s err=%v", status, err)
	}
}

// assertNoFallback enforces the central rule: every failure carries a
// non-nil error and is never reported as an unbound session.
func assertNoFallback(t *testing.T, status Status, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("failure path must return an error, got status=%s", status)
	}
	if status == StatusUnbound {
		t.Fatalf("failure path must not fall back to unbound, err=%v", err)
	}
}

func TestThreeWayNegotiation(t *testing.T) {
	policy := Policy{Required: TLSExporter, ServiceID: serviceID}

	t.Run("three parties agree", func(t *testing.T) {
		tr, _ := newConnection(TLSExporter, "conn-1")
		ev := mustBind(t, tr, serviceID)
		status, err := policy.Verify(Declaration{Type: TLSExporter}, ev, tr)
		mustBound(t, status, err)
	})

	t.Run("server-end-point happy path", func(t *testing.T) {
		tr, _ := newConnection(TLSServerEndPoint, "conn-1")
		p := Policy{Required: TLSServerEndPoint, ServiceID: serviceID}
		ev := mustBind(t, tr, serviceID)
		status, err := p.Verify(Declaration{Type: TLSServerEndPoint}, ev, tr)
		mustBound(t, status, err)
	})

	t.Run("transport offers a different type", func(t *testing.T) {
		tr, _ := newConnection(TLSServerEndPoint, "conn-1")
		ev := mustBind(t, tr, serviceID)
		status, err := policy.Verify(Declaration{Type: TLSExporter}, ev, tr)
		assertNoFallback(t, status, err)
		if status != StatusUnsupported || !errors.Is(err, ErrTypeMismatch) {
			t.Fatalf("want unsupported/ErrTypeMismatch, got %s/%v", status, err)
		}
	})

	t.Run("client declares a type the server does not require", func(t *testing.T) {
		tr, _ := newConnection(TLSServerEndPoint, "conn-1")
		ev := mustBind(t, tr, serviceID)
		status, err := policy.Verify(Declaration{Type: TLSServerEndPoint}, ev, tr)
		assertNoFallback(t, status, err)
		if status != StatusUnsupported || !errors.Is(err, ErrUnsupportedBinding) {
			t.Fatalf("want unsupported/ErrUnsupportedBinding, got %s/%v", status, err)
		}
	})

	t.Run("unknown binding type", func(t *testing.T) {
		tr, _ := newConnection(Type("tls-imaginary"), "conn-1")
		status, err := policy.Verify(
			Declaration{Type: Type("tls-imaginary")},
			&Evidence{Type: Type("tls-imaginary"), Material: []byte("x")},
			tr,
		)
		assertNoFallback(t, status, err)
		if status != StatusUnsupported || !errors.Is(err, ErrUnsupportedBinding) {
			t.Fatalf("want unsupported, got %s/%v", status, err)
		}
	})

	t.Run("typed binding required but client declares none", func(t *testing.T) {
		status, err := policy.Verify(Declaration{Type: None}, nil, nil)
		assertNoFallback(t, status, err)
		if status != StatusInvalid || !errors.Is(err, ErrBindingRequired) {
			t.Fatalf("want invalid/ErrBindingRequired, got %s/%v", status, err)
		}
	})

	t.Run("explicit unbound client allowed without a requirement", func(t *testing.T) {
		p := Policy{AllowNone: true, ServiceID: serviceID}
		status, err := p.Verify(Declaration{Type: None}, nil, nil)
		if err != nil || status != StatusUnbound {
			t.Fatalf("want unbound, got %s/%v", status, err)
		}
	})

	t.Run("unbound declaration with smuggled evidence is rejected", func(t *testing.T) {
		tr, _ := newConnection(TLSExporter, "conn-1")
		ev := mustBind(t, tr, serviceID)
		p := Policy{AllowNone: true, ServiceID: serviceID}
		status, err := p.Verify(Declaration{Type: None}, ev, tr)
		assertNoFallback(t, status, err)
		if status != StatusInvalid || !errors.Is(err, ErrEmptyBinding) {
			t.Fatalf("want invalid/ErrEmptyBinding, got %s/%v", status, err)
		}
	})

	t.Run("nil transport for a typed declaration", func(t *testing.T) {
		status, err := policy.Verify(Declaration{Type: TLSExporter},
			&Evidence{Type: TLSExporter, Material: []byte("x")}, nil)
		assertNoFallback(t, status, err)
		if status != StatusUnsupported || !errors.Is(err, ErrNoTransport) {
			t.Fatalf("want unsupported/ErrNoTransport, got %s/%v", status, err)
		}
	})
}

func TestTypeSwap(t *testing.T) {
	policy := Policy{Required: TLSExporter, ServiceID: serviceID}

	t.Run("evidence derived under one type presented under another", func(t *testing.T) {
		// Both binding views sit on the same TLS handshake, so the
		// handshake key matches and only the type swap can be at fault.
		shared := newFakeExporter("conn-1")
		trExporter := &fakeTransport{typ: TLSExporter, exp: shared}
		trEndpoint := &fakeTransport{typ: TLSServerEndPoint, exp: shared}
		ev := mustBind(t, trEndpoint, serviceID)
		ev.Type = TLSExporter
		status, err := policy.Verify(Declaration{Type: TLSExporter}, ev, trExporter)
		assertNoFallback(t, status, err)
		if status != StatusInvalid || !errors.Is(err, ErrBindingVerification) {
			t.Fatalf("want invalid/ErrBindingVerification, got %s/%v", status, err)
		}
	})

	t.Run("evidence type field disagrees with declaration", func(t *testing.T) {
		tr, _ := newConnection(TLSExporter, "conn-1")
		ev := mustBind(t, tr, serviceID)
		ev.Type = TLSServerEndPoint
		status, err := policy.Verify(Declaration{Type: TLSExporter}, ev, tr)
		assertNoFallback(t, status, err)
		if status != StatusInvalid || !errors.Is(err, ErrTypeMismatch) {
			t.Fatalf("want invalid/ErrTypeMismatch, got %s/%v", status, err)
		}
	})

	t.Run("exporter labels are type-specific and bind the identity", func(t *testing.T) {
		rec := &recordingExporter{fakeExporter: newFakeExporter("conn-1")}
		tr := &fakeTransport{typ: TLSExporter, exp: rec}
		mustBind(t, tr, serviceID)
		if rec.gotLabel != "EXPORTER-Channel-Binding" {
			t.Fatalf("tls-exporter label = %q", rec.gotLabel)
		}
		if string(rec.gotContext) != serviceID {
			t.Fatalf("service identity not passed as context: %q", rec.gotContext)
		}

		rec2 := &recordingExporter{fakeExporter: newFakeExporter("conn-1")}
		tr2 := &fakeTransport{typ: TLSServerEndPoint, exp: rec2}
		mustBind(t, tr2, serviceID)
		if rec2.gotLabel != "EXPORTER-tls-server-end-point" {
			t.Fatalf("tls-server-end-point label = %q", rec2.gotLabel)
		}
		if rec2.gotLength != materialLen {
			t.Fatalf("material length = %d, want %d", rec2.gotLength, materialLen)
		}
	})
}

func TestEmptyValues(t *testing.T) {
	policy := Policy{Required: TLSExporter, ServiceID: serviceID}

	t.Run("nil evidence", func(t *testing.T) {
		tr, _ := newConnection(TLSExporter, "conn-1")
		status, err := policy.Verify(Declaration{Type: TLSExporter}, nil, tr)
		assertNoFallback(t, status, err)
		if status != StatusInvalid || !errors.Is(err, ErrEmptyBinding) {
			t.Fatalf("want invalid/ErrEmptyBinding, got %s/%v", status, err)
		}
	})

	t.Run("empty material", func(t *testing.T) {
		tr, _ := newConnection(TLSExporter, "conn-1")
		ev := mustBind(t, tr, serviceID)
		ev.Material = nil
		status, err := policy.Verify(Declaration{Type: TLSExporter}, ev, tr)
		assertNoFallback(t, status, err)
		if !errors.Is(err, ErrEmptyBinding) {
			t.Fatalf("want ErrEmptyBinding, got %v", err)
		}
	})

	t.Run("empty handshake key", func(t *testing.T) {
		tr, _ := newConnection(TLSExporter, "conn-1")
		ev := mustBind(t, tr, serviceID)
		ev.HandshakeKey = nil
		status, err := policy.Verify(Declaration{Type: TLSExporter}, ev, tr)
		assertNoFallback(t, status, err)
		if !errors.Is(err, ErrEmptyBinding) {
			t.Fatalf("want ErrEmptyBinding, got %v", err)
		}
	})

	t.Run("exporter returns empty material", func(t *testing.T) {
		exp := newFakeExporter("conn-empty")
		exp.emptyMaterial = true
		tr := &fakeTransport{typ: TLSExporter, exp: exp}
		if _, err := Bind(tr, serviceID); !errors.Is(err, ErrEmptyBinding) {
			t.Fatalf("Bind empty exporter: want ErrEmptyBinding, got %v", err)
		}
		status, err := policy.Verify(Declaration{Type: TLSExporter},
			&Evidence{Type: TLSExporter, Material: []byte{}, HandshakeKey: []byte{}}, tr)
		assertNoFallback(t, status, err)
		if !errors.Is(err, ErrEmptyBinding) {
			t.Fatalf("verify empty evidence: want ErrEmptyBinding, got %v", err)
		}
	})

	t.Run("bind on nil transport", func(t *testing.T) {
		if _, err := Bind(nil, serviceID); !errors.Is(err, ErrNoTransport) {
			t.Fatalf("want ErrNoTransport, got %v", err)
		}
	})

	t.Run("bind on transport with unknown type", func(t *testing.T) {
		tr := &fakeTransport{typ: Type("tls-imaginary"), exp: newFakeExporter("c")}
		if _, err := Bind(tr, serviceID); !errors.Is(err, ErrUnsupportedBinding) {
			t.Fatalf("want ErrUnsupportedBinding, got %v", err)
		}
	})

	t.Run("exporter failure surfaces as verification error", func(t *testing.T) {
		exp := newFakeExporter("conn-fail")
		exp.failExport = true
		tr := &fakeTransport{typ: TLSExporter, exp: exp}
		if _, err := Bind(tr, serviceID); !errors.Is(err, ErrBindingVerification) {
			t.Fatalf("want ErrBindingVerification, got %v", err)
		}
	})
}

func TestCrossConnectionReuse(t *testing.T) {
	policy := Policy{Required: TLSExporter, ServiceID: serviceID}

	t.Run("evidence from another connection is rejected", func(t *testing.T) {
		trA, _ := newConnection(TLSExporter, "conn-A")
		trB, _ := newConnection(TLSExporter, "conn-B")
		ev := mustBind(t, trA, serviceID)

		status, err := policy.Verify(Declaration{Type: TLSExporter}, ev, trB)
		assertNoFallback(t, status, err)
		if status != StatusInvalid || !errors.Is(err, ErrHandshakeMismatch) {
			t.Fatalf("want invalid/ErrHandshakeMismatch, got %s/%v", status, err)
		}

		// The original connection still accepts its own evidence.
		status, err = policy.Verify(Declaration{Type: TLSExporter}, ev, trA)
		mustBound(t, status, err)
	})

	t.Run("handshake key copied from the live connection with stale material", func(t *testing.T) {
		trA, _ := newConnection(TLSExporter, "conn-A")
		trB, _ := newConnection(TLSExporter, "conn-B")
		ev := mustBind(t, trA, serviceID)
		// Attacker reuses B's handshake key so the instance check passes,
		// but cannot produce B's exporter material.
		ev.HandshakeKey = trB.exp.HandshakeKey()
		status, err := policy.Verify(Declaration{Type: TLSExporter}, ev, trB)
		assertNoFallback(t, status, err)
		if status != StatusInvalid || !errors.Is(err, ErrBindingVerification) {
			t.Fatalf("want invalid/ErrBindingVerification, got %s/%v", status, err)
		}
	})

	t.Run("same evidence type across connections never matches both", func(t *testing.T) {
		trA, expA := newConnection(TLSExporter, "conn-A")
		trB, _ := newConnection(TLSExporter, "conn-B")
		ev := mustBind(t, trA, serviceID)
		if hmac.Equal(expA.HandshakeKey(), trB.exp.HandshakeKey()) {
			t.Fatal("per-connection handshake keys must differ")
		}
		if _, err := policy.Verify(Declaration{Type: TLSExporter}, ev, trB); err == nil {
			t.Fatal("replayed evidence unexpectedly accepted")
		}
	})
}

func TestServiceIdentityBinding(t *testing.T) {
	policy := Policy{Required: TLSExporter, ServiceID: serviceID}

	t.Run("evidence bound to another service is rejected", func(t *testing.T) {
		tr, _ := newConnection(TLSExporter, "conn-1")
		ev := mustBind(t, tr, "ldap/other.example.net")
		status, err := policy.Verify(Declaration{Type: TLSExporter}, ev, tr)
		assertNoFallback(t, status, err)
		if status != StatusInvalid || !errors.Is(err, ErrServiceIdentity) {
			t.Fatalf("want invalid/ErrServiceIdentity, got %s/%v", status, err)
		}
	})

	t.Run("matching identity passes", func(t *testing.T) {
		tr, _ := newConnection(TLSExporter, "conn-1")
		ev := mustBind(t, tr, serviceID)
		status, err := policy.Verify(Declaration{Type: TLSExporter}, ev, tr)
		mustBound(t, status, err)
	})

	t.Run("tampered material is a verification failure", func(t *testing.T) {
		tr, _ := newConnection(TLSExporter, "conn-1")
		ev := mustBind(t, tr, serviceID)
		ev.Material[0] ^= 0xFF
		status, err := policy.Verify(Declaration{Type: TLSExporter}, ev, tr)
		assertNoFallback(t, status, err)
		if status != StatusInvalid || !errors.Is(err, ErrBindingVerification) {
			t.Fatalf("want invalid/ErrBindingVerification, got %s/%v", status, err)
		}
	})
}

func TestUnsupportedAndInvalidAreDistinct(t *testing.T) {
	policy := Policy{Required: TLSExporter, ServiceID: serviceID}

	// Unsupported: the type negotiation itself fails.
	tr, _ := newConnection(TLSServerEndPoint, "conn-1")
	status, err := policy.Verify(
		Declaration{Type: TLSServerEndPoint},
		&Evidence{Type: TLSServerEndPoint, Material: []byte("ok"), HandshakeKey: []byte("k")},
		tr,
	)
	if status != StatusUnsupported || !errors.Is(err, ErrUnsupportedBinding) {
		t.Fatalf("type mismatch: want unsupported, got %s/%v", status, err)
	}

	// Invalid: types agree, only the data is wrong (empty here).
	tr2, _ := newConnection(TLSExporter, "conn-1")
	status, err = policy.Verify(Declaration{Type: TLSExporter},
		&Evidence{Type: TLSExporter, Material: nil, HandshakeKey: nil}, tr2)
	if status != StatusInvalid || !errors.Is(err, ErrEmptyBinding) {
		t.Fatalf("bad data: want invalid, got %s/%v", status, err)
	}

	// Package-level Negotiate delegates to Policy.Verify.
	tr3, _ := newConnection(TLSExporter, "conn-1")
	ev := mustBind(t, tr3, serviceID)
	status, err = Negotiate(policy, Declaration{Type: TLSExporter}, ev, tr3)
	mustBound(t, status, err)
}

func TestStatusAndTypeNames(t *testing.T) {
	if None.String() != "none" || TLSExporter.String() != "tls-exporter" ||
		TLSServerEndPoint.String() != "tls-server-end-point" {
		t.Fatal("unexpected Type names")
	}
	names := map[Status]string{
		StatusUnbound:     "unbound",
		StatusBound:       "bound",
		StatusUnsupported: "unsupported",
		StatusInvalid:     "invalid",
	}
	for st, want := range names {
		if st.String() != want {
			t.Fatalf("status %d name = %q, want %q", int(st), st.String(), want)
		}
	}
}

func TestPolicySupportedRestriction(t *testing.T) {
	// The server only accepts tls-server-end-point even though it does not
	// mandate it; a tls-exporter client on a matching transport is refused
	// at negotiation time, never downgraded.
	policy := Policy{Supported: []Type{TLSServerEndPoint}, ServiceID: serviceID}
	tr, _ := newConnection(TLSExporter, "conn-1")
	ev := mustBind(t, tr, serviceID)
	status, err := policy.Verify(Declaration{Type: TLSExporter}, ev, tr)
	assertNoFallback(t, status, err)
	if status != StatusUnsupported || !errors.Is(err, ErrUnsupportedBinding) {
		t.Fatalf("want unsupported/ErrUnsupportedBinding, got %s/%v", status, err)
	}

	// The single supported type still works end to end.
	tr2, _ := newConnection(TLSServerEndPoint, "conn-2")
	ev2 := mustBind(t, tr2, serviceID)
	status, err = policy.Verify(Declaration{Type: TLSServerEndPoint}, ev2, tr2)
	mustBound(t, status, err)
}

func TestMisconfiguredRequiredType(t *testing.T) {
	// A required type absent from the supported list is a policy error and
	// must reject an otherwise perfect binding.
	policy := Policy{
		Required:  TLSExporter,
		Supported: []Type{TLSServerEndPoint},
		ServiceID: serviceID,
	}
	tr, _ := newConnection(TLSExporter, "conn-1")
	ev := mustBind(t, tr, serviceID)
	status, err := policy.Verify(Declaration{Type: TLSExporter}, ev, tr)
	assertNoFallback(t, status, err)
	if status != StatusUnsupported || !errors.Is(err, ErrUnsupportedBinding) {
		t.Fatalf("want unsupported/ErrUnsupportedBinding, got %s/%v", status, err)
	}
}

func TestVerifySideExporterFailure(t *testing.T) {
	policy := Policy{Required: TLSExporter, ServiceID: serviceID}
	tr, _ := newConnection(TLSExporter, "conn-1")
	ev := mustBind(t, tr, serviceID)

	// The handshake-key check passes against the same connection, but the
	// re-derivation call fails: a verification error, not a downgrade.
	tr.exp.(*fakeExporter).failExport = true
	status, err := policy.Verify(Declaration{Type: TLSExporter}, ev, tr)
	assertNoFallback(t, status, err)
	if status != StatusInvalid || !errors.Is(err, ErrBindingVerification) {
		t.Fatalf("want invalid/ErrBindingVerification, got %s/%v", status, err)
	}
}

func TestNilExporterOnTransport(t *testing.T) {
	policy := Policy{Required: TLSExporter, ServiceID: serviceID}

	// Client-side bind without an exporter.
	if _, err := Bind(&fakeTransport{typ: TLSExporter}, serviceID); !errors.Is(err, ErrNoTransport) {
		t.Fatalf("bind nil exporter: want ErrNoTransport, got %v", err)
	}

	// Server-side verify against a typed transport missing its exporter.
	ev := &Evidence{
		Type:         TLSExporter,
		ServiceID:    serviceID,
		HandshakeKey: []byte("k"),
		Material:     []byte("m"),
	}
	status, err := policy.Verify(Declaration{Type: TLSExporter}, ev, &fakeTransport{typ: TLSExporter})
	assertNoFallback(t, status, err)
	if status != StatusInvalid || !errors.Is(err, ErrNoTransport) {
		t.Fatalf("verify nil exporter: want invalid/ErrNoTransport, got %s/%v", status, err)
	}
}
