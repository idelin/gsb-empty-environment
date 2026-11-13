package netsec_test

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
	"time"

	netsec "example.org/netsec/q0154"
)

type directoryRecord struct {
	target netsec.PublicIdentity
	spkID  uint32
	spk    []byte
	opkID  uint32
	opk    []byte
}

func recordFor(bundle *netsec.PreKeyBundle) *directoryRecord {
	record := &directoryRecord{
		target: mustPublic(bundle),
		spkID:  bundle.SignedPreKeyID,
		spk:    append([]byte(nil), bundle.SignedPreKey.Bytes()...),
		opkID:  bundle.OneTimePreKeyID,
	}
	if bundle.OneTimePreKey != nil {
		record.opk = append([]byte(nil), bundle.OneTimePreKey.Bytes()...)
	}
	return record
}

func (d *directoryRecord) prove(_ context.Context, target netsec.PublicIdentity, bundle *netsec.PreKeyBundle) ([]byte, error) {
	if !target.Equal(d.target) {
		return nil, errors.New("directory: target identity mismatch")
	}
	if bundle.SignedPreKeyID != d.spkID || !bytes.Equal(bundle.SignedPreKey.Bytes(), d.spk) {
		return nil, errors.New("directory: signed prekey does not belong to target")
	}
	if bundle.OneTimePreKeyID != d.opkID {
		return nil, errors.New("directory: one-time prekey ID does not belong to target")
	}
	if d.opk == nil {
		if bundle.OneTimePreKey != nil {
			return nil, errors.New("directory: unexpected one-time prekey")
		}
	} else if bundle.OneTimePreKey == nil || !bytes.Equal(bundle.OneTimePreKey.Bytes(), d.opk) {
		return nil, errors.New("directory: one-time prekey does not belong to target")
	}
	digest := sha256.Sum256(append([]byte("test-directory/v1"), bundle.SignedBytes()...))
	return digest[:], nil
}

func mustPublic(bundle *netsec.PreKeyBundle) netsec.PublicIdentity {
	identity, err := bundle.RegisteredIdentity()
	if err != nil {
		panic(err)
	}
	return identity
}

