package taskpkg

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash"
	"math/big"
	"sort"
)

// DeviceID is the stable identity of a participating device.
type DeviceID string

// ShareType identifies which kind of mask seed a submitted share belongs to.
type ShareType uint8

const (
	// ShareUnknown is the zero value and is never accepted.
	ShareUnknown ShareType = iota
	// SharePersonal marks a share of a device's personal (self) mask seed.
	SharePersonal
	// SharePairwise marks a share of a pairwise mask seed.
	SharePairwise
)

// Phase validation errors. Callers can compare against these sentinels with
// errors.Is.
var (
	// ErrInvalidConfig is returned when the phase configuration is unusable.
	ErrInvalidConfig = errors.New("taskpkg: invalid phase configuration")
	// ErrInvalidSubmission is returned when a submission is malformed.
	ErrInvalidSubmission = errors.New("taskpkg: invalid submission")
	// ErrUntrustedDevice is returned when the submitter is not on the trusted roster.
	ErrUntrustedDevice = errors.New("taskpkg: device is not a trusted participant")
	// ErrInvalidSignature is returned when the set-digest signature does not verify.
	ErrInvalidSignature = errors.New("taskpkg: invalid set digest signature")
	// ErrDuplicateIdentity is returned when a device submits more than once.
	ErrDuplicateIdentity = errors.New("taskpkg: duplicate device submission")
	// ErrLateSubmission is returned when a submission arrives after the phase sealed.
	ErrLateSubmission = errors.New("taskpkg: submission arrived after the phase sealed")
	// ErrPhaseNotSealed is returned when finalization is attempted before sealing.
	ErrPhaseNotSealed = errors.New("taskpkg: phase must be sealed before finalization")
	// ErrSplitView is returned when more than one participation digest reaches the
	// confirmation threshold, so no single completion view is authoritative.
	ErrSplitView = errors.New("taskpkg: competing participation views reached threshold")
	// ErrInsufficientConfirmations is returned when fewer than t distinct trusted
	// devices signed the winning participation digest.
	ErrInsufficientConfirmations = errors.New("taskpkg: not enough distinct digest confirmations")
)

// Config describes one aggregation phase.
type Config struct {
	// Round binds every signature and recovery request to a single execution.
	Round uint64
	// Prime is the field modulus used for every masked integer.
	Prime *big.Int
	// Threshold is the minimum number of distinct trusted devices that must sign
	// the same participation digest for a completion set to exist.
	Threshold int
	// Roster is the full ordered list of eligible device identities.
	Roster []DeviceID
	// TrustedKeys maps every roster identity to its Ed25519 public verification key.
	TrustedKeys map[DeviceID]ed25519.PublicKey
}

func (c Config) validate() error {
	if c.Prime == nil || !c.Prime.ProbablyPrime(20) || c.Prime.Sign() <= 0 {
		return ErrInvalidConfig
	}
	if c.Threshold < 1 || len(c.Roster) == 0 || c.Threshold > len(c.Roster) {
		return ErrInvalidConfig
	}
	seen := make(map[DeviceID]struct{}, len(c.Roster))
	for _, id := range c.Roster {
		if id == "" {
			return ErrInvalidConfig
		}
		if _, dup := seen[id]; dup {
			return ErrInvalidConfig
		}
		seen[id] = struct{}{}
		key, ok := c.TrustedKeys[id]
		if !ok || len(key) != ed25519.PublicKeySize {
			return ErrInvalidConfig
		}
	}
	return nil
}

// Submission is one device's phase contribution: a signed participation-set
// digest, its masked integer, and the type of mask seed share it carries.
type Submission struct {
	Device      DeviceID
	Digest      []byte
	Signature   []byte
	MaskedValue *big.Int
	ShareType   ShareType
}

// Phase collects signed submissions until it is sealed, then finalizes exactly
// one completion set.
type Phase struct {
	cfg         Config
	prime       *big.Int
	sealed      bool
	submissions map[DeviceID]Submission
	votes       map[string]int
}

