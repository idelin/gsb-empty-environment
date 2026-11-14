package netsec

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"testing"
)

func digestOf(s string) Digest { return Digest(sha256.Sum256([]byte(s))) }

func mustExport(t *testing.T, f *Facade, label string, ctx []byte, length int) []byte {
	t.Helper()
	v, err := f.Export(label, ctx, length)
	if err != nil {
		t.Fatalf("Export(%q): %v", label, err)
	}
	return v
}

// Two virtual hosts coalesced onto one socket must never share an exporter,
// and routing back to a host must return its own cached value.
func TestSharedSocketTwoVirtualHosts(t *testing.T) {
	f := NewFacade(nil)
	f.BeginHandshake(digestOf("coalesced-conn"))
	hostA := Identity{ECHOuterName: "a.example", CertID: "cert-wildcard", ALPN: "h2"}
	hostB := Identity{ECHOuterName: "b.example", CertID: "cert-wildcard", ALPN: "h2"}

	f.Freeze(hostA)
	va := mustExport(t, f, "app/token", nil, 32)

	f.Freeze(hostB)
	vb := mustExport(t, f, "app/token", nil, 32)
	if bytes.Equal(va, vb) {
		t.Fatal("same exporter served to two virtual hosts on one socket")
	}

	f.Freeze(hostA)
	if va2 := mustExport(t, f, "app/token", nil, 32); !bytes.Equal(va, va2) {
		t.Fatal("cache returned another host's exporter after re-routing")
	}
}

// The same PSK identity resumed under different certificates must yield
// different exporters.
func TestSamePSKAcrossCertificates(t *testing.T) {
	f := NewFacade(nil)

	f.BeginHandshake(digestOf("resumption-1"))
	f.Freeze(Identity{ECHOuterName: "a.example", CertID: "cert-a", ALPN: "h2", PSKIdentity: "psk-1"})
	v1 := mustExport(t, f, "app/token", nil, 32)

	f.RecordEvent(EventResumption)
	f.BeginHandshake(digestOf("resumption-2"))
	f.Freeze(Identity{ECHOuterName: "a.example", CertID: "cert-b", ALPN: "h2", PSKIdentity: "psk-1"})
	v2 := mustExport(t, f, "app/token", nil, 32)

	if bytes.Equal(v1, v2) {
		t.Fatal("same PSK across certificates produced identical exporter")
	}
}

// A nil context and an empty non-nil context are distinct cache entries and
// distinct exported values.
func TestNilVersusEmptyContext(t *testing.T) {
	f := NewFacade(nil)
	f.BeginHandshake(digestOf("hs"))
	f.Freeze(Identity{ECHOuterName: "a.example", CertID: "cert-a", ALPN: "h2"})

	vNil := mustExport(t, f, "app/token", nil, 32)
	vEmpty := mustExport(t, f, "app/token", []byte{}, 32)
	if bytes.Equal(vNil, vEmpty) {
		t.Fatal("nil and empty context collapsed into one exporter")
	}
	if v := mustExport(t, f, "app/token", nil, 32); !bytes.Equal(v, vNil) {
		t.Fatal("nil-context cache entry not stable")
	}
	if v := mustExport(t, f, "app/token", []byte{}, 32); !bytes.Equal(v, vEmpty) {
		t.Fatal("empty-context cache entry not stable")
	}
}

// An async export started before HelloRetryRequest must not populate the
// cache of the post-HRR generation.
func TestHelloRetryRequestStaleAsync(t *testing.T) {
	f := NewFacade(nil)
	f.BeginHandshake(digestOf("chlo-shlo"))
	id := Identity{ECHOuterName: "a.example", CertID: "cert-a", ALPN: "h2"}
	f.Freeze(id)

	tk, err := f.BeginExport("app/token", nil, 32)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := f.Derive(tk)
	if err != nil {
		t.Fatal(err)
	}

	f.RecordEvent(EventHelloRetryRequest)
	if err := f.Commit(tk, stale); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("commit after HRR: got %v, want ErrStaleGeneration", err)
	}

	f.BeginHandshake(digestOf("chlo-hrr-shlo"))
	f.Freeze(id)
	if fresh := mustExport(t, f, "app/token", nil, 32); bytes.Equal(stale, fresh) {
		t.Fatal("post-HRR export served a pre-HRR value")
	}
}

