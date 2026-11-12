package netsec

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"sync"
)

const (
	// DefaultMaxPINAttempts is used when Config leaves MaxPINAttempts unset.
	DefaultMaxPINAttempts = 5
	// DefaultMaxPUKAttempts is used when Config leaves MaxPUKAttempts unset.
	DefaultMaxPUKAttempts = 5
	// MaxRetryCounter is the largest value representable by the ISO 7816
	// 63Cx retry status word.
	MaxRetryCounter = 15
)

// ISO 7816-4 status words used by the simulator.
const (
	SWOK                     uint16 = 0x9000
	SWWrongData              uint16 = 0x6A80
	SWUnsupportedInstruction uint16 = 0x6D00
	SWAuthenticationBlocked  uint16 = 0x6983
	SWInternalError          uint16 = 0x6F00
	SWRetryCounterBase       uint16 = 0x63C0
)

// APDU instruction bytes understood by HandleAPDU.
const (
	INSVerifyPIN         byte = 0x20
	INSResetRetryCounter byte = 0x2C
)

// ErrInvalidConfig reports an unusable reader configuration.
var ErrInvalidConfig = errors.New("netsec: invalid PIN handler config")

// Config describes the card secrets and retry budgets.
//
// PIN is used only when the NVM is blank. On a reboot the persisted PIN hash
// is authoritative, which is required for a PUK-changed PIN to survive.
// PUK is a fixed card secret supplied by the host on every boot.
type Config struct {
	PIN            string
	PUK            string
	MaxPINAttempts int
	MaxPUKAttempts int
}

// Response is the result of one APDU. Retry counters are the durable values
// after the command.
type Response struct {
	SW         uint16
	PINRetries int
	PUKRetries int
}

// OK reports whether the command succeeded.
func (r Response) OK() bool { return r.SW == SWOK }

// APDU is the subset of an ISO 7816 command needed by the simulator.
//
// For INSVerifyPIN, Data is the PIN. For INSResetRetryCounter, Data is the
// configured PUK followed by the new PIN (PUK || new PIN).
type APDU struct {
	CLA  byte
	INS  byte
	P1   byte
	P2   byte
	Data []byte
}

// PINHandler serialises PIN and PUK verification and updates durable retry
// counters.
//
// Security invariant: every attempt decrements its counter and commits that
// decrement before the secret is compared. A successful comparison is the
// only path that restores a counter to its maximum. Therefore a power cut at
// any write point can lose availability, but it cannot grant an extra
// comparison.
type PINHandler struct {
	mu      sync.Mutex
	journal *journal

	maxPIN  uint8
	maxPUK  uint8
	puk     []byte
	pukHash [32]byte
}

