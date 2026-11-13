package macaroon

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"
)

var (
	primaryRootKey = bytes.Repeat([]byte{0x11}, 32)
	thirdRootKeyA  = bytes.Repeat([]byte{0x22}, 32)
	thirdRootKeyB  = bytes.Repeat([]byte{0x33}, 32)
	fixedNow       = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
)

func testVerifier(vids map[string][]byte) *Verifier {
	return &Verifier{
		Check: func(predicate []byte) bool {
			switch string(predicate) {
			case "user = alice", "role = admin", "account = 42":
				return true
			default:
				return false
			}
		},
		RecoverRootKey: func(vid []byte) ([]byte, error) {
			if key, ok := vids[string(vid)]; ok {
				return key, nil
			}
			return nil, fmt.Errorf("unknown vid %q", string(vid))
		},
		Now: func() time.Time { return fixedNow },
	}
}

func mintPrimary(identifier []byte, rootKey []byte, caveats []Caveat) *Macaroon {
	sig := hmacSHA256(rootKey, identifier)
	for i := range caveats {
		c := caveats[i]
		if c.IsThirdParty() {
			msg := append(append([]byte{}, c.VID...), c.CID...)
			sig = hmacSHA256(sig, msg)
		} else {
			sig = hmacSHA256(sig, c.Caveat)
		}
	}
	return &Macaroon{Identifier: identifier, Caveats: caveats, Signature: sig}
}

func mintDischarge(identifier []byte, rootKey []byte, predicates ...[]byte) *Macaroon {
	caveats := make([]Caveat, 0, len(predicates))
	for _, p := range predicates {
		caveats = append(caveats, Caveat{Caveat: append([]byte{}, p...)})
	}
	return mintPrimary(identifier, rootKey, caveats)
}

func bound(m *Macaroon, primarySig []byte) BoundDischarge {
	msg := append(append([]byte{}, primarySig...), m.Signature...)
	return BoundDischarge{Macaroon: m, Binding: hmacSHA256(zeroKey, msg)}
}

func timeCaveat(t time.Time) []byte {
	return []byte(timeCaveatPrefix + t.UTC().Format(time.RFC3339))
}

// validRequest builds a primary macaroon with one first-party and one
// third-party caveat, plus a correctly bound discharge.
func validRequest(t *testing.T) (*Verifier, Request) {
	t.Helper()
	vid := []byte("vid-a")
	cid := []byte("cid-a")
	vids := map[string][]byte{string(vid): thirdRootKeyA}
	v := testVerifier(vids)

	caveats := []Caveat{
		{Caveat: []byte("user = alice")},
		{Location: "https://auth.example", VID: vid, CID: cid},
	}
	primary := mintPrimary([]byte("id-primary"), primaryRootKey, caveats)
	dm := mintDischarge(cid, thirdRootKeyA, []byte("account = 42"))

	return v, Request{
		Primary:    primary,
		Discharges: []BoundDischarge{bound(dm, primary.Signature)},
	}
}

func TestVerifyValid(t *testing.T) {
	v, req := validRequest(t)
	if err := v.Verify(primaryRootKey, &req); err != nil {
		t.Fatalf("expected valid request, got %v", err)
	}
}

