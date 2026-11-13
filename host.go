package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// PartyKey bundles one party's signature key pair.
type PartyKey struct {
	Private ed25519.PrivateKey
	Public  ed25519.PublicKey
}

// HostConfig configures the three-party test host.
type HostConfig struct {
	P     uint32
	Store Store
	Bus   Bus
	Rand  io.Reader
	Keys  [3]PartyKey
}

// CheckResult reports the public values and decision of one sacrifice check.
type CheckResult struct {
	Accepted bool
	R        uint32
	Rho      uint32
	Sigma    uint32
	Tau      uint32
}

// MultiplicationResult contains the public openings and the three output
// shares. Produced is false whenever the caller must not interpret Z as a
// result, which makes an error path distinguishable from an honest z=0.
type MultiplicationResult struct {
	Produced bool
	D        uint32
	E        uint32
	Z        AdditiveShares
}

// Secret recombines the output shares.
func (m MultiplicationResult) Secret(f Field) (uint32, error) {
	if !m.Produced {
		return 0, fmt.Errorf("%w: no multiplication result", ErrSacrificeFailed)
	}
	return f.Recombine(m.Z)
}

// Host simulates the three parties, their signing keys, broadcasts, sacrifice
// checks, multiplication openings, and durable triple lifecycle.
type Host struct {
	field Field
	store Store
	bus   Bus
	rng   io.Reader
	keys  [3]PartyKey
	round atomic.Uint64
	rngMu sync.Mutex

	evidenceMu sync.Mutex
	evidence   []Evidence

	closedMu sync.Mutex
	closed   bool
}

// NewHost creates a host with an in-memory store and fresh keys.
func NewHost(p uint32) (*Host, error) {
	return NewHostWithConfig(HostConfig{P: p})
}

// NewHostWithConfig creates a host. Missing Store, Bus, Rand, and Keys are
// filled with safe defaults.
func NewHostWithConfig(cfg HostConfig) (*Host, error) {
	f, err := NewField(cfg.P)
	if err != nil {
		return nil, err
	}
	rng := cfg.Rand
	if rng == nil {
		rng = rand.Reader
	}
	store := cfg.Store
	if store == nil {
		store = NewMemoryStore()
	}
	bus := cfg.Bus
	if bus == nil {
		bus = NewLoopbackBus()
	}
	keys, err := normalizeKeys(cfg.Keys, rng)
	if err != nil {
		return nil, err
	}
	return &Host{field: f, store: store, bus: bus, rng: rng, keys: keys}, nil
}

func normalizeKeys(keys [3]PartyKey, rng io.Reader) ([3]PartyKey, error) {
	allEmpty := true
	for i := range keys {
		if len(keys[i].Private) != 0 || len(keys[i].Public) != 0 {
			allEmpty = false
		}
	}
	if allEmpty {
		for i := range keys {
			pub, priv, err := ed25519.GenerateKey(rng)
			if err != nil {
				return keys, fmt.Errorf("%w: key generation: %v", ErrRandom, err)
			}
			keys[i] = PartyKey{Private: priv, Public: pub}
		}
		return keys, nil
	}
	for i := range keys {
		if len(keys[i].Private) != ed25519.PrivateKeySize {
			return keys, fmt.Errorf("%w: party %d private key", ErrMalformedMessage, i)
		}
		derived, ok := keys[i].Private.Public().(ed25519.PublicKey)
		if !ok {
			return keys, fmt.Errorf("%w: party %d public key", ErrMalformedMessage, i)
		}
		if len(keys[i].Public) == 0 {
			keys[i].Public = derived
		} else if !bytes.Equal(keys[i].Public, derived) {
			return keys, fmt.Errorf("%w: party %d key mismatch", ErrMalformedMessage, i)
		}
	}
	return keys, nil
}

// Field returns the configured prime field.
func (h *Host) Field() Field {
	return h.field
}

// PublicKeys returns copies of the three party verification keys.
func (h *Host) PublicKeys() [3]ed25519.PublicKey {
	var out [3]ed25519.PublicKey
	for i := range h.keys {
		out[i] = append(ed25519.PublicKey(nil), h.keys[i].Public...)
	}
	return out
}

