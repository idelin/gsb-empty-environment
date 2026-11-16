package netsec

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"math/big"
	"sync"
	"testing"
)

// The tests use the exponents of a cyclic group as a stand-in for curve
// points.  Pair(a*g, b*g) is represented by g^(a*b), which gives the same
// algebraic identities as a real bilinear pairing.
var mockOrder = func() *big.Int {
	order := new(big.Int).Lsh(big.NewInt(1), 255)
	return order.Sub(order, big.NewInt(19))
}()

type mockPoint struct {
	x *big.Int
}

func pointOf(x *big.Int) *mockPoint {
	return &mockPoint{x: new(big.Int).Mod(x, mockOrder)}
}

func scalarOf(v int64) *big.Int {
	return new(big.Int).Mod(big.NewInt(v), mockOrder)
}

func (p *mockPoint) Add(q Point) Point {
	other := q.(*mockPoint)
	return pointOf(new(big.Int).Add(p.x, other.x))
}

func (p *mockPoint) Equal(q Point) bool {
	other, ok := q.(*mockPoint)
	return ok && p.x.Cmp(other.x) == 0
}

func (p *mockPoint) IsInfinity() bool {
	return p.x.Sign() == 0
}

func (p *mockPoint) Bytes() []byte {
	return p.x.FillBytes(make([]byte, 32))
}

func (p *mockPoint) scalarMul(scalar *big.Int) *mockPoint {
	return pointOf(new(big.Int).Mul(p.x, scalar))
}

type mockPairingValue struct {
	exponent *big.Int
}

func (v *mockPairingValue) Mul(other PairingValue) PairingValue {
	o := other.(*mockPairingValue)
	return &mockPairingValue{exponent: new(big.Int).Mod(new(big.Int).Add(v.exponent, o.exponent), mockOrder)}
}

func (v *mockPairingValue) Equal(other PairingValue) bool {
	o, ok := other.(*mockPairingValue)
	return ok && v.exponent.Cmp(o.exponent) == 0
}

type hashCall struct {
	message string
	domain  string
}

type mockEngine struct {
	mu    sync.Mutex
	calls []hashCall
}

func (e *mockEngine) Generator() Point {
	return pointOf(big.NewInt(1))
}

func (e *mockEngine) Infinity() Point {
	return pointOf(big.NewInt(0))
}

func (e *mockEngine) HashToCurve(message, domain []byte) (Point, error) {
	e.mu.Lock()
	e.calls = append(e.calls, hashCall{message: string(message), domain: string(domain)})
	e.mu.Unlock()

	h := sha256.New()
	h.Write(domain)
	h.Write([]byte{0})
	h.Write(message)
	x := new(big.Int).SetBytes(h.Sum(nil))
	x.Mod(x, mockOrder)
	if x.Sign() == 0 {
		x.SetInt64(1)
	}
	return pointOf(x), nil
}

func (e *mockEngine) Pair(a, b Point) PairingValue {
	left := a.(*mockPoint)
	right := b.(*mockPoint)
	return &mockPairingValue{exponent: new(big.Int).Mod(new(big.Int).Mul(left.x, right.x), mockOrder)}
}

func (e *mockEngine) hashed(message, domain []byte) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, call := range e.calls {
		if call.message == string(message) && call.domain == string(domain) {
			return true
		}
	}
	return false
}

type testSigner struct {
	secret *big.Int
	public *mockPoint
}

func newTestSigner(secret int64) testSigner {
	return newTestSignerFromSecret(scalarOf(secret))
}

func newTestSignerFromSecret(sk *big.Int) testSigner {
	return testSigner{secret: sk, public: pointOf(big.NewInt(1)).scalarMul(sk)}
}

func (s testSigner) proof(engine PairingEngine, domains Domains) Point {
	hash, err := engine.HashToCurve(s.public.Bytes(), domains.ProofOfPossession)
	if err != nil {
		panic(err)
	}
	return hash.(*mockPoint).scalarMul(s.secret)
}

func (s testSigner) sign(engine PairingEngine, domains Domains, message []byte) Point {
	hash, err := engine.HashToCurve(message, domains.Signature)
	if err != nil {
		panic(err)
	}
	return hash.(*mockPoint).scalarMul(s.secret)
}

func register(t *testing.T, set *VerifierSet, engine PairingEngine, domains Domains, signers ...testSigner) {
	t.Helper()
	for _, signer := range signers {
		if err := set.Register(signer.public, signer.proof(engine, domains)); err != nil {
			t.Fatalf("register: %v", err)
		}
	}
}

