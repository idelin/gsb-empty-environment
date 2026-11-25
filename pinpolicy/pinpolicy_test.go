package pinpolicy

import "testing"

const (
	host     HostID = "term-1"
	oldRoot  RootID = "root-old"
	newRoot  RootID = "root-new"
	roleA    Role   = "deploy"
	roleB    Role   = "ops"
	oldA     KeyID  = "old-a"
	oldB     KeyID  = "old-b"
	newA     KeyID  = "new-a"
	newB     KeyID  = "new-b"
	newA2    KeyID  = "new-a2"
	oldRootK KeyID  = "old-root-key"
	newRootK KeyID  = "new-root-key"
	rogueBK  KeyID  = "rogue-backup"
)

func baseCerts() []Cert {
	return []Cert{
		{Key: oldRootK, Host: host, Role: RootRole, RootKey: oldRoot, PublishAt: 0},
		{Key: newRootK, Host: host, Role: RootRole, RootKey: newRoot, PublishAt: 0},
		{Key: oldA, Host: host, Role: roleA, PublishAt: 0},
		{Key: oldB, Host: host, Role: roleB, PublishAt: 0},
		{Key: newA, Host: host, Role: roleA, PublishAt: 0},
		{Key: newB, Host: host, Role: roleB, PublishAt: 0},
		{Key: newA2, Host: host, Role: roleA, PublishAt: 0},
		{Key: rogueBK, Host: host, Role: roleA, PublishAt: 0},
	}
}

func baseInput() Input {
	return Input{
		OldRoot:      oldRoot,
		NewRoot:      newRoot,
		Certs:        baseCerts(),
		Checkpoints:  []Time{0, 2, 8},
		OfflineMax:   6,
		SkewMin:      0,
		SkewMax:      1,
		HorizonFrom:  0,
		HorizonUntil: 12,
	}
}

func oldPolicy(seq Seq, prev string, from, until Time) Policy {
	return Policy{
		Sequence:   seq,
		Host:       host,
		ValidFrom:  from,
		ValidUntil: until,
		Primary:    oldA,
		Backup:     oldB,
		RootKey:    oldRoot,
		Thresholds: map[Role]int{roleA: 1},
		PrevDigest: prev,
	}
}

func newPolicy(seq Seq, prev string, from, until Time, primary, backup KeyID) Policy {
	return Policy{
		Sequence:   seq,
		Host:       host,
		ValidFrom:  from,
		ValidUntil: until,
		Primary:    primary,
		Backup:     backup,
		RootKey:    newRoot,
		Thresholds: map[Role]int{roleA: 1, roleB: 1},
		PrevDigest: prev,
	}
}

func handoversFor(policy Policy, publishAt Time) []Handover {
	digest := Digest(policy)
	roles := make([]Role, 0, len(policy.Thresholds))
	for role := range policy.Thresholds {
		roles = append(roles, role)
	}
	out := make([]Handover, 0, len(roles))
	for _, role := range roles {
		out = append(out, Handover{
			Host:         host,
			PolicyDigest: digest,
			Role:         role,
			OldRoot:      oldRoot,
			NewRoot:      newRoot,
			OldSigner:    oldRootK,
			NewSigner:    newRootK,
			PublishAt:    publishAt,
		})
	}
	return out
}

func hasReason(reasons []Reason, want Reason) bool {
	for _, reason := range reasons {
		if reason == want {
			return true
		}
	}
	return false
}