// Bus returns the configured broadcast channel.
func (h *Host) Bus() Bus {
	return h.bus
}

// Evidence returns a copy of all cheating evidence observed by this host.
func (h *Host) Evidence() []Evidence {
	h.evidenceMu.Lock()
	defer h.evidenceMu.Unlock()
	out := make([]Evidence, len(h.evidence))
	for i := range h.evidence {
		out[i] = h.evidence[i]
		out[i].Messages = make([]SignedMessage, len(h.evidence[i].Messages))
		for j := range h.evidence[i].Messages {
			out[i].Messages[j] = cloneMessage(h.evidence[i].Messages[j])
		}
	}
	return out
}

func (h *Host) recordEvidence(evs []Evidence) {
	if len(evs) == 0 {
		return
	}
	h.evidenceMu.Lock()
	defer h.evidenceMu.Unlock()
	h.evidence = append(h.evidence, evs...)
}

func (h *Host) isClosed() bool {
	h.closedMu.Lock()
	defer h.closedMu.Unlock()
	return h.closed
}

// Close closes this host's store handle.
func (h *Host) Close() error {
	h.closedMu.Lock()
	if h.closed {
		h.closedMu.Unlock()
		return nil
	}
	h.closed = true
	h.closedMu.Unlock()
	return h.store.Close()
}

func (h *Host) nextRound() RoundID {
	return RoundID(h.round.Add(1))
}

func (h *Host) broadcastShare(sender int, round RoundID, pair TriplePair, label string, value uint32) error {
	if sender < 0 || sender > 2 {
		return fmt.Errorf("%w: sender=%d", ErrMalformedMessage, sender)
	}
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], value)
	m, err := SignMessage(h.keys[sender].Private, h.field, round, pair, uint8(sender), label, buf[:])
	if err != nil {
		return err
	}
	if err := h.bus.Publish(m); err != nil {
		return fmt.Errorf("%w: publish %s: %v", ErrMissingMessage, label, err)
	}
	return nil
}

func (h *Host) broadcastBytes(sender int, round RoundID, pair TriplePair, label string, value []byte) error {
	if sender < 0 || sender > 2 {
		return fmt.Errorf("%w: sender=%d", ErrMalformedMessage, sender)
	}
	m, err := SignMessage(h.keys[sender].Private, h.field, round, pair, uint8(sender), label, value)
	if err != nil {
		return err
	}
	if err := h.bus.Publish(m); err != nil {
		return fmt.Errorf("%w: publish %s: %v", ErrMissingMessage, label, err)
	}
	return nil
}

func (h *Host) collectShare(round RoundID, pair TriplePair, label string, expected [3]uint32) (uint32, error) {
	msgs := h.bus.Messages(round, pair, label)
	v, ev, err := AggregateShareMessages(h.field, h.PublicKeys(), round, pair, label, msgs)
	h.recordEvidence(ev)
	if err != nil {
		return 0, err
	}
	for _, raw := range msgs {
		m := cloneMessage(raw)
		if m.Sender > 2 || m.Round != round || m.Pair != pair || m.Label != label {
			continue
		}
		if err := VerifyMessage(h.keys[m.Sender].Public, h.field, m); err != nil {
			continue
		}
		if len(m.Value) != 4 {
			continue
		}
		got := binary.BigEndian.Uint32(m.Value)
		if got == expected[m.Sender] {
			continue
		}
		h.recordEvidence([]Evidence{{
			Round: round, Pair: pair, Label: label, Sender: m.Sender,
			Kind: EvidenceOpeningMismatch, Messages: []SignedMessage{m},
			Detail: fmt.Sprintf("broadcast share %d differs from local share %d", got, expected[m.Sender]),
		}})
		return 0, fmt.Errorf("%w: sender %d, label %s", ErrOpeningMismatch, m.Sender, label)
	}
	return v, nil
}

func (h *Host) collectBytes(round RoundID, pair TriplePair, label string, valueLen int) ([3][]byte, error) {
	msgs := h.bus.Messages(round, pair, label)
	vals, ev, err := AggregateByteMessages(h.field, h.PublicKeys(), round, pair, label, valueLen, msgs)
	h.recordEvidence(ev)
	return vals, err
}