func sumPoints(points ...Point) Point {
	sum := Point(pointOf(big.NewInt(0)))
	for _, point := range points {
		sum = sum.Add(point)
	}
	return sum
}

func newTestSet(t *testing.T) (*mockEngine, Domains, *VerifierSet) {
	t.Helper()
	engine := &mockEngine{}
	domains := DefaultDomains()
	set, err := NewVerifierSet(engine, domains)
	if err != nil {
		t.Fatalf("NewVerifierSet: %v", err)
	}
	return engine, domains, set
}

func TestRegisterRejectsPublicKeyWithoutProofOfPossession(t *testing.T) {
	engine, domains, set := newTestSet(t)
	honest := newTestSigner(11)
	attacker := newTestSigner(29)

	// A classic rogue-key public key: honest + rogue = attacker, so a
	// signature made only with attacker's secret looks like an aggregate from
	// both signers.  The attacker does not know the discrete log of rogue.
	rogue := pointOf(new(big.Int).Sub(attacker.secret, honest.secret))

	register(t, set, engine, domains, honest)

	if err := set.Register(rogue, nil); !errors.Is(err, ErrInvalidProofOfPossession) {
		t.Fatalf("Register without proof error = %v, want %v", err, ErrInvalidProofOfPossession)
	}
	if err := set.Register(rogue, pointOf(big.NewInt(0))); !errors.Is(err, ErrInvalidProofOfPossession) {
		t.Fatalf("Register with infinity proof error = %v, want %v", err, ErrInvalidProofOfPossession)
	}

	// Even a signature made by the attacker over the rogue key's bytes does not
	// prove possession of the rogue key's secret.
	hash, _ := engine.HashToCurve(rogue.Bytes(), domains.ProofOfPossession)
	wrongProof := hash.(*mockPoint).scalarMul(attacker.secret)
	if err := set.Register(rogue, wrongProof); !errors.Is(err, ErrInvalidProofOfPossession) {
		t.Fatalf("Register with attacker proof error = %v, want %v", err, ErrInvalidProofOfPossession)
	}

	if set.Len() != 1 || set.Contains(rogue) {
		t.Fatalf("rogue key entered verifier set: len=%d contains=%v", set.Len(), set.Contains(rogue))
	}

	// The rogue aggregate signature is not accepted against the only actual
	// registered key.
	message := []byte("agree")
	aggSignature := attacker.sign(engine, domains, message)
	if err := set.VerifySameMessage([]byte{1}, message, aggSignature, 1); !errors.Is(err, ErrInvalidAggregateSignature) {
		t.Fatalf("rogue aggregate signature error = %v, want %v", err, ErrInvalidAggregateSignature)
	}
}

func TestDomainsMustBeDistinct(t *testing.T) {
	engine := &mockEngine{}
	same := []byte("same-domain")
	_, err := NewVerifierSet(engine, Domains{Signature: same, ProofOfPossession: same})
	if !errors.Is(err, ErrDomainCollision) {
		t.Fatalf("NewVerifierSet error = %v, want %v", err, ErrDomainCollision)
	}

	if _, err := NewVerifierSet(engine, Domains{Signature: nil, ProofOfPossession: []byte("pop")}); !errors.Is(err, ErrEmptyDomain) {
		t.Fatalf("empty domain error = %v, want %v", err, ErrEmptyDomain)
	}

	defaults := DefaultDomains()
	if bytes.Equal(defaults.Signature, defaults.ProofOfPossession) {
		t.Fatal("default signature and proof-of-possession domains are equal")
	}
}

func TestProofOfPossessionUsesPublicKeyBytesAndSeparateDomain(t *testing.T) {
	engine, domains, set := newTestSet(t)
	signer := newTestSigner(7)

	register(t, set, engine, domains, signer)
	if !engine.hashed(signer.public.Bytes(), domains.ProofOfPossession) {
		t.Fatal("proof of possession did not hash the public key bytes under the PoP domain")
	}
	if engine.hashed(signer.public.Bytes(), domains.Signature) {
		t.Fatal("proof of possession was hashed with the signature domain")
	}

	message := []byte("proposal-1")
	signature := signer.sign(engine, domains, message)
	if err := set.VerifySameMessage([]byte{1}, message, signature, 1); err != nil {
		t.Fatalf("VerifySameMessage: %v", err)
	}
	if !engine.hashed(message, domains.Signature) {
		t.Fatal("ordinary signature did not use the signature domain")
	}
	if engine.hashed(message, domains.ProofOfPossession) {
		t.Fatal("ordinary signature used the proof-of-possession domain")
	}
}