// TestCrossWindowRenewal is the passing control case: the old-root policy
// window closes before the migration policy window opens, and the new policy
// is renewed once inside the horizon. Every legal window must still contain
// a verifiable chain.
func TestCrossWindowRenewal(t *testing.T) {
	in := baseInput()

	// Windows overlap one time unit so every skew in [-1,1] at any legal
	// online time still finds a locally-valid policy; the sequence and digest
	// links distinguish renewal from window closure.
	seq0 := oldPolicy(0, "", 0, 4)
	seq1 := newPolicy(1, Digest(seq0), 3, 8, newA, newB)
	seq2 := newPolicy(2, Digest(seq1), 7, 13, newA2, newB)
	handover1 := handoversFor(seq1, 3)
	handover2 := handoversFor(seq2, 8)

	in.Releases = []Release{
		{SignedPolicy: SignedPolicy{
			Policy:     seq0,
			PublishAt:  0,
			Signatures: []Signature{{Signer: oldA, Role: roleA}},
		}},
		{SignedPolicy: SignedPolicy{
			Policy:    seq1,
			PublishAt: 3,
			Signatures: []Signature{
				{Signer: newA, Role: roleA},
				{Signer: newB, Role: roleB},
			},
		}},
		{SignedPolicy: SignedPolicy{
			Policy:    seq2,
			PublishAt: 8,
			Signatures: []Signature{
				{Signer: newA2, Role: roleA},
				{Signer: newB, Role: roleB},
			},
		}},
	}
	in.Handovers = append(handover1, handover2...)

	report, err := Verify(in)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if !report.Pass || !report.SafetyPass || !report.LivenessPass {
		t.Fatalf("expected passing report, got %s", RenderText(report))
	}

	var sawPoint3, sawOpen37 bool
	for _, window := range report.Windows {
		if window.Element.Kind == KindPoint && window.Element.At == 3 {
			sawPoint3 = true
			if !window.MigrationFetchable || !window.ChainOK {
				t.Fatalf("t=3 must be a legal migration window: %+v", window)
			}
		}
		if window.Element.Kind == KindOpen &&
			window.Element.From == 4 && window.Element.Until == 7 {
			sawOpen37 = true
			if window.Element.Sample != 5 || window.Element.Empty {
				t.Fatalf("open (4,7) should sample integer 5: %+v", window.Element)
			}
			if !window.ChainOK {
				t.Fatalf("open (4,7) should keep the chain verifiable: %+v", window)
			}
		}
	}
	if !sawPoint3 || !sawOpen37 {
		t.Fatalf("partition lost point/open distinction: %s", RenderText(report))
	}

	// Every admissible case in a legal window must verify, including the two
	// extreme skews: this is the explicit all-skew portion of the proof.
	for _, window := range report.Windows {
		if !window.MigrationFetchable || !window.Reachable {
			continue
		}
		if len(window.ChainFailure) != 0 {
			t.Fatalf("legal window must verify for every checkpoint/skew: %s",
				RenderText(report))
		}
	}
}

// TestBackupKeyRollback inserts an obsolete sequence-0 release carrying a
// backup signature once sequence 1 is published. It must be blocked at every
// fetchable window while the legitimate migration chain stays usable.
func TestBackupKeyRollback(t *testing.T) {
	in := baseInput()

	seq0 := oldPolicy(0, "", 0, 12)
	seq1 := newPolicy(1, Digest(seq0), 2, 13, newA, newB)
	// A separate, already-superseded policy number. Once seq=1 is published
	// any backup signature on this older number is a rollback attempt.
	rollback := oldPolicy(-1, "", 0, 12)

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
		{SignedPolicy: SignedPolicy{
			Policy:    rollback,
			PublishAt: 2,
			Signatures: []Signature{
				{Signer: oldA, Role: roleA},
				{Signer: rogueBK, Role: roleA, Backup: true},
			},
		}},
	}
	in.Handovers = handoversFor(seq1, 2)

	report, err := Verify(in)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}

	rollbackBlocked := 0
	for _, window := range report.Windows {
		if window.RealTime < 2 {
			continue
		}
		for _, verdict := range window.ReleaseVerdicts {
			if verdict.Sequence != -1 {
				continue
			}
			if !hasReason(verdict.Reasons, ReasonBackupRollback) {
				t.Fatalf("obsolete seq=0 backup rollback not blocked at t=%d: %+v",
					window.RealTime, verdict)
			}
			rollbackBlocked++
		}
	}
	if rollbackBlocked == 0 {
		t.Fatalf("expected rollback rejections, report=%s", RenderText(report))
	}
	if !report.SafetyPass || !report.LivenessPass {
		t.Fatalf("legitimate chain must survive rollback rejection: %s",
			RenderText(report))
	}
}

// TestSplitRolesAcrossTwoPolicies publishes two new-root policies that each
// carry only one of the two required handover roles. Neither policy alone
// covers every handover role, so liveness fails and the earliest failure
// names the missing role authorization.
func TestSplitRolesAcrossTwoPolicies(t *testing.T) {
	in := baseInput()

	seq0 := oldPolicy(0, "", 0, 12)
	partialA := newPolicy(1, Digest(seq0), 2, 12, newA, newB)
	partialA.Thresholds = map[Role]int{roleA: 1}
	partialB := newPolicy(2, Digest(seq0), 2, 12, newA2, newB)
	partialB.Thresholds = map[Role]int{roleB: 1}

	in.Releases = []Release{
		{SignedPolicy: SignedPolicy{
			Policy:     seq0,
			PublishAt:  0,
			Signatures: []Signature{{Signer: oldA, Role: roleA}},
		}},
		{SignedPolicy: SignedPolicy{
			Policy:     partialA,
			PublishAt:  2,
			Signatures: []Signature{{Signer: newA, Role: roleA}},
		}},
		{SignedPolicy: SignedPolicy{
			Policy:     partialB,
			PublishAt:  2,
			Signatures: []Signature{{Signer: newB, Role: roleB}},
		}},
	}
	in.Handovers = handoversFor(partialA, 2)

	report, err := Verify(in)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if report.LivenessPass || report.Pass {
		t.Fatalf("split-role policies must not pass: %s", RenderText(report))
	}

	failure := report.EarliestFailure
	if failure == nil {
		t.Fatal("expected earliest failure to be reported")
	}
	allowed := map[Reason]bool{
		ReasonHandoverMissing: true,
		ReasonChainBroken:     true,
		ReasonWindowClosed:    true,
	}
	if !allowed[failure.Reason] {
		t.Fatalf("unexpected earliest failure reason %s", failure.Reason)
	}

	foundMissingRole := false
	for _, window := range report.Windows {
		for _, verdict := range window.ReleaseVerdicts {
			if hasReason(verdict.Reasons, ReasonHandoverMissing) {
				for _, missing := range verdict.Missing {
					if missing.Role == roleA || missing.Role == roleB {
						foundMissingRole = true
					}
				}
			}
		}
	}
	if !foundMissingRole {
		t.Fatalf("failure must name the missing handover role: %s", RenderText(report))
	}
}