func (h *Host) reserveCheck(candidate, check Triple) error {
	cDigest := TripleDigest(h.field, candidate)
	kDigest := TripleDigest(h.field, check)
	err := h.store.Update(func(s *State) error {
		cRec := recordOf(s, candidate.ID)
		kRec := recordOf(s, check.ID)
		if cRec.Status != StatusUnused {
			return fmt.Errorf("%w: candidate %q is %s", ErrTripleAlreadyUsed, candidate.ID, cRec.Status)
		}
		if kRec.Status != StatusUnused {
			return fmt.Errorf("%w: check %q is %s", ErrTripleAlreadyUsed, check.ID, kRec.Status)
		}
		s.Triples[candidate.ID] = TripleRecord{Status: StatusChecking, Digest: cDigest}
		s.Triples[check.ID] = TripleRecord{Status: StatusChecking, Digest: kDigest}
		return nil
	})
	if err != nil {
		return fmt.Errorf("reserve check: %w", err)
	}
	return nil
}

func (h *Host) finishCheck(candidate, check Triple, accepted bool) error {
	cDigest := TripleDigest(h.field, candidate)
	kDigest := TripleDigest(h.field, check)
	cStatus := StatusBurned
	if accepted {
		cStatus = StatusChecked
	}
	err := h.store.Update(func(s *State) error {
		cRec := recordOf(s, candidate.ID)
		kRec := recordOf(s, check.ID)
		if cRec.Status != StatusChecking || cRec.Digest != cDigest {
			return fmt.Errorf("%w: candidate reservation changed", ErrStore)
		}
		if kRec.Status != StatusChecking || kRec.Digest != kDigest {
			return fmt.Errorf("%w: check reservation changed", ErrStore)
		}
		s.Triples[candidate.ID] = TripleRecord{Status: cStatus, Digest: cDigest}
		s.Triples[check.ID] = TripleRecord{Status: StatusBurned, Digest: kDigest}
		return nil
	})
	if err != nil {
		return fmt.Errorf("%w: finish check: %v", ErrStore, err)
	}
	return nil
}

