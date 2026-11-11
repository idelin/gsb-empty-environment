package sharevault

import (
	"crypto/sha256"
	"encoding/binary"
	"io"
	"sort"
)

// Contribution is one participant's refresh dealing: a random polynomial
// of degree threshold-1 with a zero constant term, evaluated at every
// participant's coordinate. Adding all contributions to the current
// shares produces a fresh sharing of the same secret.
type Contribution struct {
	Generation uint64
	From       uint16
	Deltas     []Delta
	Commitment [sha256.Size]byte
}

// GenerateContribution samples a random zero-constant polynomial of
// degree threshold-1 and evaluates it at every coordinate in xs.
func GenerateContribution(rng io.Reader, generation uint64, from uint16, xs []uint16, threshold int) (*Contribution, error) {
	if from == 0 || !feValid(from) {
		return nil, ErrInvalidCoordinate
	}
	if threshold < 1 || threshold > len(xs) {
		return nil, ErrInvalidThreshold
	}
	if err := validateXS(xs); err != nil {
		return nil, err
	}
	coeffs := make([]uint16, threshold)
	coeffs[0] = 0
	for i := 1; i < threshold; i++ {
		c, err := randomFieldElement(rng)
		if err != nil {
			return nil, err
		}
		coeffs[i] = c
	}
	c := &Contribution{
		Generation: generation,
		From:       from,
		Deltas:     make([]Delta, len(xs)),
	}
	for i, x := range xs {
		c.Deltas[i] = Delta{X: x, Y: evalPoly(coeffs, x)}
	}
	h := sha256.New()
	h.Write([]byte("sharevault-commitment-v1"))
	writeUint64(h, generation)
	writeUint16(h, from)
	for _, coeff := range coeffs {
		writeUint16(h, coeff)
	}
	copy(c.Commitment[:], h.Sum(nil))
	return c, nil
}

func writeUint16(w io.Writer, v uint16) {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], v)
	w.Write(b[:])
}

func writeUint64(w io.Writer, v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	w.Write(b[:])
}

// RefreshSession runs one proactive refresh round. Every participant
// contributes a zero-constant polynomial; once all contributions are in,
// every receiver confirms the same round digest, and only then does
// Commit atomically publish the complete next-generation share set.
// A session that is interrupted before Commit publishes nothing and
// leaves the current generation untouched.
type RefreshSession struct {
	generation uint64
	threshold  int
	xs         []uint16
	oldY       map[uint16]uint16
	contribs   map[uint16]*Contribution
	confirms   map[uint16][sha256.Size]byte
	committed  bool
}

// NewRefreshSession opens a refresh round for the given current-generation
// shares. The shares must all belong to one generation, have distinct
// valid coordinates, and number at least threshold.
func NewRefreshSession(old []Share, threshold int) (*RefreshSession, error) {
	if len(old) == 0 {
		return nil, ErrNoShares
	}
	if threshold < 1 || threshold > len(old) {
		return nil, ErrInvalidThreshold
	}
	generation := old[0].Generation
	s := &RefreshSession{
		generation: generation,
		threshold:  threshold,
		oldY:       make(map[uint16]uint16, len(old)),
		contribs:   make(map[uint16]*Contribution, len(old)),
		confirms:   make(map[uint16][sha256.Size]byte, len(old)),
	}
	for _, sh := range old {
		if sh.Generation != generation {
			return nil, ErrGenerationMismatch
		}
		if sh.X == 0 || !feValid(sh.X) {
			return nil, ErrInvalidCoordinate
		}
		if !feValid(sh.Y) {
			return nil, ErrInvalidShareValue
		}
		if _, dup := s.oldY[sh.X]; dup {
			return nil, ErrDuplicateX
		}
		s.oldY[sh.X] = sh.Y
		s.xs = append(s.xs, sh.X)
	}
	sort.Slice(s.xs, func(i, j int) bool { return s.xs[i] < s.xs[j] })
	return s, nil
}

// Generation returns the generation this round refreshes from; the
// committed shares belong to Generation()+1.
func (s *RefreshSession) Generation() uint64 { return s.generation }

// Xs returns the participant coordinates in canonical (sorted) order.
func (s *RefreshSession) Xs() []uint16 { return append([]uint16(nil), s.xs...) }