func TestSameMessageAggregateValidation(t *testing.T) {
	engine, domains, set := newTestSet(t)
	signers := []testSigner{newTestSigner(3), newTestSigner(5), newTestSigner(9)}
	register(t, set, engine, domains, signers...)

	// Duplicate registration is ignored.
	if err := set.Register(signers[0].public, signers[0].proof(engine, domains)); err != nil {
		t.Fatalf("duplicate Register: %v", err)
	}
	if err := set.Register(signers[0].public, nil); err != nil {
		t.Fatalf("duplicate Register with no proof should be ignored: %v", err)
	}
	if set.Len() != 3 {
		t.Fatalf("Len after duplicate registration = %d, want 3", set.Len())
	}

	message := []byte("block-42")

	if err := set.VerifySameMessage([]byte{1, 1}, message, signers[0].sign(engine, domains, message), 2); !errors.Is(err, ErrBitmapLengthMismatch) {
		t.Fatalf("short bitmap error = %v, want %v", err, ErrBitmapLengthMismatch)
	}
	if err := set.VerifySameMessage([]byte{1, 0, 0, 0}, message, signers[0].sign(engine, domains, message), 2); !errors.Is(err, ErrBitmapLengthMismatch) {
		t.Fatalf("long bitmap error = %v, want %v", err, ErrBitmapLengthMismatch)
	}
	if err := set.VerifySameMessage([]byte{1, 1, 1}, message, signers[0].sign(engine, domains, message), 2); !errors.Is(err, ErrInvalidAggregateSignature) {
		// One signature cannot cover three selected keys.
		t.Fatalf("one-signature aggregate error = %v", err)
	}
	if err := set.VerifySameMessage([]byte{1, 0, 0}, message, signers[0].sign(engine, domains, message), 2); !errors.Is(err, ErrThresholdNotMet) {
		t.Fatalf("insufficient participation error = %v, want %v", err, ErrThresholdNotMet)
	}
	if err := set.VerifySameMessage([]byte{0, 0, 0}, message, signers[0].sign(engine, domains, message), 1); !errors.Is(err, ErrThresholdNotMet) {
		t.Fatalf("empty bitmap error = %v, want %v", err, ErrThresholdNotMet)
	}
	if err := set.VerifySameMessage([]byte{1, 1, 0}, message, signers[0].sign(engine, domains, message), 0); !errors.Is(err, ErrInvalidThreshold) {
		t.Fatalf("zero threshold error = %v, want %v", err, ErrInvalidThreshold)
	}

	selectedSig := sumPoints(
		signers[0].sign(engine, domains, message),
		signers[2].sign(engine, domains, message),
	)
	if err := set.VerifySameMessage([]byte{1, 0, 1}, message, selectedSig, 2); err != nil {
		t.Fatalf("valid aggregate: %v", err)
	}

	aggregate, count, err := set.AggregatePublicKey([]byte{1, 0, 1}, 2)
	if err != nil {
		t.Fatalf("AggregatePublicKey: %v", err)
	}
	if count != 2 || !aggregate.Equal(sumPoints(signers[0].public, signers[2].public)) {
		t.Fatal("aggregate public key does not consist solely of selected registered keys")
	}

	// A signature from an unselected signer must not verify.
	if err := set.VerifySameMessage([]byte{1, 0, 1}, message, signers[1].sign(engine, domains, message), 2); !errors.Is(err, ErrInvalidAggregateSignature) {
		t.Fatalf("unselected signer error = %v, want %v", err, ErrInvalidAggregateSignature)
	}
}

func TestAggregatePublicKeyAtInfinityIsRejected(t *testing.T) {
	engine, domains, set := newTestSet(t)
	positive := newTestSigner(5)
	negative := newTestSignerFromSecret(new(big.Int).Sub(mockOrder, positive.secret))
	register(t, set, engine, domains, positive, negative)

	message := []byte("block")
	if _, _, err := set.AggregatePublicKey([]byte{1, 1}, 2); !errors.Is(err, ErrInfinityAggregatePublicKey) {
		t.Fatalf("AggregatePublicKey error = %v, want %v", err, ErrInfinityAggregatePublicKey)
	}
	if err := set.VerifySameMessage([]byte{1, 1}, message, positive.sign(engine, domains, message), 2); !errors.Is(err, ErrInfinityAggregatePublicKey) {
		t.Fatalf("VerifySameMessage error = %v, want %v", err, ErrInfinityAggregatePublicKey)
	}
}