// Sacrifice performs the paired sacrifice check. The check triple is always
// burned. The candidate becomes usable only if tau == 0; otherwise it is
// burned too. Any malformed, unsigned, out-of-range, or incomplete broadcast
// also burns both IDs and produces no accepted result.
func (h *Host) Sacrifice(candidate, check Triple) (CheckResult, error) {
	var zero CheckResult
	if h.isClosed() {
		return zero, ErrClosed
	}
	if err := h.field.ValidateTriple(candidate); err != nil {
		return zero, err
	}
	if err := h.field.ValidateTriple(check); err != nil {
		return zero, err
	}
	pair, err := OrderedPair(candidate, check)
	if err != nil {
		return zero, err
	}
	if err := h.reserveCheck(candidate, check); err != nil {
		return zero, err
	}

	res := CheckResult{}
	finish := func(cause error) (CheckResult, error) {
		if ferr := h.finishCheck(candidate, check, false); ferr != nil {
			if cause == nil {
				return res, ferr
			}
			return res, errors.Join(cause, ferr)
		}
		return res, cause
	}

	round := h.nextRound()
	var seeds [3][]byte
	h.rngMu.Lock()
	for i := range seeds {
		seed := make([]byte, 32)
		if _, err := io.ReadFull(h.rng, seed); err != nil {
			h.rngMu.Unlock()
			return finish(fmt.Errorf("%w: seed: %v", ErrRandom, err))
		}
		seeds[i] = seed
	}
	h.rngMu.Unlock()

	for i := range seeds {
		commit := SeedCommitment(h.field, round, pair, uint8(i), seeds[i])
		if err := h.broadcastBytes(i, round, pair, LabelCommit, commit[:]); err != nil {
			return finish(err)
		}
	}
	commitVals, err := h.collectBytes(round, pair, LabelCommit, 32)
	if err != nil {
		return finish(err)
	}
	for i := range seeds {
		want := SeedCommitment(h.field, round, pair, uint8(i), seeds[i])
		if !bytes.Equal(commitVals[i], want[:]) {
			offending, _ := signedValueInContext(h.field, h.PublicKeys(), round, pair, LabelCommit, uint8(i), h.bus.Messages(round, pair, LabelCommit))
			h.recordEvidence([]Evidence{{
				Round: round, Pair: pair, Label: LabelCommit, Sender: uint8(i),
				Kind: EvidenceCommitMismatch, Messages: []SignedMessage{offending},
				Detail: "observed commitment differs from the locally signed commitment",
			}})
			return finish(fmt.Errorf("%w: party %d commitment", ErrCommitMismatch, i))
		}
	}

	for i := range seeds {
		if err := h.broadcastBytes(i, round, pair, LabelReveal, seeds[i]); err != nil {
			return finish(err)
		}
	}
	revealVals, err := h.collectBytes(round, pair, LabelReveal, 32)
	if err != nil {
		return finish(err)
	}
	var revealed [3][]byte
	for i := range seeds {
		commit := SeedCommitment(h.field, round, pair, uint8(i), revealVals[i])
		if !bytes.Equal(commit[:], commitVals[i]) {
			offending, _ := signedValueInContext(h.field, h.PublicKeys(), round, pair, LabelReveal, uint8(i), h.bus.Messages(round, pair, LabelReveal))
			h.recordEvidence([]Evidence{{
				Round: round, Pair: pair, Label: LabelReveal, Sender: uint8(i),
				Kind: EvidenceCommitMismatch, Messages: []SignedMessage{offending},
				Detail: "revealed seed does not match the commitment",
			}})
			return finish(fmt.Errorf("%w: party %d reveal", ErrCommitMismatch, i))
		}
		revealed[i] = revealVals[i]
	}
	r, err := DeriveSacrificeRandom(h.field, round, pair, revealed)
	if err != nil || r == 0 || !h.field.Valid(r) {
		return finish(fmt.Errorf("%w: derived r", ErrRandom))
	}
	res.R = r

	var rhoShares, sigmaShares [3]uint32
	for i := 0; i < 3; i++ {
		rhoShares[i] = h.field.Sub(candidate.A[i], h.field.Mul(r, check.A[i]))
		sigmaShares[i] = h.field.Sub(candidate.B[i], check.B[i])
	}
	for i := 0; i < 3; i++ {
		if err := h.broadcastShare(i, round, pair, LabelRho, rhoShares[i]); err != nil {
			return finish(err)
		}
		if err := h.broadcastShare(i, round, pair, LabelSigma, sigmaShares[i]); err != nil {
			return finish(err)
		}
	}
	rho, err := h.collectShare(round, pair, LabelRho, rhoShares)
	if err != nil {
		return finish(err)
	}
	sigma, err := h.collectShare(round, pair, LabelSigma, sigmaShares)
	if err != nil {
		return finish(err)
	}
	res.Rho, res.Sigma = rho, sigma

	var tauShares [3]uint32
	for i := 0; i < 3; i++ {
		tauShare := h.field.Sub(candidate.C[i], h.field.Mul(r, check.C[i]))
		tauShare = h.field.Sub(tauShare, h.field.Mul(rho, check.B[i]))
		tauShare = h.field.Sub(tauShare, h.field.Mul(r, h.field.Mul(sigma, check.A[i])))
		if i == 0 {
			tauShare = h.field.Sub(tauShare, h.field.Mul(rho, sigma))
		}
		tauShares[i] = tauShare
		if err := h.broadcastShare(i, round, pair, LabelTau, tauShare); err != nil {
			return finish(err)
		}
	}
	tau, err := h.collectShare(round, pair, LabelTau, tauShares)
	if err != nil {
		return finish(err)
	}
	res.Tau = tau
	res.Accepted = tau == 0
	if err := h.finishCheck(candidate, check, res.Accepted); err != nil {
		res.Accepted = false
		return res, err
	}
	return res, nil
}

