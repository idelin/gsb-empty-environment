package threshold_test

import (
	"errors"
	"fmt"
	"math/big"
	"sync"
	"testing"

	tc "formal.local/w27-9f4c534a76"
)

type dkgWorld struct {
	t            *testing.T
	group        tc.Group
	identities   []tc.Identity
	coordinator  *tc.Coordinator
	participants map[tc.ID]*tc.Participant
	round1       map[tc.ID]*tc.Round1Message
}

func newDKGWorld(t *testing.T, threshold, n int) *dkgWorld {
	t.Helper()
	group := tc.P256Group()
	identities := make([]tc.Identity, 0, n)
	for i := 1; i <= n; i++ {
		identities = append(identities, tc.Identity{
			ID:    tc.ID(fmt.Sprintf("p%d", i)),
			Index: i,
		})
	}

	cfg := tc.DKGConfig{
		Group:        group,
		Context:      []byte("threshold-test-context"),
		Threshold:    threshold,
		Participants: identities,
	}
	coordinator, err := tc.NewCoordinator(cfg)
	if err != nil {
		t.Fatalf("NewCoordinator: %v", err)
	}
	cfg.Blacklist = coordinator.Blacklist()

	participants := make(map[tc.ID]*tc.Participant, n)
	for _, ident := range identities {
		cfg.Self = ident.ID
		participant, err := tc.NewParticipant(cfg)
		if err != nil {
			t.Fatalf("NewParticipant(%s): %v", ident.ID, err)
		}
		participants[ident.ID] = participant
	}

	return &dkgWorld{
		t:            t,
		group:        group,
		identities:   identities,
		coordinator:  coordinator,
		participants: participants,
		round1:       make(map[tc.ID]*tc.Round1Message),
	}
}

func (w *dkgWorld) broadcastRound1(id tc.ID) *tc.Round1Message {
	w.t.Helper()
	msg, err := w.participants[id].Round1()
	if err != nil {
		w.t.Fatalf("Round1(%s): %v", id, err)
	}
	if err := w.coordinator.HandleRound1(msg); err != nil {
		w.t.Fatalf("coordinator HandleRound1(%s): %v", id, err)
	}
	for recipientID, recipient := range w.participants {
		if err := recipient.ReceiveRound1(msg); err != nil {
			w.t.Fatalf("ReceiveRound1 from %s at %s: %v", id, recipientID, err)
		}
	}
	w.round1[id] = msg
	return msg
}

func (w *dkgWorld) allRound1() {
	w.t.Helper()
	for _, ident := range w.identities {
		w.broadcastRound1(ident.ID)
	}
}

func (w *dkgWorld) distributeHonestShares() {
	w.t.Helper()
	for _, ident := range w.identities {
		dealer := w.participants[ident.ID]
		shares, err := dealer.CreateShares()
		if err != nil {
			w.t.Fatalf("CreateShares(%s): %v", ident.ID, err)
		}
		for _, share := range shares {
			recipient := w.participants[share.Recipient]
			complaint, err := recipient.ReceiveShare(share)
			if err != nil || complaint != nil {
				w.t.Fatalf("honest share %s->%s: err=%v complaint=%v",
					share.Dealer, share.Recipient, err, complaint)
			}
		}
	}
}

func (w *dkgWorld) freezeAndFinish() (*tc.DKGPublicKey, map[tc.ID]*tc.KeyPackage) {
	w.t.Helper()
	pk, err := w.coordinator.Freeze()
	if err != nil {
		w.t.Fatalf("Freeze: %v", err)
	}
	keys := make(map[tc.ID]*tc.KeyPackage, len(pk.Qualified))
	for _, id := range pk.Qualified {
		kp, err := w.participants[id].Finish(pk.Qualified)
		if err != nil {
			w.t.Fatalf("Finish(%s): %v", id, err)
		}
		keys[id] = kp
	}
	return pk, keys
}

func (w *dkgWorld) honestDKG() (*tc.DKGPublicKey, map[tc.ID]*tc.KeyPackage) {
	w.t.Helper()
	w.allRound1()
	w.distributeHonestShares()
	return w.freezeAndFinish()
}

func requireErrorIs(t *testing.T, err error, target error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %v, got nil", target)
	}
	if !errors.Is(err, target) {
		t.Fatalf("error %v does not wrap %v", err, target)
	}
}

func scalarBytes(t *testing.T, g tc.Group, x *big.Int) []byte {
	t.Helper()
	size := (g.Order().BitLen() + 7) / 8
	out := make([]byte, size)
	b := new(big.Int).Mod(x, g.Order()).Bytes()
	copy(out[size-len(b):], b)
	return out
}

