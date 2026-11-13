package netsec

import (
	"crypto/rand"
	"errors"
	"testing"
)

func TestMaliciousProviderWrongCRejected(t *testing.T) {
	f := mustField(t, 17)
	honest, err := NewHonestProvider(17, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	malicious := NewMaliciousProvider(honest, CorruptC(f, 0, 3))

	candidate := mustHonestTriple(t, malicious, "bad-candidate")
	check := mustHonestTriple(t, honest, "honest-check")
	h, err := NewHost(17)
	if err != nil {
		t.Fatal(err)
	}
	res, err := h.Sacrifice(candidate, check)
	if err != nil {
		t.Fatalf("Sacrifice: %v", err)
	}
	if res.Accepted || res.Tau == 0 {
		t.Fatalf("malicious provider was accepted: %+v", res)
	}
	a, _ := f.Recombine(candidate.A)
	b, _ := f.Recombine(candidate.B)
	c, _ := f.Recombine(candidate.C)
	if c == f.Mul(a, b) {
		t.Fatal("fixture did not actually violate c=a*b")
	}
	if status, _ := h.TripleStatus(candidate.ID); status != StatusBurned {
		t.Fatalf("candidate status=%q, want burned", status)
	}
}

func TestMaliciousProviderCheckTripleRejected(t *testing.T) {
	f := mustField(t, 17)
	honest, err := NewHonestProvider(17, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	malicious := NewMaliciousProvider(honest, CorruptShare(f, 'c', 2, 7))
	candidate := mustHonestTriple(t, honest, "honest-candidate")
	check := mustHonestTriple(t, malicious, "bad-check")
	h, err := NewHost(17)
	if err != nil {
		t.Fatal(err)
	}
	res, err := h.Sacrifice(candidate, check)
	if err != nil {
		t.Fatalf("Sacrifice: %v", err)
	}
	if res.Accepted {
		t.Fatalf("corrupt check triple accepted: %+v", res)
	}
}

func TestTamperedTripleShareRejected(t *testing.T) {
	const p = uint32(17)
	f := mustField(t, p)
	// Candidate scalar relation: a=3, b=4, c=12. Changing one A share to
	// a=4 makes c != a*b deterministically.
	candidate := mustTriple(t, f, "candidate", 3, 4, 12)
	candidate.A[0] = f.Add(candidate.A[0], 1)
	check := mustTriple(t, f, "check", 5, 6, f.Mul(5, 6))
	h, err := NewHost(p)
	if err != nil {
		t.Fatal(err)
	}
	res, err := h.Sacrifice(candidate, check)
	if err != nil {
		t.Fatalf("Sacrifice: %v", err)
	}
	if res.Accepted {
		t.Fatal("tampered A share was accepted")
	}
	if status, _ := h.TripleStatus(candidate.ID); status != StatusBurned {
		t.Fatalf("status=%q, want burned", status)
	}
}

func TestHonestProviderFixture(t *testing.T) {
	for _, p := range []uint32{2, 17, 65521} {
		f := mustField(t, p)
		provider, err := NewHonestProvider(p, rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 64; i++ {
			tr := mustHonestTriple(t, provider, "t")
			a, _ := f.Recombine(tr.A)
			b, _ := f.Recombine(tr.B)
			c, _ := f.Recombine(tr.C)
			if c != f.Mul(a, b) {
				t.Fatalf("p=%d honest triple violates c=a*b", p)
			}
		}
	}
}

func TestTamperedSignatureProducesNoResult(t *testing.T) {
	const p = uint32(17)
	keys := mustKeys(t)
	bus := newTestBus()
	bus.setHook(LabelSigma, 2, func(m SignedMessage) []SignedMessage {
		m.Sig = append([]byte(nil), m.Sig...)
		m.Sig[0] ^= 0xff
		return []SignedMessage{m}
	})
	h := newTestHost(t, p, NewMemoryStore(), bus, keys)
	provider, _ := NewHonestProvider(p, rand.Reader)
	candidate := mustHonestTriple(t, provider, "candidate")
	check := mustHonestTriple(t, provider, "check")

	res, err := h.Sacrifice(candidate, check)
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("Sacrifice error=%v, want ErrBadSignature", err)
	}
	if res.Accepted {
		t.Fatal("accepted despite bad signature")
	}
	for _, id := range []string{candidate.ID, check.ID} {
		if status, _ := h.TripleStatus(id); status != StatusBurned {
			t.Fatalf("%s status=%q, want burned", id, status)
		}
	}
	ev := h.Evidence()
	if len(ev) == 0 || ev[0].Kind != EvidenceBadSignature {
		t.Fatalf("evidence=%+v, want bad signature", ev)
	}
	if len(ev[0].Messages) != 1 || len(ev[0].Messages[0].Sig) == 0 {
		t.Fatal("evidence does not retain the signed message")
	}
}

func TestOutOfRangeOpeningProducesNoResult(t *testing.T) {
	const p = uint32(17)
	f := mustField(t, p)
	keys := mustKeys(t)
	bus := newTestBus()
	bus.setHook(LabelRho, 0, func(m SignedMessage) []SignedMessage {
		return []SignedMessage{mustResign(t, keys[0], f, m, shareBytes(p))}
	})
	h := newTestHost(t, p, NewMemoryStore(), bus, keys)
	provider, _ := NewHonestProvider(p, rand.Reader)
	candidate := mustHonestTriple(t, provider, "candidate")
	check := mustHonestTriple(t, provider, "check")
	res, err := h.Sacrifice(candidate, check)
	if !errors.Is(err, ErrRange) {
		t.Fatalf("Sacrifice error=%v, want ErrRange", err)
	}
	if res.Accepted {
		t.Fatal("accepted out-of-range opening")
	}
	ev := h.Evidence()
	if len(ev) == 0 || ev[0].Kind != EvidenceMalformed || ev[0].Sender != 0 {
		t.Fatalf("evidence=%+v, want malformed opening from sender 0", ev)
	}
}

func TestValidlySignedWrongOpeningIsEvidence(t *testing.T) {
	const p = uint32(17)
	f := mustField(t, p)
	keys := mustKeys(t)
	bus := newTestBus()
	bus.setHook(LabelSigma, 1, func(m SignedMessage) []SignedMessage {
		alt := mustResign(t, keys[1], f, m, shareBytes(f.Add(bytesShare(m.Value), 1)))
		return []SignedMessage{alt}
	})
	h := newTestHost(t, p, NewMemoryStore(), bus, keys)
	provider, _ := NewHonestProvider(p, rand.Reader)
	candidate := mustHonestTriple(t, provider, "candidate")
	check := mustHonestTriple(t, provider, "check")
	res, err := h.Sacrifice(candidate, check)
	if !errors.Is(err, ErrOpeningMismatch) {
		t.Fatalf("Sacrifice error=%v, want ErrOpeningMismatch", err)
	}
	if res.Accepted {
		t.Fatal("accepted a signed but incorrect opening share")
	}
	ev := h.Evidence()
	if len(ev) == 0 || ev[0].Kind != EvidenceOpeningMismatch || ev[0].Sender != 1 {
		t.Fatalf("evidence=%+v, want opening mismatch from sender 1", ev)
	}
}

func TestBroadcastForkDuringSacrifice(t *testing.T) {
	const p = uint32(17)
	f := mustField(t, p)
	keys := mustKeys(t)
	bus := newTestBus()
	bus.setHook(LabelTau, 1, func(m SignedMessage) []SignedMessage {
		alt := mustResign(t, keys[1], f, m, shareBytes(f.Add(bytesShare(m.Value), 1)))
		return []SignedMessage{m, alt}
	})
	h := newTestHost(t, p, NewMemoryStore(), bus, keys)
	provider, _ := NewHonestProvider(p, rand.Reader)
	candidate := mustHonestTriple(t, provider, "candidate")
	check := mustHonestTriple(t, provider, "check")

	res, err := h.Sacrifice(candidate, check)
	if !errors.Is(err, ErrConflictingValues) {
		t.Fatalf("Sacrifice error=%v, want ErrConflictingValues", err)
	}
	if res.Accepted {
		t.Fatal("accepted forked broadcast")
	}
	ev := h.Evidence()
	if len(ev) == 0 || ev[0].Kind != EvidenceConflict || ev[0].Sender != 1 {
		t.Fatalf("evidence=%+v, want conflict from sender 1", ev)
	}
	if len(ev[0].Messages) != 2 {
		t.Fatalf("conflict evidence retained %d messages", len(ev[0].Messages))
	}
}

func TestDuplicateBroadcastIsEvidence(t *testing.T) {
	const p = uint32(17)
	keys := mustKeys(t)
	bus := newTestBus()
	bus.setHook(LabelSigma, 0, func(m SignedMessage) []SignedMessage {
		return []SignedMessage{m, m}
	})
	h := newTestHost(t, p, NewMemoryStore(), bus, keys)
	provider, _ := NewHonestProvider(p, rand.Reader)
	candidate := mustHonestTriple(t, provider, "candidate")
	check := mustHonestTriple(t, provider, "check")
	if _, err := h.Sacrifice(candidate, check); !errors.Is(err, ErrDuplicateSender) {
		t.Fatalf("Sacrifice error=%v, want ErrDuplicateSender", err)
	}
	ev := h.Evidence()
	if len(ev) == 0 || ev[0].Kind != EvidenceDuplicate {
		t.Fatalf("evidence=%+v, want duplicate sender", ev)
	}
}

func TestMissingOpeningProducesNoResult(t *testing.T) {
	const p = uint32(17)
	f := mustField(t, p)
	keys := mustKeys(t)
	bus := newTestBus()
	h := newTestHost(t, p, NewMemoryStore(), bus, keys)
	provider, _ := NewHonestProvider(p, rand.Reader)
	candidate := mustHonestTriple(t, provider, "candidate")
	check := mustHonestTriple(t, provider, "check")
	if res, err := h.Sacrifice(candidate, check); err != nil || !res.Accepted {
		t.Fatalf("setup sacrifice: %+v %v", res, err)
	}

	bus.setHook(LabelE, 2, func(SignedMessage) []SignedMessage { return nil })
	x, y := mustShares(t, f, 8), mustShares(t, f, 9)
	mul, err := h.Multiply(candidate, x, y)
	if !errors.Is(err, ErrMissingMessage) {
		t.Fatalf("Multiply error=%v, want ErrMissingMessage", err)
	}
	if mul.Produced || mul.Z != (AdditiveShares{}) {
		t.Fatalf("produced a result after a missing opening: %+v", mul)
	}
	if status, _ := h.TripleStatus(candidate.ID); status != StatusConsumed {
		t.Fatalf("status=%q, want consumed", status)
	}
}

func TestBroadcastForkDuringMultiply(t *testing.T) {
	const p = uint32(17)
	f := mustField(t, p)
	keys := mustKeys(t)
	bus := newTestBus()
	h := newTestHost(t, p, NewMemoryStore(), bus, keys)
	provider, _ := NewHonestProvider(p, rand.Reader)
	candidate := mustHonestTriple(t, provider, "candidate")
	check := mustHonestTriple(t, provider, "check")
	if res, err := h.Sacrifice(candidate, check); err != nil || !res.Accepted {
		t.Fatalf("setup sacrifice: %+v %v", res, err)
	}

	bus.setHook(LabelD, 0, func(m SignedMessage) []SignedMessage {
		alt := mustResign(t, keys[0], f, m, shareBytes(f.Add(bytesShare(m.Value), 1)))
		return []SignedMessage{m, alt}
	})
	x, y := mustShares(t, f, 8), mustShares(t, f, 9)
	mul, err := h.Multiply(candidate, x, y)
	if !errors.Is(err, ErrConflictingValues) {
		t.Fatalf("Multiply error=%v, want ErrConflictingValues", err)
	}
	if mul.Produced || mul.Z != (AdditiveShares{}) {
		t.Fatalf("produced a result after forked opening: %+v", mul)
	}
	ev := h.Evidence()
	if len(ev) == 0 || ev[0].Kind != EvidenceConflict {
		t.Fatalf("evidence=%+v, want conflict", ev)
	}
}

func TestTamperedSignatureDuringMultiplyProducesNoZ(t *testing.T) {
	const p = uint32(17)
	f := mustField(t, p)
	keys := mustKeys(t)
	bus := newTestBus()
	h := newTestHost(t, p, NewMemoryStore(), bus, keys)
	provider, _ := NewHonestProvider(p, rand.Reader)
	candidate := mustHonestTriple(t, provider, "candidate")
	check := mustHonestTriple(t, provider, "check")
	if res, err := h.Sacrifice(candidate, check); err != nil || !res.Accepted {
		t.Fatalf("setup sacrifice: %+v %v", res, err)
	}
	bus.setHook(LabelD, 2, func(m SignedMessage) []SignedMessage {
		m.Sig = append([]byte(nil), m.Sig...)
		m.Sig[0] ^= 0xff
		return []SignedMessage{m}
	})
	x, y := mustShares(t, f, 10), mustShares(t, f, 11)
	mul, err := h.Multiply(candidate, x, y)
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("Multiply error=%v, want ErrBadSignature", err)
	}
	if mul.Produced || mul.Z != (AdditiveShares{}) {
		t.Fatalf("produced z despite bad signature: %+v", mul)
	}
	if status, _ := h.TripleStatus(candidate.ID); status != StatusConsumed {
		t.Fatalf("status=%q, want consumed", status)
	}
}

func TestCommitRevealMismatchProducesNoResult(t *testing.T) {
	const p = uint32(17)
	keys := mustKeys(t)
	bus := newTestBus()
	bus.setHook(LabelReveal, 1, func(m SignedMessage) []SignedMessage {
		bad := append([]byte(nil), m.Value...)
		bad[0] ^= 1
		return []SignedMessage{mustResign(t, keys[1], Field{P: p}, m, bad)}
	})
	h := newTestHost(t, p, NewMemoryStore(), bus, keys)
	provider, _ := NewHonestProvider(p, rand.Reader)
	candidate := mustHonestTriple(t, provider, "candidate")
	check := mustHonestTriple(t, provider, "check")
	res, err := h.Sacrifice(candidate, check)
	if !errors.Is(err, ErrCommitMismatch) {
		t.Fatalf("Sacrifice error=%v, want ErrCommitMismatch", err)
	}
	if res.Accepted {
		t.Fatal("accepted mismatched commit/reveal")
	}
	ev := h.Evidence()
	if len(ev) == 0 || ev[0].Kind != EvidenceCommitMismatch {
		t.Fatalf("evidence=%+v, want commit mismatch", ev)
	}
}

func TestTripleReuseAndIDBinding(t *testing.T) {
	const p = uint32(17)
	f := mustField(t, p)
	provider, _ := NewHonestProvider(p, rand.Reader)
	store := NewMemoryStore()
	h1, err := NewHostWithConfig(HostConfig{P: p, Store: store})
	if err != nil {
		t.Fatal(err)
	}

	// A checked ID with altered shares must be consumed without yielding z.
	candidate := mustHonestTriple(t, provider, "candidate-1")
	check := mustHonestTriple(t, provider, "check-1")
	if res, err := h1.Sacrifice(candidate, check); err != nil || !res.Accepted {
		t.Fatalf("setup sacrifice: %+v %v", res, err)
	}
	altered := candidate
	altered.C[0] = f.Add(altered.C[0], 1)
	x, y := mustShares(t, f, 2), mustShares(t, f, 3)
	mul, err := h1.Multiply(altered, x, y)
	if !errors.Is(err, ErrTripleMismatch) {
		t.Fatalf("Multiply altered shares error=%v, want ErrTripleMismatch", err)
	}
	if mul.Produced {
		t.Fatal("produced z for altered shares under a checked ID")
	}
	if _, err := h1.Multiply(candidate, x, y); !errors.Is(err, ErrTripleAlreadyUsed) {
		t.Fatalf("correct retry error=%v, want already used", err)
	}

	// A normally consumed triple cannot be consumed or sacrificed again.
	candidate2 := mustHonestTriple(t, provider, "candidate-2")
	check2 := mustHonestTriple(t, provider, "check-2")
	if res, err := h1.Sacrifice(candidate2, check2); err != nil || !res.Accepted {
		t.Fatalf("setup sacrifice 2: %+v %v", res, err)
	}
	mul, err = h1.Multiply(candidate2, x, y)
	if err != nil || !mul.Produced {
		t.Fatalf("Multiply 2: %+v %v", mul, err)
	}
	if _, err := h1.Multiply(candidate2, x, y); !errors.Is(err, ErrTripleAlreadyUsed) {
		t.Fatalf("second Multiply error=%v, want already used", err)
	}
	other := mustHonestTriple(t, provider, "other")
	if _, err := h1.Sacrifice(candidate2, other); !errors.Is(err, ErrTripleAlreadyUsed) {
		t.Fatalf("reuse as candidate error=%v, want already used", err)
	}
	if _, err := h1.Sacrifice(other, check2); !errors.Is(err, ErrTripleAlreadyUsed) {
		t.Fatalf("reuse burned check error=%v, want already used", err)
	}
}

func TestRangeFailureBeforeConsumption(t *testing.T) {
	const p = uint32(17)
	f := mustField(t, p)
	provider, _ := NewHonestProvider(p, rand.Reader)
	h, err := NewHost(p)
	if err != nil {
		t.Fatal(err)
	}
	candidate := mustHonestTriple(t, provider, "candidate")
	check := mustHonestTriple(t, provider, "check")
	if res, err := h.Sacrifice(candidate, check); err != nil || !res.Accepted {
		t.Fatalf("setup sacrifice: %+v %v", res, err)
	}
	bad := mustShares(t, f, 4)
	bad[1] = p
	y := mustShares(t, f, 5)
	mul, err := h.Multiply(candidate, bad, y)
	if !errors.Is(err, ErrInvalidShare) || mul.Produced {
		t.Fatalf("bad range: %+v %v", mul, err)
	}
	good := mustShares(t, f, 4)
	mul, err = h.Multiply(candidate, good, y)
	if err != nil || !mul.Produced {
		t.Fatalf("valid retry after pre-consumption range failure: %+v %v", mul, err)
	}
	if z, _ := mul.Secret(f); z != f.Mul(4, 5) {
		t.Fatalf("z=%d, want 20", z)
	}
}

func TestFieldValidation(t *testing.T) {
	for _, p := range []uint32{0, 1, 4, 15, 65520, 65522, 1 << 20} {
		if _, err := NewField(p); err == nil {
			t.Fatalf("NewField(%d) unexpectedly succeeded", p)
		}
	}
	if _, err := NewField(MaxPrime); err != nil {
		t.Fatalf("MaxPrime rejected: %v", err)
	}
}
