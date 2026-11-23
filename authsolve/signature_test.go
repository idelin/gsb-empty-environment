package authsolve

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"

	"prescreen.local/ps-e39099b0dc/clockwire"
)

func signedProblem(t *testing.T) (Problem, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	artifact := DigestOf([]byte("artifact"))
	p := Problem{
		Artifact: artifact,
		Devices:  []Device{{ID: "ref", Reference: true, PublicKey: pub}},
		Credentials: []Credential{{
			ID: "c1", Identity: "alice", Device: "ref", Role: "release",
			Artifact: artifact, NotBefore: 0, NotAfter: 10,
		}},
		Policy:          PolicyNode{Role: "release"},
		Windows:         []Window{{Start: 0, End: 4}, {Start: 5, End: 9}},
		ReferenceDevice: "ref",
	}
	if err := SignCredential(priv, &p.Credentials[0]); err != nil {
		t.Fatal(err)
	}
	return p, priv
}

func TestValidAndInvalidSignature(t *testing.T) {
	p, _ := signedProblem(t)
	if _, err := Solve(p); err != nil {
		t.Fatalf("Solve: %v", err)
	}
	bad := p
	bad.Credentials = append([]Credential(nil), p.Credentials...)
	bad.Credentials[0].Signature[0] ^= 0xff
	if _, err := Solve(bad); !errors.Is(err, ErrSignature) {
		t.Fatalf("expected ErrSignature, got %v", err)
	}
}

func TestRoleDowngradeCountsIdentityOnce(t *testing.T) {
	p := Problem{
		Devices: []Device{{ID: "ref", Reference: true}},
		Credentials: []Credential{{
			ID: "admin", Identity: "alice", Device: "ref", Role: "admin",
			NotBefore: 0, NotAfter: 10,
		}},
		Policy: PolicyNode{Threshold: 2, Children: []PolicyNode{
			{Role: "release"},
			{Role: "viewer"},
		}},
		Windows:         []Window{{Start: 0, End: 5}, {Start: 6, End: 9}},
		ReferenceDevice: "ref",
		RoleLevels:      map[string]int{"admin": 3, "release": 2, "viewer": 1},
	}
	if _, err := Solve(p); !errors.Is(err, ErrNoWitness) {
		t.Fatalf("one identity must not count twice; got %v", err)
	}
	p.Credentials = append(p.Credentials, Credential{
		ID: "viewer", Identity: "bob", Device: "ref", Role: "viewer",
		NotBefore: 0, NotAfter: 10,
	})
	w, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if len(w.Signers) != 2 {
		t.Fatalf("expected two signers, got %v", w.Signers)
	}
}

func TestBoundSignedByteRoundTrip(t *testing.T) {
	b := clockwire.New(-1<<63, 1<<63-1)
	wire, err := b.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	got, err := clockwire.ParseBound(wire)
	if err != nil {
		t.Fatal(err)
	}
	if got != b {
		t.Fatalf("round trip mismatch: %#v != %#v", got, b)
	}
}