func TestDischargeReusedAcrossCaveats(t *testing.T) {
	vids := map[string][]byte{
		"vid-a": thirdRootKeyA,
		"vid-b": thirdRootKeyA, // both third parties resolve to the same key
	}
	v := testVerifier(vids)

	cidA := []byte("cid-a")
	cidB := []byte("cid-b")
	dm := mintDischarge(cidA, thirdRootKeyA, []byte("account = 42"))

	tests := []struct {
		name    string
		caveats []Caveat
	}{
		{
			// Two third-party caveats share one cid, so one authentic discharge
			// would have to satisfy both obligations within one verification.
			name: "same cid twice",
			caveats: []Caveat{
				{Location: "auth-a", VID: []byte("vid-a"), CID: cidA},
				{Location: "auth-b", VID: []byte("vid-b"), CID: cidA},
			},
		},
		{
			// Identical third-party caveats duplicated in the chain.
			name: "identical caveat duplicated",
			caveats: []Caveat{
				{Location: "auth-a", VID: []byte("vid-a"), CID: cidA},
				{Location: "auth-a", VID: []byte("vid-a"), CID: cidA},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			primary := mintPrimary([]byte("id"), primaryRootKey, tc.caveats)
			req := Request{
				Primary:    primary,
				Discharges: []BoundDischarge{bound(dm, primary.Signature)},
			}
			err := v.Verify(primaryRootKey, &req)
			if !errors.Is(err, ErrDischargeReused) {
				t.Fatalf("expected ErrDischargeReused, got %v", err)
			}
		})
	}

	// Two distinct cids with distinct discharges under the same root key verify.
	caveats := []Caveat{
		{Location: "auth-a", VID: []byte("vid-a"), CID: cidA},
		{Location: "auth-b", VID: []byte("vid-b"), CID: cidB},
	}
	primary := mintPrimary([]byte("id"), primaryRootKey, caveats)
	dmB := mintDischarge(cidB, thirdRootKeyA, []byte("account = 42"))
	req := Request{
		Primary: primary,
		Discharges: []BoundDischarge{
			bound(dm, primary.Signature),
			bound(dmB, primary.Signature),
		},
	}
	if err := v.Verify(primaryRootKey, &req); err != nil {
		t.Fatalf("expected distinct discharges to verify, got %v", err)
	}
}

func TestReusedDischargeFromAnotherRequest(t *testing.T) {
	v, req := validRequest(t)

	// Attacker copies a discharge whose identifier is cid-a and presents it for
	// a different cid. The identifier/cid mismatch means no discharge matches.
	stolen := req.Discharges[0].Macaroon
	other := Request{
		Primary: mintPrimary([]byte("id2"), primaryRootKey, []Caveat{
			{Location: "auth", VID: []byte("vid-a"), CID: []byte("different-cid")},
		}),
		Discharges: []BoundDischarge{
			{Macaroon: stolen, Binding: req.Discharges[0].Binding},
		},
	}
	err := v.Verify(primaryRootKey, &other)
	if !errors.Is(err, ErrDischarge) {
		t.Fatalf("expected ErrDischarge for foreign cid, got %v", err)
	}
}

func TestDeletedCaveatBreaksSignature(t *testing.T) {
	v, req := validRequest(t)

	// Drop the third-party caveat but keep the original final signature.
	tampered := *req.Primary
	tampered.Caveats = tampered.Caveats[:1]
	err := v.Verify(primaryRootKey, &Request{Primary: &tampered})
	if !errors.Is(err, ErrSignature) {
		t.Fatalf("expected ErrSignature after deleting primary caveat, got %v", err)
	}

	// Re-sign after legitimately removing the caveat yields a different final
	// signature and no third-party obligation remains to discharge.
	resigned := mintPrimary(req.Primary.Identifier, primaryRootKey, tampered.Caveats)
	if err := v.Verify(primaryRootKey, &Request{Primary: resigned}); err != nil {
		t.Fatalf("resigned macaroon without the caveat should verify, got %v", err)
	}

	// Delete a caveat inside the discharge macaroon while retaining its old
	// signature: its signature chain no longer matches.
	v2, req2 := validRequest(t)
	dm := *req2.Discharges[0].Macaroon
	dm.Caveats = nil // remove the first-party predicate, keep the stale signature
	req2.Discharges[0].Macaroon = &dm
	err = v2.Verify(primaryRootKey, &req2)
	if !errors.Is(err, ErrSignature) {
		t.Fatalf("expected ErrSignature after deleting discharge caveat, got %v", err)
	}
}

func TestWrongBindingSignature(t *testing.T) {
	v, req := validRequest(t)
	req.Discharges[0].Binding = bytes.Repeat([]byte{0xff}, sha256.Size)
	err := v.Verify(primaryRootKey, &req)
	if !errors.Is(err, ErrBinding) {
		t.Fatalf("expected ErrBinding, got %v", err)
	}

	// A binding computed from the wrong primary signature is also rejected.
	v2, req2 := validRequest(t)
	wrongPrimarySig := bytes.Repeat([]byte{0x01}, sha256.Size)
	req2.Discharges[0].Binding = hmacSHA256(zeroKey,
		append(append([]byte{}, wrongPrimarySig...), req2.Discharges[0].Macaroon.Signature...))
	err = v2.Verify(primaryRootKey, &req2)
	if !errors.Is(err, ErrBinding) {
		t.Fatalf("expected ErrBinding for foreign primary signature, got %v", err)
	}
}