// TestRevocationExactlyAtOnlineBoundary revokes the migration primary key at
// t=5, immediately after the migration publishes at t=4. The exact point
// t=5 must reject the key because revocation uses an open edge, while the
// open interval (4,5) has no interior integer and is reported empty.
func TestRevocationExactlyAtOnlineBoundary(t *testing.T) {
	in := baseInput()

	seq0 := oldPolicy(0, "", 0, 12)
	seq1 := newPolicy(1, Digest(seq0), 4, 12, newA, newB)

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
	for index := range in.Certs {
		if in.Certs[index].Key == newA {
			in.Certs[index].RevokedAt = 5
			in.Certs[index].Revoked = true
		}
	}

	report, err := Verify(in)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}

	var sawEmpty45, sawPoint5 bool
	for _, window := range report.Windows {
		element := window.Element
		if element.Kind == KindOpen && element.From == 4 && element.Until == 5 {
			sawEmpty45 = element.Empty
		}
		if element.Kind == KindPoint && element.At == 5 {
			sawPoint5 = true
			blocked := false
			for _, verdict := range window.ReleaseVerdicts {
				if verdict.Sequence == 1 &&
					hasReason(verdict.Reasons, ReasonKeyRevoked) {
					blocked = true
				}
			}
			if !blocked {
				t.Fatalf("key revoked at t=5 must be rejected at the exact point: %s",
					RenderText(report))
			}
			if window.ChainOK {
				t.Fatalf("chain at t=5 must not verify with a revoked migration key")
			}
		}
		if element.Kind == KindPoint && element.At == 4 {
			if !window.ChainOK {
				t.Fatalf("at t=4 the not-yet-revoked key must still verify: %s",
					RenderText(report))
			}
		}
	}
	if !sawEmpty45 || !sawPoint5 {
		t.Fatalf("boundary partition must show empty (4,5) and point 5: %s",
			RenderText(report))
	}
	if report.SafetyPass != true {
		t.Fatalf("revocation must never be accepted, so safety stays true: %s",
			RenderText(report))
	}
	if report.LivenessPass {
		t.Fatalf("a legal window without a live migration key must fail liveness: %s",
			RenderText(report))
	}
}

// TestPartitionDistinguishesPointAndOpen checks that adjacent event times
// produce an explicitly empty open interval while a wider gap is sampled at
// a deterministic interior integer.
func TestPartitionDistinguishesPointAndOpen(t *testing.T) {
	in := baseInput()
	in.HorizonFrom = 0
	in.HorizonUntil = 6
	seq0 := oldPolicy(0, "", 0, 2)
	seq1 := newPolicy(1, Digest(seq0), 5, 6, newA, newB)
	in.Releases = []Release{
		{SignedPolicy: SignedPolicy{
			Policy:     seq0,
			PublishAt:  0,
			Signatures: []Signature{{Signer: oldA, Role: roleA}},
		}},
		{SignedPolicy: SignedPolicy{
			Policy:    seq1,
			PublishAt: 5,
			Signatures: []Signature{
				{Signer: newA, Role: roleA},
				{Signer: newB, Role: roleB},
			},
		}},
	}
	in.Handovers = handoversFor(seq1, 5)

	report, err := Verify(in)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}

	kinds := map[string]bool{}
	for _, element := range report.Elements {
		if element.Kind == KindOpen {
			if element.Until-element.From == 1 && !element.Empty {
				t.Fatalf("interval between adjacent integers must be empty: %+v", element)
			}
			if element.Until-element.From >= 2 &&
				(element.Empty || element.Sample != element.From+1) {
				t.Fatalf("wide open interval must sample from+1: %+v", element)
			}
		}
		kinds[string(element.Kind)] = true
	}
	if !kinds[string(KindPoint)] || !kinds[string(KindOpen)] {
		t.Fatalf("expected both point and open elements: %+v", report.Elements)
	}
}