func (h *Host) reserveMultiply(t Triple) error {
	digest := TripleDigest(h.field, t)
	var mismatch bool
	err := h.store.Update(func(s *State) error {
		rec := recordOf(s, t.ID)
		switch rec.Status {
		case StatusUnused:
			return fmt.Errorf("%w: %q", ErrTripleNotChecked, t.ID)
		case StatusChecked:
			if rec.Digest != digest {
				mismatch = true
			}
			s.Triples[t.ID] = TripleRecord{Status: StatusConsumed, Digest: digest}
			return nil
		default:
			return fmt.Errorf("%w: %q is %s", ErrTripleAlreadyUsed, t.ID, rec.Status)
		}
	})
	if err != nil {
		if mismatch {
			return fmt.Errorf("%w: %q", ErrTripleMismatch, t.ID)
		}
		if errors.Is(err, ErrTripleNotChecked) || errors.Is(err, ErrTripleAlreadyUsed) {
			return err
		}
		return fmt.Errorf("%w: reserve multiply: %v", ErrStore, err)
	}
	if mismatch {
		return fmt.Errorf("%w: %q", ErrTripleMismatch, t.ID)
	}
	return nil
}

// Multiply consumes one checked triple and opens d=x-a and e=y-b. All three
// shares are broadcast and signature-checked. On any failure, Produced is
// false and no usable z exists. The triple is already durably consumed before
// any opening is broadcast.
func (h *Host) Multiply(t Triple, xShares, yShares AdditiveShares) (MultiplicationResult, error) {
	var zero MultiplicationResult
	if h.isClosed() {
		return zero, ErrClosed
	}
	if err := h.field.ValidateTriple(t); err != nil {
		return zero, err
	}
	if err := h.field.ValidateShares(xShares); err != nil {
		return zero, fmt.Errorf("%w: x shares", err)
	}
	if err := h.field.ValidateShares(yShares); err != nil {
		return zero, fmt.Errorf("%w: y shares", err)
	}
	if err := h.reserveMultiply(t); err != nil {
		return zero, err
	}

	pair := TriplePair{First: t.ID}
	round := h.nextRound()
	var dShares, eShares [3]uint32
	for i := 0; i < 3; i++ {
		dShares[i] = h.field.Sub(xShares[i], t.A[i])
		eShares[i] = h.field.Sub(yShares[i], t.B[i])
	}
	for i := 0; i < 3; i++ {
		if err := h.broadcastShare(i, round, pair, LabelD, dShares[i]); err != nil {
			return zero, err
		}
		if err := h.broadcastShare(i, round, pair, LabelE, eShares[i]); err != nil {
			return zero, err
		}
	}
	d, err := h.collectShare(round, pair, LabelD, dShares)
	if err != nil {
		return zero, err
	}
	e, err := h.collectShare(round, pair, LabelE, eShares)
	if err != nil {
		return zero, err
	}

	out := MultiplicationResult{Produced: true, D: d, E: e}
	for i := 0; i < 3; i++ {
		z := h.field.Add(t.C[i], h.field.Mul(d, t.B[i]))
		z = h.field.Add(z, h.field.Mul(e, t.A[i]))
		if i == 0 {
			z = h.field.Add(z, h.field.Mul(d, e))
		}
		out.Z[i] = z
	}
	return out, nil
}

// CheckAndMultiply performs a sacrifice check with checkID and, if accepted,
// immediately consumes the candidate for one multiplication.
func (h *Host) CheckAndMultiply(candidate, check Triple, xShares, yShares AdditiveShares) (MultiplicationResult, error) {
	res, err := h.Sacrifice(candidate, check)
	if err != nil {
		return MultiplicationResult{}, err
	}
	if !res.Accepted {
		return MultiplicationResult{}, fmt.Errorf("%w: tau=%d", ErrSacrificeFailed, res.Tau)
	}
	return h.Multiply(candidate, xShares, yShares)
}

// TripleStatus returns the durable status for an ID.
func (h *Host) TripleStatus(id string) (TripleStatus, error) {
	var out TripleStatus
	err := h.store.View(func(s *State) error {
		out = recordOf(s, id).Status
		return nil
	})
	if err != nil {
		return "", err
	}
	return out, nil
}