func TestUnknownPredicateRejected(t *testing.T) {
	v, req := validRequest(t)

	unknown := mintPrimary(req.Primary.Identifier, primaryRootKey, []Caveat{
		{Caveat: []byte("user = alice")},
		{Caveat: []byte("team = ghosts")}, // not recognized by the callback
	})
	err := v.Verify(primaryRootKey, &Request{Primary: unknown})
	if !errors.Is(err, ErrPredicate) {
		t.Fatalf("expected ErrPredicate, got %v", err)
	}

	// An unknown predicate inside a discharge is rejected as well.
	v2, req2 := validRequest(t)
	badDischarge := mintDischarge(req2.Discharges[0].Macaroon.Identifier, thirdRootKeyA,
		[]byte("team = ghosts"))
	req2.Discharges[0] = bound(badDischarge, req2.Primary.Signature)
	err = v2.Verify(primaryRootKey, &req2)
	if !errors.Is(err, ErrPredicate) {
		t.Fatalf("expected ErrPredicate in discharge, got %v", err)
	}
}

func TestMultipleTimeCaveatsTakeEarliest(t *testing.T) {
	earliest := fixedNow.Add(-time.Hour)
	middle := fixedNow.Add(time.Hour)
	latest := fixedNow.Add(24 * time.Hour)

	primary := mintPrimary([]byte("id"), primaryRootKey, []Caveat{
		{Caveat: timeCaveat(latest)},
		{Caveat: timeCaveat(earliest)},
		{Caveat: timeCaveat(middle)},
	})
	v := testVerifier(nil)
	err := v.Verify(primaryRootKey, &Request{Primary: primary})
	if !errors.Is(err, ErrTime) {
		t.Fatalf("expected earliest deadline to expire the request, got %v", err)
	}

	// All deadlines in the future (earliest wins and is still open).
	future := mintPrimary([]byte("id"), primaryRootKey, []Caveat{
		{Caveat: timeCaveat(latest)},
		{Caveat: timeCaveat(middle)},
	})
	if err := v.Verify(primaryRootKey, &Request{Primary: future}); err != nil {
		t.Fatalf("expected unexpired macaroon, got %v", err)
	}

	// Exact equality is not before the deadline, so time < T rejects at T.
	atDeadline := mintPrimary([]byte("id"), primaryRootKey, []Caveat{
		{Caveat: timeCaveat(fixedNow)},
	})
	if err := v.Verify(primaryRootKey, &Request{Primary: atDeadline}); !errors.Is(err, ErrTime) {
		t.Fatalf("expected ErrTime at exact deadline, got %v", err)
	}
}

func TestTooManyCaveats(t *testing.T) {
	caveats := make([]Caveat, MaxCaveats+1)
	for i := range caveats {
		caveats[i] = Caveat{Caveat: []byte("user = alice")}
	}
	primary := mintPrimary([]byte("id"), primaryRootKey, caveats)
	v := testVerifier(nil)
	err := v.Verify(primaryRootKey, &Request{Primary: primary})
	if !errors.Is(err, ErrTooManyCaveats) {
		t.Fatalf("expected ErrTooManyCaveats, got %v", err)
	}

	// Exactly MaxCaveats primary caveats is accepted.
	caveats = caveats[:MaxCaveats]
	primary = mintPrimary([]byte("id"), primaryRootKey, caveats)
	if err := v.Verify(primaryRootKey, &Request{Primary: primary}); err != nil {
		t.Fatalf("expected %d caveats to be accepted, got %v", MaxCaveats, err)
	}

	// Discharge caveats count toward the total as well.
	vid := []byte("vid-x")
	cid := []byte("cid-x")
	v2 := testVerifier(map[string][]byte{string(vid): thirdRootKeyB})
	primaryCaveats := make([]Caveat, MaxCaveats)
	for i := range primaryCaveats {
		primaryCaveats[i] = Caveat{Caveat: []byte("user = alice")}
	}
	primaryCaveats = append(primaryCaveats, Caveat{Location: "auth", VID: vid, CID: cid})
	primary = mintPrimary([]byte("id"), primaryRootKey, primaryCaveats)
	dm := mintDischarge(cid, thirdRootKeyB, []byte("account = 42"))
	err = v2.Verify(primaryRootKey, &Request{
		Primary:    primary,
		Discharges: []BoundDischarge{bound(dm, primary.Signature)},
	})
	if !errors.Is(err, ErrTooManyCaveats) {
		t.Fatalf("expected discharge caveats to count, got %v", err)
	}
}

