package pinpolicy_test

import (
	"fmt"

	"prescreen.local/ps-9059846159/pinpolicy"
)

// ExampleVerify shows the smallest end-to-end migration: one old-root policy
// followed by one dual-authorized new-root policy.
func ExampleVerify() {
	const (
		host    pinpolicy.HostID = "term-7"
		oldRoot pinpolicy.RootID = "old"
		newRoot pinpolicy.RootID = "new"
		deploy  pinpolicy.Role   = "deploy"
	)

	in := pinpolicy.Input{
		OldRoot: oldRoot,
		NewRoot: newRoot,
		Certs: []pinpolicy.Cert{
			{Key: "old-root", Host: host, Role: pinpolicy.RootRole, RootKey: oldRoot, PublishAt: 0},
			{Key: "new-root", Host: host, Role: pinpolicy.RootRole, RootKey: newRoot, PublishAt: 0},
			{Key: "old-k", Host: host, Role: deploy, PublishAt: 0},
			{Key: "new-k", Host: host, Role: deploy, PublishAt: 0},
		},
		Checkpoints:  []pinpolicy.Time{0},
		OfflineMax:   4,
		SkewMin:      0,
		SkewMax:      1,
		HorizonFrom:  0,
		HorizonUntil: 4,
	}

	first := pinpolicy.Policy{
		Sequence: 0, Host: host, ValidFrom: 0, ValidUntil: 4,
		Primary: "old-k", Backup: "old-k", RootKey: oldRoot,
		Thresholds: map[pinpolicy.Role]int{deploy: 1},
	}
	migrated := pinpolicy.Policy{
		Sequence: 1, Host: host, ValidFrom: 1, ValidUntil: 5,
		Primary: "new-k", Backup: "new-k", RootKey: newRoot,
		Thresholds: map[pinpolicy.Role]int{deploy: 1},
		PrevDigest: pinpolicy.Digest(first),
	}
	in.Releases = []pinpolicy.Release{
		{SignedPolicy: pinpolicy.SignedPolicy{
			Policy:     first,
			PublishAt:  0,
			Signatures: []pinpolicy.Signature{{Signer: "old-k", Role: deploy}},
		}},
		{SignedPolicy: pinpolicy.SignedPolicy{
			Policy:     migrated,
			PublishAt:  1,
			Signatures: []pinpolicy.Signature{{Signer: "new-k", Role: deploy}},
		}},
	}
	in.Handovers = []pinpolicy.Handover{{
		Host: host, PolicyDigest: pinpolicy.Digest(migrated), Role: deploy,
		OldRoot: oldRoot, NewRoot: newRoot,
		OldSigner: "old-root", NewSigner: "new-root", PublishAt: 1,
	}}

	report, err := pinpolicy.Verify(in)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("pass:", report.Pass)
	fmt.Println("safety:", report.SafetyPass)
	fmt.Println("liveness:", report.LivenessPass)
	fmt.Println("legal windows:", report.Stats.LegalWindowCount)

	// Output:
	// pass: true
	// safety: true
	// liveness: true
	// legal windows: 3
}