// NewPINHandler opens (or factory-initialises) a card backed by nvm.
func NewPINHandler(nvm SlotNVM, cfg Config) (*PINHandler, error) {
	if nvm == nil {
		return nil, fmt.Errorf("%w: nil NVM", ErrInvalidConfig)
	}
	maxPIN, maxPUK, err := normalizeLimits(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.PUK == "" {
		return nil, fmt.Errorf("%w: empty PUK", ErrInvalidConfig)
	}

	j := newJournal(nvm)
	state, present, err := j.load()
	if err != nil {
		return nil, err
	}
	if !present {
		if cfg.PIN == "" {
			return nil, fmt.Errorf("%w: empty initial PIN", ErrInvalidConfig)
		}
		var salt [16]byte
		if _, err := rand.Read(salt[:]); err != nil {
			return nil, fmt.Errorf("netsec: generate PIN salt: %w", err)
		}
		state = persistedState{
			PINRetries: maxPIN,
			PUKRetries: maxPUK,
			Salt:       salt,
			PINHash:    pinDigest(salt, []byte(cfg.PIN)),
		}
		state, err = j.commit(state)
		if err != nil {
			return nil, err
		}
	}

	h := &PINHandler{
		journal: j,
		maxPIN:  maxPIN,
		maxPUK:  maxPUK,
		puk:     []byte(cfg.PUK),
		pukHash: pukDigest([]byte(cfg.PUK)),
	}
	h.normalizeLimits(&state)
	return h, nil
}

func normalizeLimits(cfg Config) (uint8, uint8, error) {
	maxPIN := cfg.MaxPINAttempts
	if maxPIN <= 0 {
		maxPIN = DefaultMaxPINAttempts
	}
	maxPUK := cfg.MaxPUKAttempts
	if maxPUK <= 0 {
		maxPUK = DefaultMaxPUKAttempts
	}
	if maxPIN > MaxRetryCounter {
		return 0, 0, fmt.Errorf("%w: MaxPINAttempts %d exceeds %d", ErrInvalidConfig, maxPIN, MaxRetryCounter)
	}
	if maxPUK > MaxRetryCounter {
		return 0, 0, fmt.Errorf("%w: MaxPUKAttempts %d exceeds %d", ErrInvalidConfig, maxPUK, MaxRetryCounter)
	}
	return uint8(maxPIN), uint8(maxPUK), nil
}

func (h *PINHandler) normalizeLimits(state *persistedState) {
	if state.PINRetries > h.maxPIN {
		state.PINRetries = h.maxPIN
	}
	if state.PUKRetries > h.maxPUK {
		state.PUKRetries = h.maxPUK
	}
}

func (h *PINHandler) loadState() (persistedState, error) {
	state, present, err := h.journal.load()
	if err != nil {
		return persistedState{}, err
	}
	if !present {
		return persistedState{}, ErrCorruptState
	}
	h.normalizeLimits(&state)
	return state, nil
}

func (h *PINHandler) result(state persistedState, sw uint16) Response {
	return Response{
		SW:         sw,
		PINRetries: int(state.PINRetries),
		PUKRetries: int(state.PUKRetries),
	}
}

func errorResponse(err error) (Response, error) {
	return Response{SW: SWInternalError}, err
}

// VerifyPIN verifies a PIN. It decrements and commits the PIN retry counter
// first, then compares in constant time. A correct PIN restores the counter to
// MaxPINAttempts. A card whose PIN counter is zero rejects every PIN, including
// the correct one, and can only be recovered with a PUK reset.
func (h *PINHandler) VerifyPIN(pin string) (Response, error) {
	return h.VerifyPINBytes([]byte(pin))
}

// VerifyPINBytes is VerifyPIN for raw APDU data.
func (h *PINHandler) VerifyPINBytes(pin []byte) (Response, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	state, err := h.loadState()
	if err != nil {
		return errorResponse(err)
	}
	if state.PINRetries == 0 {
		return h.result(state, SWAuthenticationBlocked), nil
	}

	pinHash := pinDigest(state.Salt, pin)
	state.PINRetries--
	state, err = h.journal.commit(state)
	if err != nil {
		return errorResponse(err)
	}

	if subtle.ConstantTimeCompare(state.PINHash[:], pinHash[:]) == 1 {
		state.PINRetries = h.maxPIN
		state, err = h.journal.commit(state)
		if err != nil {
			return errorResponse(err)
		}
		return h.result(state, SWOK), nil
	}
	return h.result(state, retryStatus(state.PINRetries)), nil
}

// ResetPIN verifies the PUK and replaces the PIN. The PUK has its own durable
// retry counter which is decremented before comparison. A successful PUK entry
// restores both the PIN and PUK budgets.
func (h *PINHandler) ResetPIN(puk, newPIN string) (Response, error) {
	return h.ResetPINBytes([]byte(puk), []byte(newPIN))
}

// ResetPINBytes is ResetPIN for raw APDU data.
func (h *PINHandler) ResetPINBytes(puk, newPIN []byte) (Response, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	state, err := h.loadState()
	if err != nil {
		return errorResponse(err)
	}
	if len(newPIN) == 0 {
		return h.result(state, SWWrongData), nil
	}
	if state.PUKRetries == 0 {
		return h.result(state, SWAuthenticationBlocked), nil
	}

	pukHash := pukDigest(puk)
	state.PUKRetries--
	state, err = h.journal.commit(state)
	if err != nil {
		return errorResponse(err)
	}

	if subtle.ConstantTimeCompare(h.pukHash[:], pukHash[:]) != 1 {
		return h.result(state, retryStatus(state.PUKRetries)), nil
	}

	state.PINHash = pinDigest(state.Salt, newPIN)
	state.PINRetries = h.maxPIN
	state.PUKRetries = h.maxPUK
	state, err = h.journal.commit(state)
	if err != nil {
		return errorResponse(err)
	}
	return h.result(state, SWOK), nil
}

// HandleAPDU dispatches a PIN verify or PUK reset command.
func (h *PINHandler) HandleAPDU(apdu APDU) (Response, error) {
	switch apdu.INS {
	case INSVerifyPIN:
		return h.VerifyPINBytes(apdu.Data)
	case INSResetRetryCounter:
		if len(apdu.Data) <= len(h.puk) {
			h.mu.Lock()
			state, err := h.loadState()
			if err != nil {
				h.mu.Unlock()
				return errorResponse(err)
			}
			resp := h.result(state, SWWrongData)
			h.mu.Unlock()
			return resp, nil
		}
		return h.ResetPINBytes(apdu.Data[:len(h.puk)], apdu.Data[len(h.puk):])
	default:
		return Response{SW: SWUnsupportedInstruction}, nil
	}
}

// RetryCounters returns the durable remaining PIN and PUK attempts.
func (h *PINHandler) RetryCounters() (pinRetries, pukRetries int, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	state, err := h.loadState()
	if err != nil {
		return 0, 0, err
	}
	return int(state.PINRetries), int(state.PUKRetries), nil
}

func retryStatus(remaining uint8) uint16 {
	if remaining > MaxRetryCounter {
		remaining = MaxRetryCounter
	}
	return SWRetryCounterBase | uint16(remaining)
}

func pinDigest(salt [16]byte, pin []byte) [32]byte {
	hash := sha256.New()
	hash.Write([]byte("netsec-pin\x00"))
	hash.Write(salt[:])
	hash.Write(pin)
	var out [32]byte
	copy(out[:], hash.Sum(nil))
	return out
}

func pukDigest(puk []byte) [32]byte {
	hash := sha256.New()
	hash.Write([]byte("netsec-puk\x00"))
	hash.Write(puk)
	var out [32]byte
	copy(out[:], hash.Sum(nil))
	return out
}
