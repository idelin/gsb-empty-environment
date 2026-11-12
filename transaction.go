package netsec

import (
	"crypto/rand"
	"math/big"
	"sync"
)

// MinChallengeLen is the FC-SP minimum challenge size: 128 bits / 16 bytes.
const MinChallengeLen = 16

// challengeLen is the size of challenges generated locally.
const challengeLen = 32

// State is the lifecycle state of one AUTH_ELS transaction.
type State uint8

const (
	// StateInit waits for the first AUTH_ELS (negotiation) message.
	StateInit State = iota
	// StateNegotiated means parameters are agreed and a shared secret exists.
	StateNegotiated
	// StateVerified means the peer CHAP response was accepted.
	StateVerified
	// StateFailed is terminal: the transaction must be restarted.
	StateFailed
)

func (s State) String() string {
	switch s {
	case StateInit:
		return "init"
	case StateNegotiated:
		return "negotiated"
	case StateVerified:
		return "verified"
	case StateFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// Transaction drives one port authentication exchange. A failed transaction
// never recovers and never falls back to plain CHAP: the caller must reject
// the exchange and start a new Transaction.
type Transaction struct {
	mu sync.Mutex

	policy Policy
	state  State

	hashID  HashID
	groupID DHGroupID

	group     GroupParams
	nullDH    bool
	localKeys KeyPair

	peerPublic *big.Int
	rawShared  *big.Int

	localChapID    []byte
	localChallenge []byte
	peerChapID     []byte
	peerChallenge  []byte
	peerResponse   []byte

	localName string
	peerName  string

	// observedChallenges rejects any challenge value seen more than once in
	// this transaction, regardless of direction.
	observedChallenges map[string]struct{}
}

// NewTransaction validates the policy and creates an exchange in StateInit.
// localName is optional and, when set, is sent in the Name_Set TLV.
func NewTransaction(policy Policy, localName string) (*Transaction, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return &Transaction{
		policy:             policy,
		state:              StateInit,
		localName:          localName,
		observedChallenges: map[string]struct{}{},
	}, nil
}

// State reports the current lifecycle state.
func (t *Transaction) State() State {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

// HashID reports the negotiated hash, valid once StateNegotiated is reached.
func (t *Transaction) HashID() HashID {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.hashID
}

// DHGroupID reports the negotiated DH group. DHGroupNull means null DH was
// explicitly permitted and selected.
func (t *Transaction) DHGroupID() DHGroupID {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.groupID
}

// IsNullDH reports whether the null DH group was negotiated.
func (t *Transaction) IsNullDH() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.nullDH
}

// PeerName returns the Name_Set value advertised by the peer, if any.
func (t *Transaction) PeerName() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.peerName
}

// fail marks the transaction terminally failed. Callers must hold t.mu.
func (t *Transaction) fail() {
	t.state = StateFailed
}

// observeChallenge enforces the 16-byte minimum and rejects reuse of the same
// challenge value within the transaction. Callers must hold t.mu.
func (t *Transaction) observeChallenge(ch []byte) error {
	if len(ch) < MinChallengeLen {
		return ErrChallengeTooShort
	}
	key := string(ch)
	if _, seen := t.observedChallenges[key]; seen {
		return ErrChallengeReused
	}
	t.observedChallenges[key] = struct{}{}
	return nil
}

// generateChallengeLocked returns a fresh random challenge that has not been
// used in this transaction. Callers must hold t.mu.
func (t *Transaction) generateChallengeLocked() ([]byte, error) {
	for attempt := 0; attempt < 4; attempt++ {
		ch := make([]byte, challengeLen)
		if _, err := rand.Read(ch); err != nil {
			return nil, err
		}
		key := string(ch)
		if _, seen := t.observedChallenges[key]; seen {
			continue
		}
		t.observedChallenges[key] = struct{}{}
		return ch, nil
	}
	return nil, ErrChallengeReused
}

// GenerateChallenge returns a fresh 32-byte random challenge that has not
// been used in this transaction.
func (t *Transaction) GenerateChallenge() ([]byte, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.generateChallengeLocked()
}

func randomByte() byte {
	var b [1]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("dhchap: crypto/rand unavailable: " + err.Error())
	}
	return b[0]
}

func requireOnce(m AuthMessage, ptype uint8) (Param, error) {
	if m.count(ptype) != 1 {
		return Param{}, ErrInvalidMessage
	}
	p, _ := m.Param(ptype)
	return p, nil
}

func requireAbsent(m AuthMessage, ptype uint8) error {
	if m.count(ptype) != 0 {
		return ErrInvalidMessage
	}
	return nil
}

func bytesToHashIDs(b []byte) []HashID {
	out := make([]HashID, len(b))
	for i, v := range b {
		out[i] = HashID(v)
	}
	return out
}

func bytesToGroupIDs(b []byte) []DHGroupID {
	out := make([]DHGroupID, len(b))
	for i, v := range b {
		out[i] = DHGroupID(v)
	}
	return out
}
