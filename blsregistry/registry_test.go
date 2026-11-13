package blsregistry

import (
	"errors"
	"math/big"
	"testing"
)

// registerHonest builds a key pair and registers it with a valid proof of
// possession.
func registerHonest(t *testing.T, r *Registry) mockKeyPair {
	t.Helper()
	kp := newMockKeyPair()
	proof := SignProofOfPossession(r.engine, kp.secret, kp.public)
	if err := r.Register(kp.public, proof); err != nil {
		t.Fatalf("register honest key: %v", err)
	}
	return kp
}

func TestDomainTagsAreDistinct(t *testing.T) {
	if DomainSignature == DomainProofOfPossession {
		t.Fatal("proof-of-possession tag must differ from the signature tag")
	}
}

func TestProofOfPossessionRegistrationAndDuplicate(t *testing.T) {
	engine := mockEngine{}
	registry, err := NewRegistry(engine, 1)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}

	kp := newMockKeyPair()

	// Missing proof of possession is rejected.
	if err := registry.Register(kp.public, nil); !errors.Is(err, ErrNilProof) {
		t.Fatalf("nil proof: got %v, want ErrNilProof", err)
	}

	// A bogus proof (identity point) is rejected.
	bogusProof := mockG2{new(big.Int)}
	if err := registry.Register(kp.public, bogusProof); !errors.Is(err, ErrInvalidProofOfPossession) {
		t.Fatalf("identity proof: got %v, want ErrInvalidProofOfPossession", err)
	}

	// A proof signed in the ordinary signature domain is rejected because
	// the two domain separation tags must not be interchanged.
	wrongDomainProof := Sign(engine, kp.secret, kp.public.Encode())
	if err := registry.Register(kp.public, wrongDomainProof); !errors.Is(err, ErrInvalidProofOfPossession) {
		t.Fatalf("same-tag proof: got %v, want ErrInvalidProofOfPossession", err)
	}

	// A proof over unrelated bytes in the PoP domain is also rejected.
	forged := engine.HashToG2(DomainProofOfPossession, []byte("not-the-key-encoding")).Mul(kp.secret)
	if err := registry.Register(kp.public, forged); !errors.Is(err, ErrInvalidProofOfPossession) {
		t.Fatalf("forged proof: got %v, want ErrInvalidProofOfPossession", err)
	}

	// The valid proof of possession is accepted.
	validProof := SignProofOfPossession(engine, kp.secret, kp.public)
	if err := registry.Register(kp.public, validProof); err != nil {
		t.Fatalf("valid registration: %v", err)
	}
	if registry.Size() != 1 {
		t.Fatalf("size: got %d, want 1", registry.Size())
	}

	// Re-registering the same public key is ignored.
	if err := registry.Register(kp.public, validProof); err != nil {
		t.Fatalf("duplicate registration: %v", err)
	}
	if registry.Size() != 1 {
		t.Fatalf("duplicate changed size: got %d, want 1", registry.Size())
	}

	// Nil and identity public keys are rejected.
	if err := registry.Register(nil, validProof); !errors.Is(err, ErrNilPublicKey) {
		t.Fatalf("nil key: got %v, want ErrNilPublicKey", err)
	}
	identityKey := mockG1{new(big.Int)}
	identityProof := engine.HashToG2(DomainProofOfPossession, identityKey.Encode()).Mul(kp.secret)
	if err := registry.Register(identityKey, identityProof); !errors.Is(err, ErrIdentityPublicKey) {
		t.Fatalf("identity key: got %v, want ErrIdentityPublicKey", err)
	}
}