func generateBundles(t *testing.T, keys map[tc.ID]*tc.KeyPackage, ids []tc.ID) map[tc.ID]*tc.NonceBundle {
	t.Helper()
	bundles := make(map[tc.ID]*tc.NonceBundle, len(ids))
	for _, id := range ids {
		signer, err := tc.NewSigner(tc.P256Group(), keys[id], nil)
		if err != nil {
			t.Fatalf("NewSigner(%s): %v", id, err)
		}
		bundle, err := signer.Nonces.Generate()
		if err != nil {
			t.Fatalf("Generate nonce(%s): %v", id, err)
		}
		bundles[id] = bundle
	}
	return bundles
}

func signingPackageFor(t *testing.T, g tc.Group, pk *tc.DKGPublicKey, message []byte, bundles map[tc.ID]*tc.NonceBundle, ids []tc.ID) (*tc.SigningSession, *tc.SigningPackage) {
	t.Helper()
	session, err := tc.NewSigningSession(g, pk, message)
	if err != nil {
		t.Fatalf("NewSigningSession: %v", err)
	}
	for _, id := range ids {
		if err := session.AddCommitment(bundles[id].Public()); err != nil {
			t.Fatalf("AddCommitment(%s): %v", id, err)
		}
	}
	pkg, err := session.FinalizeCommitments()
	if err != nil {
		t.Fatalf("FinalizeCommitments: %v", err)
	}
	return session, pkg
}

func partialFor(t *testing.T, g tc.Group, kp *tc.KeyPackage, pkg *tc.SigningPackage, bundle *tc.NonceBundle) *tc.PartialSignature {
	t.Helper()
	signer, err := tc.NewSigner(g, kp, nil)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	partial, err := signer.SignWithBundle(pkg, bundle)
	if err != nil {
		t.Fatalf("SignWithBundle(%s): %v", kp.ID, err)
	}
	return partial
}

func TestHonestThresholdSigningWithP256(t *testing.T) {
	cases := []struct {
		threshold int
		n         int
	}{
		{1, 1},
		{2, 3},
		{3, 5},
	}

	for _, tcCase := range cases {
		t.Run(fmt.Sprintf("t%d_n%d", tcCase.threshold, tcCase.n), func(t *testing.T) {
			w := newDKGWorld(t, tcCase.threshold, tcCase.n)
			pk, keys := w.honestDKG()
			if len(pk.Qualified) != tcCase.n {
				t.Fatalf("qualified=%v want all %d", pk.Qualified, tcCase.n)
			}

			signerIDs := append([]tc.ID(nil), pk.Qualified[:tcCase.threshold]...)
			message := []byte("approve-vault-withdrawal:amount=42;nonce=honest")
			bundles := generateBundles(t, keys, signerIDs)
			session, pkg := signingPackageFor(t, w.group, pk, message, bundles, signerIDs)

			partials := make([]*tc.PartialSignature, 0, len(signerIDs))
			for _, id := range signerIDs {
				partials = append(partials, partialFor(t, w.group, keys[id], pkg, bundles[id]))
			}

			signature, report, err := session.Aggregate(partials)
			if err != nil {
				t.Fatalf("Aggregate: %v (report=%+v)", err, report)
			}
			if report == nil || !report.OK {
				t.Fatalf("unexpected accountability report: %+v", report)
			}
			if err := tc.VerifySignature(w.group, pk.GroupPublicKey, message, signature); err != nil {
				t.Fatalf("VerifySignature: %v", err)
			}
		})
	}
}

