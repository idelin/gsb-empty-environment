package netsec

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func TestVerifyResponseAcceptsValidFixture(t *testing.T) {
	f := newFixture()
	if err := VerifyResponse(f.response, f.options()); err != nil {
		t.Fatalf("valid response rejected: %v", err)
	}
}

func TestRelayedTranscriptRejected(t *testing.T) {
	// The attacker relays a complete, internally consistent response captured
	// in another verifier session. The signature is over the victim's
	// SessionTranscript, so it fails against this verifier's own transcript.
	f := newFixture()
	options := f.options()
	options.SessionTranscript = encodedSessionTranscript(
		[]byte("a-different-verifier-session-transcript"))

	err := VerifyResponse(f.response, options)
	if !errors.Is(err, ErrDeviceSignature) {
		t.Fatalf("expected ErrDeviceSignature for relayed transcript, got %v", err)
	}
}

func TestUnsignedInjectedElementRejected(t *testing.T) {
	// An extra data element whose digestID has no entry in the MSO
	// valueDigests: the issuer never signed it.
	f := newFixture()
	resp := f.cloneResponse()
	ns := "org.iso.18013.5.1"
	resp.IssuerSigned.NameSpaces[ns] = append(resp.IssuerSigned.NameSpaces[ns], IssuerSignedItem{
		DigestID:          99,
		Random:            bytes.Repeat([]byte{0x09}, 16),
		ElementIdentifier: "domestic_driving_privileges",
		ElementValue:      "forged",
	})
	// The attacker also fakes a request entry; the MSO digest check is what
	// must still reject the unsigned element.
	options := f.options()
	options.Requested = append(options.Requested, RequestedElement{
		NameSpace: ns, ElementIdentifier: "domestic_driving_privileges",
	})

	err := VerifyResponse(resp, options)
	if !errors.Is(err, ErrDigestNotFound) {
		t.Fatalf("expected ErrDigestNotFound for unsigned item, got %v", err)
	}
}

func TestTamperedElementValueDigestMismatch(t *testing.T) {
	// Same digestID, still present in the MSO, but elementValue is tampered
	// with so the recomputed digest no longer equals the issuer digest.
	f := newFixture()
	resp := f.cloneResponse()
	resp.IssuerSigned.NameSpaces["org.iso.18013.5.1"][0].ElementValue = "Forged"

	err := VerifyResponse(resp, f.options())
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("expected ErrDigestMismatch for tampered value, got %v", err)
	}
}

func TestElementOutsideRequestRejected(t *testing.T) {
	f := newFixture()
	resp := f.cloneResponse()

	// Remove family_name from the request while keeping it in the response.
	options := f.options()
	options.Requested = options.Requested[1:]

	err := VerifyResponse(resp, options)
	if !errors.Is(err, ErrNotRequested) {
		t.Fatalf("expected ErrNotRequested, got %v", err)
	}
}

func TestReturnedSubsetOfRequestAccepted(t *testing.T) {
	// Requesting more than is returned is allowed; the returned set only has
	// to be a subset of the request.
	f := newFixture()
	resp := f.cloneResponse()
	ns := "org.iso.18013.5.1"
	resp.IssuerSigned.NameSpaces[ns] = resp.IssuerSigned.NameSpaces[ns][:1]

	if err := VerifyResponse(resp, f.options()); err != nil {
		t.Fatalf("subset response rejected: %v", err)
	}
}

func TestExpiredMSORejected(t *testing.T) {
	base := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		now  time.Time
	}{
		{"before validFrom", base.Add(-48 * time.Hour)},
		{"at validUntil", base.Add(24 * time.Hour)},
		{"after validUntil", base.Add(48 * time.Hour)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			options := f.options()
			options.Now = tc.now
			err := VerifyResponse(f.response, options)
			if !errors.Is(err, ErrExpired) {
				t.Fatalf("expected ErrExpired, got %v", err)
			}
		})
	}
}

func TestValidityBoundaryAccepted(t *testing.T) {
	f := newFixture()
	for _, now := range []time.Time{
		f.response.MSO.ValidityInfo.ValidFrom,
		f.response.MSO.ValidityInfo.ValidUntil.Add(-time.Second),
	} {
		options := f.options()
		options.Now = now
		if err := VerifyResponse(f.response, options); err != nil {
			t.Fatalf("time %s should be inside validity window: %v", now, err)
		}
	}
}