// When ECH is rejected (outer name induced), the inner-name identity's
// exporter must not leak into the outer-name generation.
func TestECHOuterNameInduction(t *testing.T) {
	f := NewFacade(nil)
	f.BeginHandshake(digestOf("hs-ech-accepted"))
	innerID := Identity{
		ECHAccepted:  true,
		ECHInnerName: "secret.internal",
		ECHOuterName: "public.example",
		CertID:       "cert-secret",
		ALPN:         "h2",
	}
	f.Freeze(innerID)
	if got := innerID.Vhost(); got != "secret.internal" {
		t.Fatalf("Vhost() = %q, want inner name", got)
	}
	innerVal := mustExport(t, f, "app/token", nil, 32)

	tk, err := f.BeginExport("app/other", nil, 32)
	if err != nil {
		t.Fatal(err)
	}
	staleVal, err := f.Derive(tk)
	if err != nil {
		t.Fatal(err)
	}

	f.RecordEvent(EventECHRejected)
	if err := f.Commit(tk, staleVal); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("commit after ECH rejection: got %v, want ErrStaleGeneration", err)
	}

	f.BeginHandshake(digestOf("hs-ech-rejected"))
	outerID := Identity{ECHOuterName: "public.example", CertID: "cert-public", ALPN: "h2"}
	f.Freeze(outerID)
	if got := outerID.Vhost(); got != "public.example" {
		t.Fatalf("Vhost() = %q, want outer name", got)
	}
	outerVal := mustExport(t, f, "app/token", nil, 32)
	if bytes.Equal(innerVal, outerVal) {
		t.Fatal("ECH rejection leaked the inner-name exporter to the outer vhost")
	}
	if again := mustExport(t, f, "app/token", nil, 32); !bytes.Equal(again, outerVal) {
		t.Fatal("cache inconsistent after ECH rejection")
	}
}

// A legal resumption exports only after re-freezing and never replays the
// full handshake's value.
func TestLegalResumption(t *testing.T) {
	f := NewFacade(nil)
	f.BeginHandshake(digestOf("full-handshake"))
	f.Freeze(Identity{ECHOuterName: "a.example", CertID: "cert-a", ALPN: "h2"})
	vFull := mustExport(t, f, "app/token", nil, 32)

	f.RecordEvent(EventResumption)
	if _, err := f.Export("app/token", nil, 32); !errors.Is(err, ErrNotFrozen) {
		t.Fatalf("export during resumption: got %v, want ErrNotFrozen", err)
	}

	f.BeginHandshake(digestOf("resumed-handshake"))
	f.Freeze(Identity{ECHOuterName: "a.example", CertID: "cert-a", ALPN: "h2", PSKIdentity: "psk-1"})
	vRes := mustExport(t, f, "app/token", nil, 32)
	if bytes.Equal(vFull, vRes) {
		t.Fatal("resumed export equals the full-handshake export")
	}
}

// Every generation-bumping event invalidates pending async exports.
func TestGenerationEventsInvalidateAsync(t *testing.T) {
	events := []Event{
		EventZeroRTT,
		EventHelloRetryRequest,
		EventECHRejected,
		EventResumption,
		EventCertReload,
	}
	for _, ev := range events {
		t.Run(ev.String(), func(t *testing.T) {
			f := NewFacade(nil)
			f.BeginHandshake(digestOf("hs-" + ev.String()))
			f.Freeze(Identity{ECHOuterName: "a.example", CertID: "cert-a", ALPN: "h2"})
			gen0 := f.Generation()

			tk, err := f.BeginExport("app/token", nil, 32)
			if err != nil {
				t.Fatal(err)
			}
			val, err := f.Derive(tk)
			if err != nil {
				t.Fatal(err)
			}

			f.RecordEvent(ev)
			if f.Generation() == gen0 {
				t.Fatalf("%s did not bump the generation", ev)
			}
			if err := f.Commit(tk, val); !errors.Is(err, ErrStaleGeneration) {
				t.Fatalf("commit after %s: got %v, want ErrStaleGeneration", ev, err)
			}
			if _, err := f.Export("app/token", nil, 32); !errors.Is(err, ErrNotFrozen) {
				t.Fatalf("export after %s before re-freeze: got %v, want ErrNotFrozen", ev, err)
			}
		})
	}
}