func TestRogueKeyWithoutProofOfPossessionIsBlocked(t *testing.T) {
	engine := mockEngine{}
	registry, err := NewRegistry(engine, 2)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}

	honest := registerHonest(t, registry)

	// Classic rogue-key construction: the attacker cannot derive a secret for
	// the honest key, so it picks a coefficient a and registers the malicious
	// public key PK_rogue = a*G1 - PK_honest, whose discrete log relative to
	// G1 is unknown. Without proof-of-possession enforcement this would let
	// the attacker forge signatures attributed to both members.
	coefficient := mockScalar{big.NewInt(4242)}
	rogueKey := engine.G1Generator().Mul(coefficient).Add(honest.public.Neg()).(mockG1)

	// No proof available: the attacker cannot sign over its own key encoding.
	if err := registry.Register(rogueKey, nil); !errors.Is(err, ErrNilProof) {
		t.Fatalf("rogue key without proof: got %v, want ErrNilProof", err)
	}

	// Any fabricated proof fails the pairing check because the discrete log of
	// the rogue key is unknown.
	fakeProof := engine.HashToG2(DomainProofOfPossession, rogueKey.Encode()).Mul(coefficient)
	if err := registry.Register(rogueKey, fakeProof); !errors.Is(err, ErrInvalidProofOfPossession) {
		t.Fatalf("rogue key with fabricated proof: got %v, want ErrInvalidProofOfPossession", err)
	}

	// Demonstrate the underlying forgery that proof-of-possession neutralizes:
	// with a bare check over the summed public key, the attacker alone passes
	// the same-message aggregate equation for itself plus the honest key.
	message := []byte("committee-decision-42")
	hashed := engine.HashToG2(DomainSignature, message)
	forgedAggregateSignature := hashed.Mul(coefficient)
	summedKey := honest.public.Add(rogueKey)
	if !engine.CheckPairing(
		[]G1Point{summedKey, engine.G1Generator()},
		[]G2Point{hashed, forgedAggregateSignature.Neg()},
	) {
		t.Fatal("sanity check: rogue-key forgery should verify against a bare aggregate key")
	}

	// The malicious key never entered the validator set.
	if registry.Size() != 1 {
		t.Fatalf("registry size: got %d, want 1", registry.Size())
	}

	// The honest quorum cannot be met, so no forged aggregate can be accepted
	// through the registry.
	bitmap := []bool{true}
	if err := registry.VerifyAggregateSameMessage(message, bitmap, forgedAggregateSignature); !errors.Is(err, ErrQuorumNotReached) {
		t.Fatalf("forged aggregate via registry: got %v, want ErrQuorumNotReached", err)
	}
}

func TestSameMessageAggregateVerification(t *testing.T) {
	engine := mockEngine{}
	const quorum = 3
	registry, err := NewRegistry(engine, quorum)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	keyPairs := make([]mockKeyPair, 0, 4)
	for range 4 {
		keyPairs = append(keyPairs, registerHonest(t, registry))
	}

	message := []byte("block-100-vote")
	bitmap := []bool{true, true, true, false}

	// Honest aggregate of the three selected signatures.
	var aggregate G2Point
	for i, on := range bitmap {
		if !on {
			continue
		}
		signature := Sign(engine, keyPairs[i].secret, message)
		if aggregate == nil {
			aggregate = signature
		} else {
			aggregate = aggregate.Add(signature)
		}
	}
	if err := registry.VerifyAggregateSameMessage(message, bitmap, aggregate); err != nil {
		t.Fatalf("valid same-message aggregate: %v", err)
	}

	// Bitmap length must equal the set size.
	shortBitmap := []bool{true, true, true}
	if err := registry.VerifyAggregateSameMessage(message, shortBitmap, aggregate); !errors.Is(err, ErrBitmapLength) {
		t.Fatalf("short bitmap: got %v, want ErrBitmapLength", err)
	}
	longBitmap := []bool{true, true, true, false, false}
	if err := registry.VerifyAggregateSameMessage(message, longBitmap, aggregate); !errors.Is(err, ErrBitmapLength) {
		t.Fatalf("long bitmap: got %v, want ErrBitmapLength", err)
	}

	// Selected count below threshold is rejected.
	belowQuorum := []bool{true, true, false, false}
	if err := registry.VerifyAggregateSameMessage(message, belowQuorum, aggregate); !errors.Is(err, ErrQuorumNotReached) {
		t.Fatalf("below quorum: got %v, want ErrQuorumNotReached", err)
	}

	// Empty bitmap is rejected.
	emptyBitmap := []bool{false, false, false, false}
	if err := registry.VerifyAggregateSameMessage(message, emptyBitmap, aggregate); !errors.Is(err, ErrNoSelectedValidators) {
		t.Fatalf("empty bitmap: got %v, want ErrNoSelectedValidators", err)
	}

	// Aggregate public key only covers selected keys: a signature including
	// the unselected fourth validator must fail.
	fullBitmap := []bool{true, true, true, true}
	if err := registry.VerifyAggregateSameMessage(message, fullBitmap, aggregate); !errors.Is(err, ErrPairingMismatch) {
		t.Fatalf("unselected key contribution: got %v, want ErrPairingMismatch", err)
	}

	// Wrong message fails.
	if err := registry.VerifyAggregateSameMessage([]byte("other-message"), bitmap, aggregate); !errors.Is(err, ErrPairingMismatch) {
		t.Fatalf("wrong message: got %v, want ErrPairingMismatch", err)
	}

	// Identity aggregate signature is rejected.
	identitySignature := mockG2{new(big.Int)}
	if err := registry.VerifyAggregateSameMessage(message, bitmap, identitySignature); !errors.Is(err, ErrPairingMismatch) {
		t.Fatalf("identity signature: got %v, want ErrPairingMismatch", err)
	}

	// Nil aggregate signature is rejected.
	if err := registry.VerifyAggregateSameMessage(message, bitmap, nil); !errors.Is(err, ErrNilSignature) {
		t.Fatalf("nil signature: got %v, want ErrNilSignature", err)
	}
}