func TestDuplicateDigestIDRejected(t *testing.T) {
	f := newFixture()
	resp := f.cloneResponse()

	// Reuse digestID 0 inside the same namespace. A matching MSO digest is
	// forged as well so the duplicate rule is what fires.
	ns := "org.iso.18013.5.1"
	forged := IssuerSignedItem{
		DigestID:          0,
		Random:            bytes.Repeat([]byte{0x04}, 16),
		ElementIdentifier: "portrait_capture_date",
		ElementValue:      "2026-01-01",
	}
	resp.IssuerSigned.NameSpaces[ns] = append(resp.IssuerSigned.NameSpaces[ns], forged)
	// The forged element also appears in the request so the duplicate
	// digestID rule, not the subset rule, is what rejects it.
	options := f.options()
	options.Requested = append(options.Requested, RequestedElement{
		NameSpace: ns, ElementIdentifier: "portrait_capture_date",
	})

	err := VerifyResponse(resp, options)
	if !errors.Is(err, ErrDuplicateDigestID) {
		t.Fatalf("expected ErrDuplicateDigestID, got %v", err)
	}
}

func TestDuplicateDigestIDAcrossNamespacesAllowed(t *testing.T) {
	// The fixture already uses digestID 0 in both namespaces, which is legal.
	f := newFixture()
	if err := VerifyResponse(f.response, f.options()); err != nil {
		t.Fatalf("digestID reuse across namespaces should be allowed: %v", err)
	}
}

func TestShortRandomRejected(t *testing.T) {
	f := newFixture()
	resp := f.cloneResponse()
	resp.IssuerSigned.NameSpaces["org.iso.18013.5.1"][0].Random =
		bytes.Repeat([]byte{0x01}, 15)

	err := VerifyResponse(resp, f.options())
	if !errors.Is(err, ErrShortRandom) {
		t.Fatalf("expected ErrShortRandom, got %v", err)
	}
}

func TestDocTypeMismatchRejected(t *testing.T) {
	f := newFixture()
	resp := f.cloneResponse()
	resp.DocType = "org.iso.18013.5.1.mDL.wrong"

	err := VerifyResponse(resp, f.options())
	if !errors.Is(err, ErrDocTypeMismatch) {
		t.Fatalf("expected ErrDocTypeMismatch, got %v", err)
	}

	resp2 := f.cloneResponse()
	resp2.MSO.DocType = ""
	if err := VerifyResponse(resp2, f.options()); !errors.Is(err, ErrDocTypeMismatch) {
		t.Fatalf("empty MSO docType: expected ErrDocTypeMismatch, got %v", err)
	}
}

func TestUnknownNameSpaceDigestNotFound(t *testing.T) {
	f := newFixture()
	resp := f.cloneResponse()
	resp.IssuerSigned.NameSpaces["org.example.unknown"] = []IssuerSignedItem{{
		DigestID:          0,
		Random:            bytes.Repeat([]byte{0x05}, 16),
		ElementIdentifier: "something",
		ElementValue:      "value",
	}}
	options := f.options()
	options.Requested = append(options.Requested, RequestedElement{
		NameSpace: "org.example.unknown", ElementIdentifier: "something",
	})

	err := VerifyResponse(resp, options)
	if !errors.Is(err, ErrDigestNotFound) {
		t.Fatalf("expected ErrDigestNotFound for unknown namespace, got %v", err)
	}
}

func TestTamperedDeviceSignatureRejected(t *testing.T) {
	f := newFixture()
	resp := f.cloneResponse()
	cose := resp.DeviceSigned.DeviceAuth.DeviceAuthCOSE
	cose[len(cose)-1] ^= 0xff

	err := VerifyResponse(resp, f.options())
	if !errors.Is(err, ErrDeviceSignature) {
		t.Fatalf("expected ErrDeviceSignature, got %v", err)
	}
}

func TestMissingInputsAndCallbacks(t *testing.T) {
	f := newFixture()

	if err := VerifyResponse(nil, f.options()); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("nil response: expected ErrInvalidResponse, got %v", err)
	}

	options := f.options()
	options.NewHash = nil
	if err := VerifyResponse(f.response, options); !errors.Is(err, ErrMissingCallback) {
		t.Fatalf("nil hasher: expected ErrMissingCallback, got %v", err)
	}

	options = f.options()
	options.VerifyDeviceSignature = nil
	if err := VerifyResponse(f.response, options); !errors.Is(err, ErrMissingCallback) {
		t.Fatalf("nil verifier: expected ErrMissingCallback, got %v", err)
	}

	options = f.options()
	options.SessionTranscript = nil
	if err := VerifyResponse(f.response, options); !errors.Is(err, ErrSessionTranscript) {
		t.Fatalf("nil transcript: expected ErrSessionTranscript, got %v", err)
	}
}