// AddContribution registers one participant's dealing. It rejects
// contributions for the wrong generation, unknown or duplicate
// contributors, malformed delta sets, and dealings whose polynomial
// has a non-zero constant term or inconsistent points.
func (s *RefreshSession) AddContribution(c *Contribution) error {
	if s.committed {
		return ErrAlreadyCommitted
	}
	if c == nil {
		return ErrIncompleteRound
	}
	if c.Generation != s.generation {
		return ErrGenerationMismatch
	}
	if _, ok := s.oldY[c.From]; !ok {
		return ErrUnknownParticipant
	}
	if _, dup := s.contribs[c.From]; dup {
		return ErrDuplicateContribution
	}
	if len(c.Deltas) != len(s.xs) {
		return ErrInconsistentDeltas
	}
	deltas := make([]Delta, len(c.Deltas))
	copy(deltas, c.Deltas)
	sort.Slice(deltas, func(i, j int) bool { return deltas[i].X < deltas[j].X })
	for i, d := range deltas {
		if d.X != s.xs[i] || !feValid(d.Y) {
			return ErrInconsistentDeltas
		}
	}
	if err := verifyZeroConstant(deltas, s.threshold); err != nil {
		return err
	}
	stored := *c
	stored.Deltas = deltas
	s.contribs[c.From] = &stored
	return nil
}

// verifyZeroConstant checks that the deltas lie on a single polynomial
// of degree < threshold whose constant term is zero, using the first
// threshold points to pin the polynomial and the rest as witnesses.
func verifyZeroConstant(deltas []Delta, threshold int) error {
	basis := deltas[:threshold]
	if interpolateAt(basis, 0) != 0 {
		return ErrNonZeroConstant
	}
	for _, d := range deltas[threshold:] {
		if interpolateAt(basis, d.X) != d.Y {
			return ErrInconsistentDeltas
		}
	}
	return nil
}

// Digest returns the canonical round digest over the generation,
// threshold, participant set, and every contribution received so far.
// All receivers must confirm this exact digest before Commit succeeds.
func (s *RefreshSession) Digest() [sha256.Size]byte {
	h := sha256.New()
	h.Write([]byte("sharevault-refresh-digest-v1"))
	writeUint64(h, s.generation)
	writeUint16(h, uint16(s.threshold))
	writeUint16(h, uint16(len(s.xs)))
	for _, x := range s.xs {
		writeUint16(h, x)
	}
	froms := make([]uint16, 0, len(s.contribs))
	for from := range s.contribs {
		froms = append(froms, from)
	}
	sort.Slice(froms, func(i, j int) bool { return froms[i] < froms[j] })
	writeUint16(h, uint16(len(froms)))
	for _, from := range froms {
		c := s.contribs[from]
		writeUint16(h, from)
		h.Write(c.Commitment[:])
		for _, d := range c.Deltas {
			writeUint16(h, d.X)
			writeUint16(h, d.Y)
		}
	}
	var digest [sha256.Size]byte
	copy(digest[:], h.Sum(nil))
	return digest
}

// Confirm records that the participant owning coordinate x has verified
// and accepted the round digest. It fails if the digest does not match
// the session's current digest.
func (s *RefreshSession) Confirm(x uint16, digest [sha256.Size]byte) error {
	if s.committed {
		return ErrAlreadyCommitted
	}
	if _, ok := s.oldY[x]; !ok {
		return ErrUnknownParticipant
	}
	if digest != s.Digest() {
		return ErrDigestMismatch
	}
	s.confirms[x] = digest
	return nil
}

// Commit atomically switches the generation. It succeeds only after
// every participant has contributed and every participant has confirmed
// the current round digest; on success it returns the complete
// next-generation share set. On any failure it returns nil and no
// partial share set is published.
func (s *RefreshSession) Commit() ([]Share, error) {
	if s.committed {
		return nil, ErrAlreadyCommitted
	}
	if len(s.contribs) != len(s.xs) {
		return nil, ErrIncompleteRound
	}
	digest := s.Digest()
	for _, x := range s.xs {
		confirmed, ok := s.confirms[x]
		if !ok || confirmed != digest {
			return nil, ErrMissingConfirmation
		}
	}
	shares := make([]Share, len(s.xs))
	for i, x := range s.xs {
		y := s.oldY[x]
		for _, from := range s.xs {
			for _, d := range s.contribs[from].Deltas {
				if d.X == x {
					y = feAdd(y, d.Y)
					break
				}
			}
		}
		shares[i] = Share{X: x, Y: y, Generation: s.generation + 1}
	}
	s.committed = true
	return shares, nil
}