func TestDistinctMessageAggregateVerification(t *testing.T) {
	engine := mockEngine{}
	const quorum = 2
	registry, err := NewRegistry(engine, quorum)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	keyPairs := make([]mockKeyPair, 0, 3)
	for range 3 {
		keyPairs = append(keyPairs, registerHonest(t, registry))
	}

	bitmap := []bool{true, true, false}
	messages := [][]byte{[]byte("msg-a"), []byte("msg-b")}

	// Each selected validator signs its own message; aggregate the signatures.
	var aggregate G2Point
	for i, message := range messages {
		signature := Sign(engine, keyPairs[i].secret, message)
		if aggregate == nil {
			aggregate = signature
		} else {
			aggregate = aggregate.Add(signature)
		}
	}
	if err := registry.VerifyAggregateDistinctMessages(messages, bitmap, aggregate); err != nil {
		t.Fatalf("valid distinct-message aggregate: %v", err)
	}

	// Duplicate messages are rejected.
	duplicates := [][]byte{[]byte("msg-a"), []byte("msg-a")}
	if err := registry.VerifyAggregateDistinctMessages(duplicates, bitmap, aggregate); !errors.Is(err, ErrDuplicateMessage) {
		t.Fatalf("duplicate messages: got %v, want ErrDuplicateMessage", err)
	}

	// Message count must match the selected count.
	tooFew := [][]byte{[]byte("msg-a")}
	if err := registry.VerifyAggregateDistinctMessages(tooFew, bitmap, aggregate); !errors.Is(err, ErrDistinctMessageCount) {
		t.Fatalf("too few messages: got %v, want ErrDistinctMessageCount", err)
	}

	// Bitmap length mismatch is rejected.
	badBitmap := []bool{true, true}
	if err := registry.VerifyAggregateDistinctMessages(messages, badBitmap, aggregate); !errors.Is(err, ErrBitmapLength) {
		t.Fatalf("bad bitmap: got %v, want ErrBitmapLength", err)
	}

	// Below quorum is rejected.
	belowQuorum := []bool{true, false, false}
	if err := registry.VerifyAggregateDistinctMessages([][]byte{[]byte("only-one")}, belowQuorum, aggregate); !errors.Is(err, ErrQuorumNotReached) {
		t.Fatalf("below quorum: got %v, want ErrQuorumNotReached", err)
	}

	// A permuted message-to-key assignment fails the pairing equation.
	swapped := [][]byte{[]byte("msg-b"), []byte("msg-a")}
	if err := registry.VerifyAggregateDistinctMessages(swapped, bitmap, aggregate); !errors.Is(err, ErrPairingMismatch) {
		t.Fatalf("swapped messages: got %v, want ErrPairingMismatch", err)
	}

	// Identity aggregate signature is rejected.
	identitySignature := mockG2{new(big.Int)}
	if err := registry.VerifyAggregateDistinctMessages(messages, bitmap, identitySignature); !errors.Is(err, ErrPairingMismatch) {
		t.Fatalf("identity signature: got %v, want ErrPairingMismatch", err)
	}
}

func TestNewRegistryValidation(t *testing.T) {
	if _, err := NewRegistry(nil, 1); err == nil {
		t.Fatal("nil engine should be rejected")
	}
	if _, err := NewRegistry(mockEngine{}, 0); err == nil {
		t.Fatal("zero quorum should be rejected")
	}
}