// Certificate hot reload with an unchanged digest must still produce a new
// exporter once the new certificate identity is frozen.
func TestCertHotReloadNewIdentity(t *testing.T) {
	f := NewFacade(nil)
	f.BeginHandshake(digestOf("hs"))
	f.Freeze(Identity{ECHOuterName: "a.example", CertID: "cert-old", ALPN: "h2"})
	vOld := mustExport(t, f, "app/token", nil, 32)

	f.RecordEvent(EventCertReload)
	f.Freeze(Identity{ECHOuterName: "a.example", CertID: "cert-new", ALPN: "h2"})
	vNew := mustExport(t, f, "app/token", nil, 32)
	if bytes.Equal(vOld, vNew) {
		t.Fatal("certificate hot reload did not change the exporter")
	}
}

// Reserved EXPORTER-prefixed labels and out-of-range lengths are rejected.
func TestLabelAndLengthValidation(t *testing.T) {
	f := NewFacade(nil)
	f.BeginHandshake(digestOf("hs"))
	f.Freeze(Identity{ECHOuterName: "a.example", CertID: "cert-a", ALPN: "h2"})

	for _, label := range []string{"", "EXPORTER", "EXPORTER-Channel-Binding", "EXPORTER-anything"} {
		if _, err := f.Export(label, nil, 32); err == nil {
			t.Errorf("label %q accepted", label)
		}
		if _, err := f.BeginExport(label, nil, 32); err == nil {
			t.Errorf("async label %q accepted", label)
		}
	}
	for _, n := range []int{0, -1, MaxExportLength + 1} {
		if _, err := f.Export("app/token", nil, n); err == nil {
			t.Errorf("length %d accepted", n)
		}
	}
	if _, err := f.Export("app/token", nil, MaxExportLength); err != nil {
		t.Errorf("max length rejected: %v", err)
	}
}

// Nothing may be exported before decisions are frozen.
func TestExportRequiresFrozenDecisions(t *testing.T) {
	f := NewFacade(nil)
	if _, err := f.Export("app/token", nil, 32); !errors.Is(err, ErrNotFrozen) {
		t.Fatalf("export before any handshake: got %v, want ErrNotFrozen", err)
	}
	f.BeginHandshake(digestOf("hs"))
	if _, err := f.Export("app/token", nil, 32); !errors.Is(err, ErrNotFrozen) {
		t.Fatalf("export mid-handshake: got %v, want ErrNotFrozen", err)
	}
	if _, err := f.BeginExport("app/token", nil, 32); !errors.Is(err, ErrNotFrozen) {
		t.Fatalf("async export mid-handshake: got %v, want ErrNotFrozen", err)
	}
	f.Freeze(Identity{ECHOuterName: "a.example", CertID: "cert-a", ALPN: "h2"})
	if _, err := f.Export("app/token", nil, 32); err != nil {
		t.Fatalf("export after freeze: %v", err)
	}
}

// The async happy path stores exactly the derived value and rejects
// length-mismatched commits.
func TestAsyncCommitPopulatesCache(t *testing.T) {
	f := NewFacade(nil)
	f.BeginHandshake(digestOf("hs"))
	f.Freeze(Identity{ECHOuterName: "a.example", CertID: "cert-a", ALPN: "h2"})

	tk, err := f.BeginExport("app/token", nil, 32)
	if err != nil {
		t.Fatal(err)
	}
	val, err := f.Derive(tk)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Commit(tk, val); err != nil {
		t.Fatal(err)
	}
	if got := mustExport(t, f, "app/token", nil, 32); !bytes.Equal(got, val) {
		t.Fatal("committed value not served from cache")
	}

	tk2, err := f.BeginExport("app/other", nil, 32)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Commit(tk2, val[:16]); !errors.Is(err, ErrValueLength) {
		t.Fatalf("short commit: got %v, want ErrValueLength", err)
	}
}