func TestRogueKeyCancellationBlockedByProofOfPossession(t *testing.T) {
	w := newDKGWorld(t, 3, 3)
	honest1 := w.identities[0].ID
	honest2 := w.identities[1].ID
	rogueID := w.identities[2].ID

	msg1 := w.broadcastRound1(honest1)
	msg2 := w.broadcastRound1(honest2)

	targetSecret, err := w.group.RandomScalar(nil)
	if err != nil {
		t.Fatal(err)
	}
	targetPoint := w.group.ScalarBaseMult(targetSecret)

	c01, err := w.group.Decode(msg1.Commitments[0])
	if err != nil {
		t.Fatal(err)
	}
	c02, err := w.group.Decode(msg2.Commitments[0])
	if err != nil {
		t.Fatal(err)
	}
	rogueC0Point := w.group.Add(targetPoint, w.group.Neg(c01))
	rogueC0Point = w.group.Add(rogueC0Point, w.group.Neg(c02))
	rogueC0 := w.group.Encode(rogueC0Point)

	c1Scalar, _ := w.group.RandomScalar(nil)
	c2Scalar, _ := w.group.RandomScalar(nil)
	rogue := &tc.Round1Message{
		Dealer: rogueID,
		Commitments: [][]byte{
			rogueC0,
			w.group.Encode(w.group.ScalarBaseMult(c1Scalar)),
			w.group.Encode(w.group.ScalarBaseMult(c2Scalar)),
		},
		// Structurally valid Schnorr fields, but not a proof of knowledge
		// of the canceled rogueC0.
		PoP: tc.SchnorrProof{
			Nonce:    w.group.Encode(w.group.ScalarBaseMult(big.NewInt(424242))),
			Response: scalarBytes(t, w.group, big.NewInt(7)),
		},
	}

	canceled := w.group.Add(w.group.Add(c01, c02), rogueC0Point)
	if !w.group.Equal(canceled, targetPoint) {
		t.Fatal("test setup failed: rogue C0 does not cancel joint key to target")
	}

	err = w.coordinator.HandleRound1(rogue)
	requireErrorIs(t, err, tc.ErrProofOfPossession)
	if err := tc.VerifyProofOfPossession(
		w.group, []byte("threshold-test-context"), rogueID, rogueC0, rogue.PoP,
	); !errors.Is(err, tc.ErrProofOfPossession) {
		t.Fatalf("direct PoP verification error=%v, want ErrProofOfPossession", err)
	}
	if !w.coordinator.Blacklist().IsBanned(rogueID) {
		t.Fatal("rogue participant was not permanently blacklisted")
	}
	record, ok := w.coordinator.Blacklist().Record(rogueID)
	if !ok || record.Reason != tc.BanInvalidRound1 {
		t.Fatalf("unexpected ban record: %+v ok=%v", record, ok)
	}

	_, err = w.coordinator.Freeze()
	requireErrorIs(t, err, tc.ErrInsufficientQualified)
}