// NewPhase validates cfg and constructs an open submission phase.
func NewPhase(cfg Config) (*Phase, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	clone := Config{
		Round:       cfg.Round,
		Prime:       new(big.Int).Set(cfg.Prime),
		Threshold:   cfg.Threshold,
		Roster:      append([]DeviceID(nil), cfg.Roster...),
		TrustedKeys: make(map[DeviceID]ed25519.PublicKey, len(cfg.TrustedKeys)),
	}
	for id, key := range cfg.TrustedKeys {
		clone.TrustedKeys[id] = append(ed25519.PublicKey(nil), key...)
	}
	return &Phase{
		cfg:         clone,
		prime:       new(big.Int).Set(clone.Prime),
		submissions: make(map[DeviceID]Submission),
		votes:       make(map[string]int),
	}, nil
}

// RosterDigest computes the canonical participation-set digest over a roster.
// It is order-independent: callers may pass identities in any order.
func RosterDigest(round uint64, prime *big.Int, roster []DeviceID) []byte {
	ids := append([]DeviceID(nil), roster...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	h := sha256.New()
	var prefix [8]byte
	binary.BigEndian.PutUint64(prefix[:], round)
	h.Write(prefix[:])
	if prime != nil {
		writeSized(h, prime.Bytes())
	}
	for _, id := range ids {
		writeSized(h, []byte(id))
	}
	return h.Sum(nil)
}

func writeSized(h hash.Hash, b []byte) {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(b)))
	h.Write(length[:])
	h.Write(b)
}

// Submit accepts one signed submission while the phase is open.
func (p *Phase) Submit(sub Submission) error {
	if p.sealed {
		return ErrLateSubmission
	}
	if sub.Device == "" || len(sub.Digest) != sha256.Size ||
		len(sub.Signature) != ed25519.SignatureSize || sub.MaskedValue == nil {
		return ErrInvalidSubmission
	}
	if sub.ShareType != SharePersonal && sub.ShareType != SharePairwise {
		return ErrInvalidSubmission
	}
	if _, known := p.cfg.TrustedKeys[sub.Device]; !known {
		return ErrUntrustedDevice
	}
	if _, exists := p.submissions[sub.Device]; exists {
		return ErrDuplicateIdentity
	}
	if !ed25519.Verify(p.cfg.TrustedKeys[sub.Device], sub.Digest, sub.Signature) {
		return ErrInvalidSignature
	}
	p.votes[hex.EncodeToString(sub.Digest)]++

	accepted := Submission{
		Device:      sub.Device,
		Digest:      append([]byte(nil), sub.Digest...),
		Signature:   append([]byte(nil), sub.Signature...),
		MaskedValue: modPrime(sub.MaskedValue, p.prime),
		ShareType:   sub.ShareType,
	}
	p.submissions[sub.Device] = accepted
	return nil
}

// Seal closes the submission window. It is idempotent.
func (p *Phase) Seal() {
	p.sealed = true
}

// CompletionSet returns the sorted identities whose accepted submissions signed
// digest. The caller must still confirm the returned set's threshold vote with
// Finalize; this helper only materializes the view for plan construction.
func (p *Phase) CompletionSet(digest []byte) []DeviceID {
	var ids []DeviceID
	for id, sub := range p.submissions {
		if bytes.Equal(sub.Digest, digest) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (p *Phase) winningDigest() ([]byte, int, error) {
	type tally struct {
		digest string
		count  int
	}
	var tallies []tally
	for digest, count := range p.votes {
		tallies = append(tallies, tally{digest, count})
	}
	sort.Slice(tallies, func(i, j int) bool {
		if tallies[i].count != tallies[j].count {
			return tallies[i].count > tallies[j].count
		}
		return tallies[i].digest < tallies[j].digest
	})
	if len(tallies) == 0 || tallies[0].count < p.cfg.Threshold {
		return nil, 0, ErrInsufficientConfirmations
	}
	winner, err := hex.DecodeString(tallies[0].digest)
	if err != nil {
		return nil, 0, ErrInvalidSubmission
	}
	for _, other := range tallies[1:] {
		if other.count >= p.cfg.Threshold {
			return nil, 0, ErrSplitView
		}
	}
	return winner, tallies[0].count, nil
}

func modPrime(value, prime *big.Int) *big.Int {
	return new(big.Int).Mod(new(big.Int).Set(value), prime)
}