func TestMissingAndForeignDischarge(t *testing.T) {
	vid := []byte("vid-a")
	cid := []byte("cid-a")
	v := testVerifier(map[string][]byte{string(vid): thirdRootKeyA})
	primary := mintPrimary([]byte("id"), primaryRootKey, []Caveat{
		{Location: "auth", VID: vid, CID: cid},
	})

	err := v.Verify(primaryRootKey, &Request{Primary: primary})
	if !errors.Is(err, ErrDischarge) {
		t.Fatalf("expected ErrDischarge when no discharge is present, got %v", err)
	}

	// A discharge with the right identifier but signed under the wrong root key
	// fails its own signature check.
	wrong := mintDischarge(cid, thirdRootKeyB)
	err = v.Verify(primaryRootKey, &Request{
		Primary:    primary,
		Discharges: []BoundDischarge{bound(wrong, primary.Signature)},
	})
	if !errors.Is(err, ErrSignature) {
		t.Fatalf("expected ErrSignature for wrong discharge root key, got %v", err)
	}

	// An unknown VID prevents root-key recovery.
	unknownVID := mintPrimary([]byte("id"), primaryRootKey, []Caveat{
		{Location: "auth", VID: []byte("missing-vid"), CID: cid},
	})
	dm := mintDischarge(cid, thirdRootKeyA)
	err = v.Verify(primaryRootKey, &Request{
		Primary:    unknownVID,
		Discharges: []BoundDischarge{bound(dm, unknownVID.Signature)},
	})
	if err == nil {
		t.Fatal("expected error when root key cannot be recovered")
	}
}

func TestMalformedRequests(t *testing.T) {
	v, req := validRequest(t)

	if err := v.Verify(nil, &req); !errors.Is(err, ErrMalformed) {
		t.Fatalf("expected ErrMalformed for empty root key, got %v", err)
	}
	if err := v.Verify(primaryRootKey, nil); !errors.Is(err, ErrMalformed) {
		t.Fatalf("expected ErrMalformed for nil request, got %v", err)
	}

	bad := req
	bad.Discharges = []BoundDischarge{{Macaroon: nil}}
	if err := v.Verify(primaryRootKey, &bad); !errors.Is(err, ErrMalformed) {
		t.Fatalf("expected ErrMalformed for nil discharge, got %v", err)
	}

	badCallbacks := &Verifier{}
	if err := badCallbacks.Verify(primaryRootKey, &req); !errors.Is(err, ErrMalformed) {
		t.Fatalf("expected ErrMalformed for unconfigured callbacks, got %v", err)
	}
}

func TestNestedThirdPartyCaveatInDischargeRejected(t *testing.T) {
	vid := []byte("vid-a")
	cid := []byte("cid-a")
	v := testVerifier(map[string][]byte{string(vid): thirdRootKeyA})
	primary := mintPrimary([]byte("id"), primaryRootKey, []Caveat{
		{Location: "auth", VID: vid, CID: cid},
	})

	// A discharge must only carry first-party caveats; a third-party caveat
	// inside it cannot be discharged by this verifier and is rejected.
	nested := mintPrimary(cid, thirdRootKeyA, []Caveat{
		{Location: "nested", VID: []byte("vid-nested"), CID: []byte("cid-nested")},
	})
	err := v.Verify(primaryRootKey, &Request{
		Primary:    primary,
		Discharges: []BoundDischarge{bound(nested, primary.Signature)},
	})
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("expected ErrMalformed for nested third-party caveat, got %v", err)
	}
}