func TestBadShareComplaintRemovesDealerAndThresholdSigningContinues(t *testing.T) {
	w := newDKGWorld(t, 2, 3)
	w.allRound1()

	badDealer := w.identities[0].ID
	victim := w.identities[1].ID
	remaining := []tc.ID{w.identities[1].ID, w.identities[2].ID}

	for _, ident := range w.identities {
		shares, err := w.participants[ident.ID].CreateShares()
		if err != nil {
			t.Fatalf("CreateShares(%s): %v", ident.ID, err)
		}
		for _, share := range shares {
			recipient := w.participants[share.Recipient]
			if share.Dealer == badDealer && share.Recipient == victim {
				share.Share = scalarBytes(t, w.group, big.NewInt(1))
				complaint, receiveErr := recipient.ReceiveShare(share)
				requireErrorIs(t, receiveErr, tc.ErrInvalidShare)
				if complaint == nil {
					t.Fatal("invalid share did not produce complaint evidence")
				}
				if err := complaint.Verify(w.group); err != nil {
					t.Fatalf("independent complaint verification failed: %v", err)
				}
				record, err := w.coordinator.SubmitComplaint(complaint)
				if err != nil {
					t.Fatalf("SubmitComplaint: %v", err)
				}
				if record.ID != badDealer || record.Reason != tc.BanBadShare {
					t.Fatalf("unexpected ban record: %+v", record)
				}
				continue
			}

			complaint, err := recipient.ReceiveShare(share)
			if err != nil || complaint != nil {
				t.Fatalf("share %s->%s: err=%v complaint=%v", share.Dealer, share.Recipient, err, complaint)
			}
		}
	}

	if !w.coordinator.Blacklist().IsBanned(badDealer) {
		t.Fatal("bad dealer not blacklisted")
	}

	// A valid share must not be usable as a false complaint.
	honestShares, err := w.participants[w.identities[2].ID].CreateShares()
	if err != nil {
		t.Fatal(err)
	}
	var validToVictim *tc.Round2Share
	for _, share := range honestShares {
		if share.Recipient == victim {
			validToVictim = share
		}
	}
	falseComplaint := &tc.Complaint{
		Dealer:       w.identities[2].ID,
		Accuser:      victim,
		AccuserIndex: w.identities[1].Index,
		Share:        validToVictim.Share,
		Commitments:  w.round1[w.identities[2].ID].Commitments,
	}
	if err := tc.VerifyComplaint(w.group, *falseComplaint); !errors.Is(err, tc.ErrComplaintUnsubstantiated) {
		t.Fatalf("false complaint error=%v, want ErrComplaintUnsubstantiated", err)
	}
	if err := w.coordinator.HandleComplaint(falseComplaint); !errors.Is(err, tc.ErrInvalidComplaint) {
		t.Fatalf("HandleComplaint(false)=%v, want ErrInvalidComplaint", err)
	}
	if w.coordinator.Blacklist().IsBanned(w.identities[2].ID) {
		t.Fatal("false complaint incorrectly blacklisted honest dealer")
	}

	pk, keys := w.freezeAndFinish()
	if len(pk.Qualified) != 2 || pk.Qualified[0] != remaining[0] || pk.Qualified[1] != remaining[1] {
		t.Fatalf("qualified=%v want %v", pk.Qualified, remaining)
	}

	message := []byte("bad-share-removed-but-quorum-signs")
	bundles := generateBundles(t, keys, remaining)
	session, pkg := signingPackageFor(t, w.group, pk, message, bundles, remaining)
	partials := []*tc.PartialSignature{
		partialFor(t, w.group, keys[remaining[0]], pkg, bundles[remaining[0]]),
		partialFor(t, w.group, keys[remaining[1]], pkg, bundles[remaining[1]]),
	}
	sig, report, err := session.Aggregate(partials)
	if err != nil {
		t.Fatalf("Aggregate after complaint: %v report=%+v", err, report)
	}
	if !report.OK {
		t.Fatalf("report not OK: %+v", report)
	}
	if err := tc.Verify(w.group, pk.GroupPublicKey, message, sig); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestConcurrentNonceReplayIsBlocked(t *testing.T) {
	w := newDKGWorld(t, 2, 3)
	pk, keys := w.honestDKG()
	ids := pk.Qualified[:2]
	message := []byte("concurrent-replay-message")

	signer1, err := tc.NewSigner(w.group, keys[ids[0]], nil)
	if err != nil {
		t.Fatal(err)
	}
	bundle1, err := signer1.Nonces.Generate()
	if err != nil {
		t.Fatal(err)
	}
	bundle2, err := signer1.Nonces.Generate()
	if err != nil {
		t.Fatal(err)
	}
	signer2, err := tc.NewSigner(w.group, keys[ids[1]], nil)
	if err != nil {
		t.Fatal(err)
	}
	bundle2ForSigner2, err := signer2.Nonces.Generate()
	if err != nil {
		t.Fatal(err)
	}

	session, err := tc.NewSigningSession(w.group, pk, message)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.AddCommitment(bundle1.Public()); err != nil {
		t.Fatal(err)
	}
	if err := session.AddCommitment(bundle2ForSigner2.Public()); err != nil {
		t.Fatal(err)
	}
	pkg, err := session.FinalizeCommitments()
	if err != nil {
		t.Fatal(err)
	}

	const goroutines = 32
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	replays := 0
	var successfulPartial *tc.PartialSignature

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			partial, err := signer1.SignWithBundle(pkg, bundle1)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				successes++
				successfulPartial = partial
			case errors.Is(err, tc.ErrNonceReuse):
				replays++
			default:
				t.Errorf("unexpected SignWithBundle error: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if successes != 1 {
		t.Fatalf("successes=%d want 1", successes)
	}
	if replays != goroutines-1 {
		t.Fatalf("replays=%d want %d", replays, goroutines-1)
	}
	if !bundle1.Consumed() {
		t.Fatal("bundle not marked consumed")
	}
	_, err = signer1.Round2Sign(pkg, bundle1.Commitment.ID)
	requireErrorIs(t, err, tc.ErrNonceReuse)

	partial2, err := signer2.SignWithBundle(pkg, bundle2ForSigner2)
	if err != nil {
		t.Fatalf("honest second signer: %v", err)
	}
	sig, report, err := session.Aggregate([]*tc.PartialSignature{successfulPartial, partial2})
	if err != nil {
		t.Fatalf("Aggregate: %v report=%+v", err, report)
	}
	if !report.OK {
		t.Fatalf("report: %+v", report)
	}
	if err := tc.Verify(w.group, pk.GroupPublicKey, message, sig); err != nil {
		t.Fatalf("Verify after concurrent nonce race: %v", err)
	}

	// This unrelated bundle was generated for signer1 but never used; ensure
	// the test setup did not accidentally consume every nonce.
	if bundle2.Consumed() {
		t.Fatal("unrelated bundle was consumed")
	}
}

func TestBadPartialSignatureIsPreciselyIdentified(t *testing.T) {
	w := newDKGWorld(t, 2, 3)
	pk, keys := w.honestDKG()
	ids := pk.Qualified[:2]
	message := []byte("bad-partial-accountability")
	bundles := generateBundles(t, keys, ids)
	session, pkg := signingPackageFor(t, w.group, pk, message, bundles, ids)

	good := partialFor(t, w.group, keys[ids[0]], pkg, bundles[ids[0]])
	bad := partialFor(t, w.group, keys[ids[1]], pkg, bundles[ids[1]])

	z := new(big.Int).SetBytes(bad.Z)
	z.Add(z, big.NewInt(1))
	bad.Z = scalarBytes(t, w.group, z)

	signature, report, err := session.Aggregate([]*tc.PartialSignature{good, bad})
	if signature != nil {
		t.Fatal("Aggregate returned a signature for bad partial")
	}
	var accountability *tc.AccountabilityError
	if !errors.As(err, &accountability) {
		t.Fatalf("error %v is not *AccountabilityError", err)
	}
	if !errors.Is(err, tc.ErrAccountability) || !errors.Is(err, tc.ErrInvalidPartialSignature) {
		t.Fatalf("error %v missing accountability sentinels", err)
	}
	if report == nil || report.OK {
		t.Fatalf("bad report: %+v", report)
	}
	if len(report.Invalid) != 1 || report.Invalid[0].Signer != ids[1] {
		t.Fatalf("Invalid=%+v want only %s", report.Invalid, ids[1])
	}
	if len(report.Culprits) != 1 || report.Culprits[0] != ids[1] {
		t.Fatalf("Culprits=%v want exactly [%s]", report.Culprits, ids[1])
	}
	if report.Invalid[0].Reason == nil || !errors.Is(report.Invalid[0].Reason, tc.ErrInvalidPartialSignature) {
		t.Fatalf("failure reason %v does not wrap ErrInvalidPartialSignature", report.Invalid[0].Reason)
	}
	if session.State() != tc.StateFailed {
		t.Fatalf("state=%s want failed", session.State())
	}

	verifyReport, verifyErr := session.VerifyPartials([]*tc.PartialSignature{good, bad})
	if verifyErr != nil {
		t.Fatal(verifyErr)
	}
	if len(verifyReport.Culprits) != 1 || verifyReport.Culprits[0] != ids[1] {
		t.Fatalf("VerifyPartials culprits=%v", verifyReport.Culprits)
	}
}

func TestBindingFactorBindsEveryCommitment(t *testing.T) {
	g := tc.P256Group()
	indices := map[tc.ID]int{"p1": 1, "p2": 2}
	makeCommitment := func(id tc.ID, seed int64) *tc.NonceCommitment {
		d := big.NewInt(seed)
		e := big.NewInt(seed + 100)
		return &tc.NonceCommitment{
			ID:     fmt.Sprintf("nonce-%s", id),
			Signer: id,
			D:      g.Encode(g.ScalarBaseMult(d)),
			E:      g.Encode(g.ScalarBaseMult(e)),
		}
	}

	c1 := makeCommitment("p1", 7)
	c2 := makeCommitment("p2", 11)
	message := []byte("rho-binding")
	rhoBefore, err := tc.BindingFactor(g, indices, "p1", message, []*tc.NonceCommitment{c1, c2})
	if err != nil {
		t.Fatal(err)
	}

	tampered := makeCommitment("p2", 12)
	rhoAfter, err := tc.BindingFactor(g, indices, "p1", message, []*tc.NonceCommitment{c1, tampered})
	if err != nil {
		t.Fatal(err)
	}
	if rhoBefore.Cmp(rhoAfter) == 0 {
		t.Fatal("rho unchanged after changing another participant's (D,E) commitment")
	}

	rhoOtherMessage, err := tc.BindingFactor(g, indices, "p1", []byte("other"), []*tc.NonceCommitment{c1, c2})
	if err != nil {
		t.Fatal(err)
	}
	if rhoBefore.Cmp(rhoOtherMessage) == 0 {
		t.Fatal("rho unchanged after changing message")
	}
}

func TestSigningSessionRejectsSameCommitmentTupleUnderOtherID(t *testing.T) {
	w := newDKGWorld(t, 2, 3)
	pk, _ := w.honestDKG()
	ids := pk.Qualified[:2]

	bundle, err := tc.NewNonceVault(w.group, ids[0], nil).Generate()
	if err != nil {
		t.Fatal(err)
	}
	session, err := tc.NewSigningSession(w.group, pk, []byte("replay-tuple"))
	if err != nil {
		t.Fatal(err)
	}
	if err := session.AddCommitment(bundle.Public()); err != nil {
		t.Fatal(err)
	}

	replayedUnderOtherID := bundle.Public()
	replayedUnderOtherID.Signer = ids[1]
	replayedUnderOtherID.ID = "different-nonce-id"
	err = session.AddCommitment(replayedUnderOtherID)
	requireErrorIs(t, err, tc.ErrNonceReuse)
}