func TestInfinityPublicKeysAreRejected(t *testing.T) {
	engine, domains, set := newTestSet(t)
	infinity := pointOf(big.NewInt(0))

	if err := set.Register(infinity, infinity); !errors.Is(err, ErrInfinityPublicKey) {
		t.Fatalf("Register infinity error = %v, want %v", err, ErrInfinityPublicKey)
	}
	if err := set.VerifyProofOfPossession(infinity, infinity); !errors.Is(err, ErrInfinityPublicKey) {
		t.Fatalf("VerifyProofOfPossession infinity error = %v, want %v", err, ErrInfinityPublicKey)
	}

	signer := newTestSigner(13)
	register(t, set, engine, domains, signer)
	message := []byte("block")
	if err := set.VerifySameMessage([]byte{1}, message, infinity, 1); !errors.Is(err, ErrInfinitySignature) {
		t.Fatalf("infinity signature error = %v, want %v", err, ErrInfinitySignature)
	}
}

func TestDistinctMessagesAggregate(t *testing.T) {
	engine, domains, set := newTestSet(t)
	signers := []testSigner{newTestSigner(17), newTestSigner(19), newTestSigner(23)}
	register(t, set, engine, domains, signers...)

	messageA := []byte("proposal-A")
	messageB := []byte("proposal-B")
	bitmap := []byte{1, 1, 0}
	signature := sumPoints(
		signers[0].sign(engine, domains, messageA),
		signers[1].sign(engine, domains, messageB),
	)

	if err := set.VerifyDistinctMessages(bitmap, [][]byte{messageA, messageB}, signature, 2); err != nil {
		t.Fatalf("valid multi-message aggregate: %v", err)
	}
	if err := set.VerifyAggregateDifferentMessages(bitmap, [][]byte{messageA, messageB}, signature, 2); err != nil {
		t.Fatalf("alias valid multi-message aggregate: %v", err)
	}

	if err := set.VerifyDistinctMessages([]byte{1, 1}, [][]byte{messageA, messageB}, signature, 2); !errors.Is(err, ErrBitmapLengthMismatch) {
		t.Fatalf("bitmap mismatch error = %v, want %v", err, ErrBitmapLengthMismatch)
	}
	if err := set.VerifyDistinctMessages(bitmap, [][]byte{messageA}, signature, 2); !errors.Is(err, ErrMessageCountMismatch) {
		t.Fatalf("message count error = %v, want %v", err, ErrMessageCountMismatch)
	}
	if err := set.VerifyDistinctMessages([]byte{1, 0, 0}, [][]byte{messageA}, signature, 2); !errors.Is(err, ErrThresholdNotMet) {
		t.Fatalf("threshold error = %v, want %v", err, ErrThresholdNotMet)
	}

	duplicateSignature := sumPoints(
		signers[0].sign(engine, domains, messageA),
		signers[1].sign(engine, domains, messageA),
	)
	if err := set.VerifyDistinctMessages(bitmap, [][]byte{messageA, messageA}, duplicateSignature, 2); !errors.Is(err, ErrDuplicateMessage) {
		t.Fatalf("duplicate message error = %v, want %v", err, ErrDuplicateMessage)
	}
	// nil and an empty slice are the same message for duplicate detection.
	emptySignature := sumPoints(
		signers[0].sign(engine, domains, nil),
		signers[1].sign(engine, domains, []byte{}),
	)
	if err := set.VerifyDistinctMessages(bitmap, [][]byte{nil, []byte{}}, emptySignature, 2); !errors.Is(err, ErrDuplicateMessage) {
		t.Fatalf("nil/empty duplicate message error = %v, want %v", err, ErrDuplicateMessage)
	}

	duplicateEntries := []SignerMessage{
		{Signer: 0, Message: messageA},
		{Signer: 1, Message: messageA},
	}
	if err := set.VerifyDistinctSignerMessages(duplicateEntries, duplicateSignature, 2); !errors.Is(err, ErrDuplicateMessage) {
		t.Fatalf("explicit duplicate message error = %v, want %v", err, ErrDuplicateMessage)
	}
	duplicateSigners := []SignerMessage{
		{Signer: 0, Message: messageA},
		{Signer: 0, Message: messageB},
	}
	if err := set.VerifyDistinctSignerMessages(duplicateSigners, signature, 2); !errors.Is(err, ErrDuplicateSigner) {
		t.Fatalf("duplicate signer error = %v, want %v", err, ErrDuplicateSigner)
	}
	validEntries := []SignerMessage{
		{Signer: 0, Message: messageA},
		{Signer: 1, Message: messageB},
	}
	if err := set.VerifyDistinctSignerMessages(validEntries, signature, 2); err != nil {
		t.Fatalf("valid explicit multi-message aggregate: %v", err)
	}
}
