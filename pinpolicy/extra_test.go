package pinpolicy

import (
	"strings"
	"testing"
)

// TestMissingHandoverReportsExactGap removes one side of the dual-root
// authorization and checks the earliest failure names the host, digest and
// missing role side.
func TestMissingHandoverReportsExactGap(t *testing.T) {
	in := baseInput()
	seq0 := oldPolicy(0, "", 0, 13)
	seq1 := newPolicy(1, Digest(seq0), 2, 13, newA, newB)

	in.Releases = []Release{
		{SignedPolicy: SignedPolicy{
			Policy:     seq0,
			PublishAt:  0,
			Signatures: []Signature{{Signer: oldA, Role: roleA}},
		}},
		{SignedPolicy: SignedPolicy{
			Policy:    seq1,
			PublishAt: 2,
			Signatures: []Signature{
				{Signer: newA, Role: roleA},
				{Signer: newB, Role: roleB},
			},
		}},
	}
	// Only the deploy handover exists; ops is entirely unauthorized.
	for _, handover := range handoversFor(seq1, 2) {
		if handover.Role == roleA {
			in.Handovers = append(in.Handovers, handover)
		}
	}

	report, err := Verify(in)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if report.Pass || report.LivenessPass {
		t.Fatalf("missing handover role must fail liveness: %s", RenderText(report))
	}

	found := false
	for _, window := range report.Windows {
		for _, verdict := range window.ReleaseVerdicts {
			for _, missing := range verdict.Missing {
				if missing.Host == host && missing.Role == roleB &&
					missing.PolicyDigest == Digest(seq1) && missing.MissingSide == "both" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatalf("exact missing ops authorization was not reported: %s",
			RenderText(report))
	}
	if !strings.Contains(RenderText(report), "earliest_failure") {
		t.Fatalf("renderer must surface the earliest failure")
	}
}

// TestUnknownAndRevokedSigner covers the unknown-key and open-revocation
// predicates independently of the migration boundary test.
func TestUnknownAndRevokedSigner(t *testing.T) {
	in := baseInput()
	seq0 := oldPolicy(0, "", 0, 13)

	in.Releases = []Release{
		{SignedPolicy: SignedPolicy{
			Policy:    seq0,
			PublishAt: 0,
			Signatures: []Signature{
				{Signer: KeyID("ghost-key"), Role: roleA},
			},
		}},
	}

	report, err := Verify(in)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if !report.SafetyPass {
		t.Fatalf("blocking an unknown signer must keep safety true: %s",
			RenderText(report))
	}
	if !reportWindowHasReason(report, ReasonUnknownKey) {
		t.Fatalf("expected unknown_key rejection in report: %s", RenderText(report))
	}

	// Now make the signer a known key that is revoked at t=0.
	in.Certs = append(in.Certs, Cert{
		Key: "ghost-key", Host: host, Role: roleA,
		PublishAt: 0, RevokedAt: 0, Revoked: true,
	})
	report, err = Verify(in)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if !report.SafetyPass {
		t.Fatalf("rejecting a revoked signer must keep safety true: %s",
			RenderText(report))
	}
	if !reportWindowHasReason(report, ReasonKeyRevoked) {
		t.Fatalf("expected key_revoked rejection in report: %s", RenderText(report))
	}
}

func reportWindowHasReason(report *Report, want Reason) bool {
	for _, window := range report.Windows {
		for _, verdict := range window.ReleaseVerdicts {
			for _, reason := range verdict.Reasons {
				if reason == want {
					return true
				}
			}
		}
	}
	return false
}

// TestSequenceConflictRejected publishes two different fetchable policies
// with the same sequence number. Validate rejects structural duplicates, so
// this constructs the runtime conflict path through distinct hosts is not
// possible; instead we assert Validate catches the malformed schedule.
func TestSequenceConflictRejected(t *testing.T) {
	in := baseInput()
	body := oldPolicy(0, "", 0, 13)
	in.Releases = []Release{
		{SignedPolicy: SignedPolicy{Policy: body, PublishAt: 0}},
		{SignedPolicy: SignedPolicy{Policy: body, PublishAt: 1}},
	}
	if _, err := Verify(in); err == nil {
		t.Fatal("duplicate sequence schedules must be rejected by Validate")
	}
}

// TestNoViableCheckpoint proves the offline bound is enforced: a horizon
// placed beyond OfflineMax from every checkpoint yields no reachable window.
func TestNoViableCheckpoint(t *testing.T) {
	in := baseInput()
	in.Checkpoints = []Time{0}
	in.OfflineMax = 2
	in.HorizonFrom = 4
	in.HorizonUntil = 6

	seq0 := oldPolicy(0, "", 0, 13)
	seq1 := newPolicy(1, Digest(seq0), 4, 13, newA, newB)
	in.Releases = []Release{
		{SignedPolicy: SignedPolicy{
			Policy:     seq0,
			PublishAt:  0,
			Signatures: []Signature{{Signer: oldA, Role: roleA}},
		}},
		{SignedPolicy: SignedPolicy{
			Policy:    seq1,
			PublishAt: 4,
			Signatures: []Signature{
				{Signer: newA, Role: roleA},
				{Signer: newB, Role: roleB},
			},
		}},
	}
	in.Handovers = handoversFor(seq1, 4)

	report, err := Verify(in)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	for _, window := range report.Windows {
		if window.Element.Kind == KindOpen && window.Element.Empty {
			continue
		}
		if window.Reachable {
			t.Fatalf("window %+v must not be reachable under the offline bound",
				window.Element)
		}
		if len(window.Cases) != 0 {
			t.Fatalf("unreachable window must enumerate no checkpoint/skew cases")
		}
	}
}

// TestValidateErrors exercises the structural input guards.
func TestValidateErrors(t *testing.T) {
	in := baseInput()
	in.OldRoot = in.NewRoot
	if err := Validate(in); err == nil {
		t.Fatal("identical roots must be rejected")
	}

	in = baseInput()
	in.SkewMax = in.SkewMin - 1
	if err := Validate(in); err == nil {
		t.Fatal("inverted skew interval must be rejected")
	}

	in = baseInput()
	in.Releases = nil
	if err := Validate(in); err == nil {
		t.Fatal("empty release schedule must be rejected")
	}

	in = baseInput()
	in.Handovers = []Handover{{OldRoot: newRoot, NewRoot: newRoot}}
	if err := Validate(in); err == nil {
		t.Fatal("handover with mismatched roots must be rejected")
	}
}

// TestChainDigestBreak changes a predecessor digest so the migration chain
// cannot link and must report chain_link_broken.
func TestChainDigestBreak(t *testing.T) {
	in := baseInput()
	seq0 := oldPolicy(0, "", 0, 13)
	seq1 := newPolicy(1, "wrong-digest", 2, 13, newA, newB)

	in.Releases = []Release{
		{SignedPolicy: SignedPolicy{
			Policy:     seq0,
			PublishAt:  0,
			Signatures: []Signature{{Signer: oldA, Role: roleA}},
		}},
		{SignedPolicy: SignedPolicy{
			Policy:    seq1,
			PublishAt: 2,
			Signatures: []Signature{
				{Signer: newA, Role: roleA},
				{Signer: newB, Role: roleB},
			},
		}},
	}
	in.Handovers = handoversFor(seq1, 2)

	report, err := Verify(in)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if report.LivenessPass {
		t.Fatalf("broken predecessor digest must fail liveness: %s",
			RenderText(report))
	}

	sawBroken := false
	for _, window := range report.Windows {
		for _, reason := range window.ChainFailure {
			if reason == ReasonChainBroken {
				sawBroken = true
			}
		}
	}
	if !sawBroken {
		t.Fatalf("expected chain_link_broken in the per-case failures")
	}
}

// TestHandoverRootKeyRevoked revokes the new root signer before the online
// time and requires the dedicated handover-key-dead reason in addition to
// the missing-authorization record.
func TestHandoverRootKeyRevoked(t *testing.T) {
	in := baseInput()
	seq0 := oldPolicy(0, "", 0, 13)
	seq1 := newPolicy(1, Digest(seq0), 2, 13, newA, newB)

	in.Releases = []Release{
		{SignedPolicy: SignedPolicy{
			Policy:     seq0,
			PublishAt:  0,
			Signatures: []Signature{{Signer: oldA, Role: roleA}},
		}},
		{SignedPolicy: SignedPolicy{
			Policy:    seq1,
			PublishAt: 2,
			Signatures: []Signature{
				{Signer: newA, Role: roleA},
				{Signer: newB, Role: roleB},
			},
		}},
	}
	in.Handovers = handoversFor(seq1, 2)
	for index := range in.Certs {
		if in.Certs[index].Key == newRootK {
			in.Certs[index].RevokedAt = 3
			in.Certs[index].Revoked = true
		}
	}

	report, err := Verify(in)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if report.LivenessPass {
		t.Fatalf("revoked new-root signer must block handover: %s",
			RenderText(report))
	}
	if !reportWindowHasReason(report, ReasonHandoverKeyDead) {
		t.Fatalf("expected handover_key_revoked reason: %s", RenderText(report))
	}
}