func mustIdentity(t *testing.T, account, device string) *netsec.DeviceIdentity {
	t.Helper()
	identity, err := netsec.NewDeviceIdentity(account, device)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func mustSignedPreKey(t *testing.T, id uint32) *netsec.SignedPreKey {
	t.Helper()
	key, err := netsec.NewSignedPreKey(id)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func mustOneTimePreKey(t *testing.T, id uint32) *netsec.OneTimePreKey {
	t.Helper()
	key, err := netsec.NewOneTimePreKey(id)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func newReceiver(t *testing.T, identity *netsec.DeviceIdentity, withOPK bool) (*netsec.Receiver, *netsec.PreKeyBundle, *directoryRecord, time.Time) {
	t.Helper()
	spk := mustSignedPreKey(t, 1001)
	expiresAt := time.Now().Add(time.Hour)
	cfg := netsec.ReceiverConfig{
		Identity:     identity,
		SignedPreKey: spk,
		ExpiresAt:    expiresAt,
	}
	var opkID uint32
	if withOPK {
		opk := mustOneTimePreKey(t, 2001)
		cfg.OneTimePreKeys = []*netsec.OneTimePreKey{opk}
		opkID = opk.ID
	}
	receiver, err := netsec.NewReceiver(cfg)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := receiver.PreKeyBundle(opkID)
	if err != nil {
		t.Fatal(err)
	}
	return receiver, bundle, recordFor(bundle), expiresAt
}

func newInitiator(t *testing.T, identity *netsec.DeviceIdentity, record *directoryRecord) *netsec.Initiator {
	t.Helper()
	initiator, err := netsec.NewInitiator(netsec.InitiatorConfig{
		Identity:       identity,
		DirectoryProof: record.prove,
	})
	if err != nil {
		t.Fatal(err)
	}
	return initiator
}

func TestHandshakeRoundTripAndRetransmission(t *testing.T) {
	alice := mustIdentity(t, "alice", "phone")
	bob := mustIdentity(t, "bob", "laptop")
	receiver, bundle, record, _ := newReceiver(t, bob, true)
	initiator := newInitiator(t, alice, record)

	handshake, err := initiator.Start(bob.Public(), bundle, []byte("hello bob"))
	if err != nil {
		t.Fatal(err)
	}
	if handshake.Mode() != netsec.PreKeyModeOneTimePreKey {
		t.Fatalf("mode = %s", handshake.Mode())
	}
	result, err := receiver.AcceptInitial(handshake.Message(), alice.Public())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result.Plaintext, []byte("hello bob")) {
		t.Fatalf("plaintext = %q", result.Plaintext)
	}
	if !bytes.Equal(result.RootKey, handshake.RootKey()) {
		t.Fatal("initiator and receiver root keys differ")
	}
	if result.Mode != netsec.PreKeyModeOneTimePreKey || !result.OneTimePreKeyUsed {
		t.Fatalf("unexpected mode/OPK state: %+v", result)
	}
	if result.OneTimePreKeyID != bundle.OneTimePreKeyID {
		t.Fatalf("OPK ID = %d", result.OneTimePreKeyID)
	}
	if !receiver.OPKUsed(bundle.OneTimePreKeyID) {
		t.Fatal("OPK was not consumed")
	}

	wire, err := handshake.MessageBytes()
	if err != nil {
		t.Fatal(err)
	}
	retransmitted, err := netsec.ParseInitialMessage(wire)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := receiver.AcceptInitial(retransmitted, alice.Public())
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Replayed {
		t.Fatal("retransmission was not marked as a replay")
	}
	if !bytes.Equal(replay.Plaintext, result.Plaintext) || !bytes.Equal(replay.RootKey, result.RootKey) {
		t.Fatal("retransmission did not return the original result")
	}
	if receiver.AcceptedHandshakeCount() != 1 {
		t.Fatalf("accepted handshakes = %d", receiver.AcceptedHandshakeCount())
	}
}

func TestRetransmissionAfterExpiryReturnsOriginalResult(t *testing.T) {
	now := time.Now()
	alice := mustIdentity(t, "alice", "phone")
	bob := mustIdentity(t, "bob", "laptop")
	spk := mustSignedPreKey(t, 1101)
	opk := mustOneTimePreKey(t, 2101)
	expiresAt := now.Add(time.Minute)
	receiver, err := netsec.NewReceiver(netsec.ReceiverConfig{
		Identity:       bob,
		SignedPreKey:   spk,
		OneTimePreKeys: []*netsec.OneTimePreKey{opk},
		ExpiresAt:      expiresAt,
		Now:            func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := receiver.PreKeyBundle(opk.ID)
	if err != nil {
		t.Fatal(err)
	}
	record := recordFor(bundle)
	initiator, err := netsec.NewInitiator(netsec.InitiatorConfig{
		Identity:       alice,
		DirectoryProof: record.prove,
		Now:            func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	handshake, err := initiator.Start(bob.Public(), bundle, []byte("before expiry"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := receiver.AcceptInitial(handshake.Message(), alice.Public())
	if err != nil {
		t.Fatal(err)
	}

	now = now.Add(2 * time.Minute)
	replayed, err := receiver.AcceptInitial(handshake.Message(), alice.Public())
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.Replayed || !bytes.Equal(replayed.Plaintext, first.Plaintext) {
		t.Fatal("cached retransmission was not returned after expiry")
	}
	if _, err := initiator.Start(bob.Public(), bundle, []byte("too late")); !errors.Is(err, netsec.ErrBundleExpired) {
		t.Fatalf("new handshake after expiry error = %v", err)
	}
}

func TestNoOneTimePreKeyStillEstablishes(t *testing.T) {
	alice := mustIdentity(t, "alice", "phone")
	bob := mustIdentity(t, "bob", "laptop")
	receiver, bundle, record, _ := newReceiver(t, bob, false)
	initiator := newInitiator(t, alice, record)

	handshake, err := initiator.Start(bob.Public(), bundle, []byte("signed-only"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := receiver.AcceptInitial(handshake.Message(), alice.Public())
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != netsec.PreKeyModeSignedPreKeyOnly {
		t.Fatalf("mode = %s", result.Mode)
	}
	if result.OneTimePreKeyUsed || result.OneTimePreKeyID != 0 {
		t.Fatalf("unexpected OPK state: %+v", result)
	}
	if !bytes.Equal(result.RootKey, handshake.RootKey()) {
		t.Fatal("root keys differ")
	}
}

func TestIdentityKeySubstitutionRejected(t *testing.T) {
	alice := mustIdentity(t, "alice", "phone")
	bob := mustIdentity(t, "bob", "laptop")
	_, bobBundle, record, expiry := newReceiver(t, bob, true)
	initiator := newInitiator(t, alice, record)
	mallory := mustIdentity(t, "mallory", "tablet")

	tampered := bobBundle.Clone()
	tampered.IdentityKey = mallory.IdentityPublicKey()
	if _, err := initiator.Start(bob.Public(), tampered, []byte("x")); !errors.Is(err, netsec.ErrInvalidSignature) {
		t.Fatalf("tampered identity key error = %v", err)
	}

	forged := &netsec.PreKeyBundle{
		Version:        netsec.ProtocolVersion,
		AccountID:      bob.AccountID(),
		DeviceID:       bob.DeviceID(),
		SigningKey:     mallory.SigningPublicKey(),
		IdentityKey:    mallory.IdentityPublicKey(),
		SignedPreKeyID: bobBundle.SignedPreKeyID,
		SignedPreKey:   bobBundle.SignedPreKey,
		ExpiresAt:      expiry,
	}
	if err := forged.Sign(mallory); err != nil {
		t.Fatal(err)
	}
	if _, err := initiator.Start(bob.Public(), forged, []byte("x")); !errors.Is(err, netsec.ErrIdentityMismatch) {
		t.Fatalf("forged identity error = %v", err)
	}
}

func TestSignedPreKeyTransplantRejected(t *testing.T) {
	alice := mustIdentity(t, "alice", "phone")
	bob := mustIdentity(t, "bob", "laptop")
	_, bobBundle, bobRecord, expiry := newReceiver(t, bob, true)
	initiator := newInitiator(t, alice, bobRecord)

	mallory := mustIdentity(t, "mallory", "tablet")
	mallorySPK := mustSignedPreKey(t, 3001)
	malloryBundle, err := netsec.BuildPreKeyBundle(mallory, mallorySPK, nil, expiry)
	if err != nil {
		t.Fatal(err)
	}
	malloryRecord := recordFor(malloryBundle)
	malloryInitiator := newInitiator(t, alice, malloryRecord)

	transplant := malloryBundle.Clone()
	transplant.SignedPreKeyID = bobBundle.SignedPreKeyID
	transplant.SignedPreKey = bobBundle.SignedPreKey
	if err := transplant.Sign(mallory); err != nil {
		t.Fatal(err)
	}
	if _, err := malloryInitiator.Start(mallory.Public(), transplant, []byte("x")); !errors.Is(err, netsec.ErrDirectoryProof) {
		t.Fatalf("transplanted signed prekey error = %v", err)
	}

	byteTampered := bobBundle.Clone()
	byteTampered.SignedPreKey = mallorySPK.PublicKey()
	if _, err := initiator.Start(bob.Public(), byteTampered, []byte("x")); !errors.Is(err, netsec.ErrInvalidSignature) {
		t.Fatalf("byte-tampered signed prekey error = %v", err)
	}
}

func TestOneTimePreKeyReuseRejected(t *testing.T) {
	alice := mustIdentity(t, "alice", "phone")
	bob := mustIdentity(t, "bob", "laptop")
	receiver, bundle, record, _ := newReceiver(t, bob, true)
	initiator := newInitiator(t, alice, record)

	first, err := initiator.Start(bob.Public(), bundle, []byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := receiver.AcceptInitial(first.Message(), alice.Public()); err != nil {
		t.Fatal(err)
	}

	second, err := initiator.Start(bob.Public(), bundle, []byte("second"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := receiver.AcceptInitial(second.Message(), alice.Public()); !errors.Is(err, netsec.ErrOneTimePreKeyUsed) {
		t.Fatalf("OPK reuse error = %v", err)
	}
	if receiver.AcceptedHandshakeCount() != 1 {
		t.Fatalf("accepted handshakes = %d", receiver.AcceptedHandshakeCount())
	}
}

func TestDirectoryTargetMismatchRejected(t *testing.T) {
	alice := mustIdentity(t, "alice", "phone")
	bob := mustIdentity(t, "bob", "laptop")
	carol := mustIdentity(t, "carol", "desktop")
	_, bundle, record, _ := newReceiver(t, bob, true)
	initiator := newInitiator(t, alice, record)

	if _, err := initiator.Start(carol.Public(), bundle, []byte("x")); !errors.Is(err, netsec.ErrIdentityMismatch) {
		t.Fatalf("target mismatch error = %v", err)
	}

	failing, err := netsec.NewInitiator(netsec.InitiatorConfig{
		Identity: alice,
		DirectoryProof: func(context.Context, netsec.PublicIdentity, *netsec.PreKeyBundle) ([]byte, error) {
			return nil, errors.New("not published for this device")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := failing.Start(bob.Public(), bundle, []byte("x")); !errors.Is(err, netsec.ErrDirectoryProof) {
		t.Fatalf("directory rejection error = %v", err)
	}
}

func TestReceiverProofVerifierRejectsBeforeConsumption(t *testing.T) {
	alice := mustIdentity(t, "alice", "phone")
	bob := mustIdentity(t, "bob", "laptop")
	spk := mustSignedPreKey(t, 1201)
	opk := mustOneTimePreKey(t, 2201)
	expiresAt := time.Now().Add(time.Hour)
	receiver, err := netsec.NewReceiver(netsec.ReceiverConfig{
		Identity:       bob,
		SignedPreKey:   spk,
		OneTimePreKeys: []*netsec.OneTimePreKey{opk},
		ExpiresAt:      expiresAt,
		DirectoryProofVerifier: func(context.Context, netsec.PublicIdentity, []byte) error {
			return errors.New("proof is not valid for this device")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := receiver.PreKeyBundle(opk.ID)
	if err != nil {
		t.Fatal(err)
	}
	initiator := newInitiator(t, alice, recordFor(bundle))
	handshake, err := initiator.Start(bob.Public(), bundle, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := receiver.AcceptInitial(handshake.Message(), alice.Public()); !errors.Is(err, netsec.ErrDirectoryProof) {
		t.Fatalf("receiver proof verifier error = %v", err)
	}
	if receiver.OPKUsed(opk.ID) {
		t.Fatal("OPK was consumed before proof verification")
	}
	if receiver.AcceptedHandshakeCount() != 0 {
		t.Fatal("rejected message created a session")
	}
}

func TestExpiredBundleRejected(t *testing.T) {
	alice := mustIdentity(t, "alice", "phone")
	bob := mustIdentity(t, "bob", "laptop")
	spk := mustSignedPreKey(t, 4001)
	expiresAt := time.Now().Add(-time.Second)
	receiver, err := netsec.NewReceiver(netsec.ReceiverConfig{
		Identity:     bob,
		SignedPreKey: spk,
		ExpiresAt:    expiresAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := receiver.PreKeyBundle(0)
	if err != nil {
		t.Fatal(err)
	}
	initiator := newInitiator(t, alice, &directoryRecord{target: bob.Public(), spkID: spk.ID, spk: spk.PublicKey().Bytes()})
	if _, err := initiator.Start(bob.Public(), bundle, []byte("x")); !errors.Is(err, netsec.ErrBundleExpired) {
		t.Fatalf("expired bundle error = %v", err)
	}
}

func TestTamperedFirstMessageRejected(t *testing.T) {
	alice := mustIdentity(t, "alice", "phone")
	bob := mustIdentity(t, "bob", "laptop")
	receiver, bundle, record, _ := newReceiver(t, bob, true)
	initiator := newInitiator(t, alice, record)

	handshake, err := initiator.Start(bob.Public(), bundle, []byte("authentic"))
	if err != nil {
		t.Fatal(err)
	}

	badCiphertext := handshake.Message()
	badCiphertext.Ciphertext[0] ^= 0xff
	if _, err := receiver.AcceptInitial(badCiphertext, alice.Public()); !errors.Is(err, netsec.ErrDecrypt) {
		t.Fatalf("ciphertext tamper error = %v", err)
	}

	badTranscript := handshake.Message()
	badTranscript.Transcript[len(badTranscript.Transcript)-1] ^= 0x01
	if _, err := receiver.AcceptInitial(badTranscript, alice.Public()); err == nil {
		t.Fatal("tampered transcript was accepted")
	}

	if _, err := receiver.AcceptInitial(handshake.Message(), alice.Public()); err != nil {
		t.Fatal(err)
	}
	changedAfterAccept := handshake.Message()
	changedAfterAccept.Ciphertext[0] ^= 0x01
	if _, err := receiver.AcceptInitial(changedAfterAccept, alice.Public()); !errors.Is(err, netsec.ErrHandshakeConflict) {
		t.Fatalf("changed retransmission error = %v", err)
	}
}

func TestConcurrentOPKConsumptionIsAtomic(t *testing.T) {
	alice := mustIdentity(t, "alice", "phone")
	bob := mustIdentity(t, "bob", "laptop")
	receiver, bundle, record, _ := newReceiver(t, bob, true)
	initiator := newInitiator(t, alice, record)

	first, err := initiator.Start(bob.Public(), bundle, []byte("one"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := initiator.Start(bob.Public(), bundle, []byte("two"))
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, message := range []*netsec.InitialMessage{first.Message(), second.Message()} {
		wg.Add(1)
		go func(message *netsec.InitialMessage) {
			defer wg.Done()
			<-start
			_, err := receiver.AcceptInitial(message, alice.Public())
			errs <- err
		}(message)
	}
	close(start)
	wg.Wait()
	close(errs)

	successes := 0
	reuses := 0
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, netsec.ErrOneTimePreKeyUsed):
			reuses++
		default:
			t.Fatalf("unexpected concurrent accept error: %v", err)
		}
	}
	if successes != 1 || reuses != 1 {
		t.Fatalf("successes=%d reuses=%d", successes, reuses)
	}
}

func TestInvalidCurveMaterialRejected(t *testing.T) {
	xPrivate, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, signing, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := netsec.NewDeviceIdentityFromKeys("a", "d", signing, xPrivate); err == nil {
		t.Fatal("X25519 identity key was accepted")
	}

	bob := mustIdentity(t, "bob", "laptop")
	_, bundle, _, _ := newReceiver(t, bob, false)
	invalid := bundle.Clone()
	invalid.IdentityKey = nil
	if err := invalid.Verify(time.Now()); !errors.Is(err, netsec.ErrInvalidBundle) {
		t.Fatalf("nil identity point error = %v", err)
	}

	if _, err := netsec.NewReceiver(netsec.ReceiverConfig{
		Identity: bob,
		SignedPreKey: &netsec.SignedPreKey{
			ID:         1,
			PrivateKey: xPrivate,
		},
		ExpiresAt: time.Now().Add(time.Hour),
	}); err == nil {
		t.Fatal("X25519 signed prekey was accepted")
	}
}

func TestTranscriptRejectsInfinityAndOffCurvePoints(t *testing.T) {
	alice := mustIdentity(t, "alice", "phone")
	bob := mustIdentity(t, "bob", "laptop")
	_, bundle, record, _ := newReceiver(t, bob, true)
	initiator := newInitiator(t, alice, record)

	handshake, err := initiator.Start(bob.Public(), bundle, []byte("points"))
	if err != nil {
		t.Fatal(err)
	}
	transcript, err := netsec.ParseTranscript(handshake.Transcript())
	if err != nil {
		t.Fatal(err)
	}

	infinity := *transcript
	infinity.EphemeralKey = []byte{0}
	if err := infinity.Validate(); !errors.Is(err, netsec.ErrInvalidTranscript) {
		t.Fatalf("infinity encoding error = %v", err)
	}

	offCurve := *transcript
	offCurve.EphemeralKey = make([]byte, 65)
	offCurve.EphemeralKey[0] = 4
	if err := offCurve.Validate(); !errors.Is(err, netsec.ErrInvalidTranscript) {
		t.Fatalf("off-curve point error = %v", err)
	}
}

func TestDirectoryProofDoesNotImplyPeerOnline(t *testing.T) {
	alice := mustIdentity(t, "alice", "phone")
	bob := mustIdentity(t, "bob", "laptop")
	receiver, bundle, record, _ := newReceiver(t, bob, true)
	initiator := newInitiator(t, alice, record)

	handshake, err := initiator.Start(bob.Public(), bundle, []byte("offline delivery"))
	if err != nil {
		t.Fatal(err)
	}
	if handshake == nil {
		t.Fatal("no handshake")
	}
	if receiver.AcceptedHandshakeCount() != 0 {
		t.Fatal("directory proof must not accept a message on behalf of the receiver")
	}
	if receiver.OPKUsed(bundle.OneTimePreKeyID) {
		t.Fatal("directory proof must not consume the receiver OPK")
	}
}

func TestCanonicalMessageEncoding(t *testing.T) {
	alice := mustIdentity(t, "alice", "phone")
	bob := mustIdentity(t, "bob", "laptop")
	_, bundle, record, _ := newReceiver(t, bob, true)
	initiator := newInitiator(t, alice, record)

	handshake, err := initiator.Start(bob.Public(), bundle, []byte("canonical"))
	if err != nil {
		t.Fatal(err)
	}
	wire, err := handshake.MessageBytes()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := netsec.ParseInitialMessage(wire)
	if err != nil {
		t.Fatal(err)
	}
	rewritten, err := parsed.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(wire, rewritten) {
		t.Fatal("message encoding is not canonical")
	}
	withTrailing := append(append([]byte(nil), wire...), 0)
	if _, err := netsec.ParseInitialMessage(withTrailing); err == nil {
		t.Fatal("trailing bytes were accepted")
	}
}
