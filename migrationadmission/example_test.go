package migrationadmission_test

import (
	"crypto/ed25519"
	"fmt"

	"prescreen.local/ps-015e5a23de/migrationadmission"
)

// Example shows the full host flow: define both policy versions, sign the
// alias/lateness material, let the loader independently verify the bundle,
// issue a login credential and re-check it after an alias rotation.
func Example() {
	pub, priv, err := migrationadmission.GenerateRootKey()
	if err != nil {
		panic(err)
	}

	grammar := migrationadmission.Grammar{
		Name:    "g",
		Start:   0,
		Accepts: []int{0, 1},
		Transitions: []migrationadmission.Rule{
			{From: 0, On: 'a', To: 1},
		},
	}
	old := migrationadmission.Policy{
		Version: "old-1",
		Grammar: grammar,
		Transcription: migrationadmission.TranscriptionTable{
			Name:  "old-table",
			Rules: []migrationadmission.TranscriptionRule{{On: 'a', Emit: []migrationadmission.Symbol{1}}},
		},
	}
	next := migrationadmission.Policy{
		Version: "new-1",
		Grammar: grammar,
		Transcription: migrationadmission.TranscriptionTable{
			Name:  "new-table",
			Rules: []migrationadmission.TranscriptionRule{{On: 'a', Emit: []migrationadmission.Symbol{1, 2}}},
			Tail:  []migrationadmission.Symbol{9},
		},
	}

	cert, err := migrationadmission.SignLatenessCertificate(priv, migrationadmission.LatenessCertificate{
		TenantID:      "tenant-demo",
		PolicyVersion: next.Version,
		MaxBacklog:    3,
		Epoch:         1,
	})
	if err != nil {
		panic(err)
	}
	bundle := migrationadmission.Bundle{
		TenantID:  "tenant-demo",
		Epoch:     1,
		OldPolicy: old,
		NewPolicy: next,
		RootKey:   pub,
		Lateness:  cert,
	}
	signed, err := bundle.Marshal(priv)
	if err != nil {
		panic(err)
	}
	wire, err := signed.MarshalBinary()
	if err != nil {
		panic(err)
	}

	verified, err := migrationadmission.Verify(wire, nil)
	if err != nil {
		panic(err)
	}
	identifier, err := verified.Admit([]byte("a"))
	if err != nil {
		panic(err)
	}
	fmt.Println("login identifier:", identifier)

	credential, err := verified.IssueCredential(priv, []byte("a"))
	if err != nil {
		panic(err)
	}
	fmt.Println("fresh credential:", verified.VerifyCredential(credential) == nil)

	// Rotating the alias epoch invalidates the old credential.
	cert2, err := migrationadmission.SignLatenessCertificate(priv, migrationadmission.LatenessCertificate{
		TenantID:      "tenant-demo",
		PolicyVersion: next.Version,
		MaxBacklog:    3,
		Epoch:         2,
	})
	if err != nil {
		panic(err)
	}
	bundle.Epoch = 2
	bundle.Lateness = cert2
	rotated, err := bundle.Marshal(priv)
	if err != nil {
		panic(err)
	}
	rotatedWire, err := rotated.MarshalBinary()
	if err != nil {
		panic(err)
	}
	verified2, err := migrationadmission.Verify(rotatedWire, ed25519.PublicKey(pub))
	if err != nil {
		panic(err)
	}
	fmt.Println("stale credential after rotation:", verified2.VerifyCredential(credential))
	// Output:
	// login identifier: [1 2 9]
	// fresh credential: true
	// stale credential after rotation: migrationadmission: credential was invalidated by an alias update
}
