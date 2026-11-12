package taskpkg

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"math/big"
	"testing"
)

const testRound = uint64(7)

type participant struct {
	id   DeviceID
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

type testHarness struct {
	t       *testing.T
	prime   *big.Int
	devices []participant
	keys    map[DeviceID]ed25519.PublicKey
	roster  []DeviceID
	values  map[DeviceID]*big.Int
	self    map[DeviceID]*big.Int
	pair    map[PairID]*big.Int
}

func newHarness(t *testing.T) *testHarness {
	t.Helper()
	prime := big.NewInt(1_000_003)
	names := []DeviceID{"dev-a", "dev-b", "dev-c", "dev-d", "dev-e"}
	h := &testHarness{
		t:      t,
		prime:  prime,
		keys:   make(map[DeviceID]ed25519.PublicKey),
		values: make(map[DeviceID]*big.Int),
		self:   make(map[DeviceID]*big.Int),
		pair:   make(map[PairID]*big.Int),
	}
	for _, id := range names {
		pub, priv, err := ed25519.GenerateKey(newSeedReader(string(id)))
		if err != nil {
			t.Fatalf("generate key for %s: %v", id, err)
		}
		h.devices = append(h.devices, participant{id: id, priv: priv, pub: pub})
		h.keys[id] = pub
		h.roster = append(h.roster, id)
		h.values[id] = deriveScalar("value", string(id), prime)
		h.self[id] = deriveScalar("self", string(id), prime)
	}
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			h.pair[NewPairID(names[i], names[j])] =
				deriveScalar("pair", string(names[i])+"|"+string(names[j]), prime)
		}
	}
	return h
}

func (h *testHarness) key(id DeviceID) ed25519.PrivateKey {
	for _, d := range h.devices {
		if d.id == id {
			return d.priv
		}
	}
	h.t.Fatalf("unknown device %s", id)
	return nil
}

// maskedValue reproduces the mask convention: personal mask adds once, and the
// pairwise scalar adds on the low endpoint and subtracts on the high endpoint.
func (h *testHarness) maskedValue(id DeviceID) *big.Int {
	masked := new(big.Int).Set(h.values[id])
	masked.Add(masked, h.self[id])
	for pair, scalar := range h.pair {
		switch id {
		case pair.Low:
			masked.Add(masked, scalar)
		case pair.High:
			masked.Sub(masked, scalar)
		}
	}
	return masked.Mod(masked, h.prime)
}

func (h *testHarness) config(threshold int) Config {
	return Config{
		Round:       testRound,
		Prime:       h.prime,
		Threshold:   threshold,
		Roster:      append([]DeviceID(nil), h.roster...),
		TrustedKeys: h.keys,
	}
}

func (h *testHarness) sign(id DeviceID, digest []byte) []byte {
	return ed25519.Sign(h.key(id), digest)
}

func (h *testHarness) submit(phase *Phase, id DeviceID, digest []byte, share ShareType) {
	sub := Submission{
		Device:      id,
		Digest:      digest,
		Signature:   h.sign(id, digest),
		MaskedValue: h.maskedValue(id),
		ShareType:   share,
	}
	if err := phase.Submit(sub); err != nil {
		h.t.Fatalf("submit %s: %v", id, err)
	}
}

func (h *testHarness) expectedSum(ids []DeviceID) *big.Int {
	total := big.NewInt(0)
	for _, id := range ids {
		total.Add(total, h.values[id])
	}
	return total.Mod(total, h.prime)
}

// observableRecoverer is a fake recovery host that records every request so the
// test can assert which seeds a caller is allowed to observe.
type observableRecoverer struct {
	h *testHarness

	personalCalls [][]DeviceID
	pairCalls     [][]PairID
	personalSeen  map[DeviceID]int
}

func newObservable(h *testHarness) *observableRecoverer {
	return &observableRecoverer{
		h:            h,
		personalSeen: make(map[DeviceID]int),
	}
}

func (r *observableRecoverer) RecoverPersonal(_ context.Context, devices []DeviceID) (PersonalRecovery, error) {
	r.personalCalls = append(r.personalCalls, append([]DeviceID(nil), devices...))
	out := make(PersonalRecovery, len(devices))
	for _, id := range devices {
		r.personalSeen[id]++
		out[id] = new(big.Int).Set(r.h.self[id])
	}
	return out, nil
}

func (r *observableRecoverer) RecoverPairwise(_ context.Context, pairs []PairID) (PairwiseRecovery, error) {
	r.pairCalls = append(r.pairCalls, append([]PairID(nil), pairs...))
	out := make(PairwiseRecovery, len(pairs))
	for _, pair := range pairs {
		scalar := r.h.pair[pair]
		out[pair] = PairSeed{
			LowMask:  new(big.Int).Set(scalar),
			HighMask: new(big.Int).Set(scalar),
		}
	}
	return out, nil
}

// assertPrivacy checks the observable record against the completion set.
func (r *observableRecoverer) assertPrivacy(t *testing.T, completed []DeviceID) {
	t.Helper()
	if len(r.personalCalls) != 1 || len(r.pairCalls) != 1 {
		t.Fatalf("expected one call per seed kind, got personal=%d pairwise=%d",
			len(r.personalCalls), len(r.pairCalls))
	}
	done := make(map[DeviceID]struct{}, len(completed))
	for _, id := range completed {
		done[id] = struct{}{}
		if r.personalSeen[id] != 1 {
			t.Fatalf("completed device %s personal recovery count = %d, want 1",
				id, r.personalSeen[id])
		}
	}
	for id, count := range r.personalSeen {
		if _, ok := done[id]; !ok {
			t.Fatalf("personal seed of non-completed device %s was exposed %d time(s)", id, count)
		}
	}
	for _, call := range r.pairCalls {
		for _, pair := range call {
			_, lowDone := done[pair.Low]
			_, highDone := done[pair.High]
			if lowDone && highDone {
				t.Fatalf("intra-completion-set pair %s/%s was recovered", pair.Low, pair.High)
			}
		}
	}
}

func bytesEqual(a, b []byte) bool {
	return bytes.Equal(a, b)
}
