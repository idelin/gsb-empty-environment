package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrInvalidState        = errors.New("netsec: operation not allowed in current state")
	ErrChallengeMismatch   = errors.New("netsec: authorization challenge does not match boot challenge")
	ErrWindowExpired       = errors.New("netsec: authorization window expired")
	ErrWindowNotExtendable = errors.New("netsec: authorization window cannot be extended")
)

// Clock abstracts time so tests can drive window expiry with a fake clock.
type Clock interface {
	Now() time.Time
}

// Driver abstracts the hardware endpoints the host gates. Implementations
// must treat a failed call as "state unchanged".
type Driver interface {
	ShutdownSensitiveDMA() error
	RestoreSensitiveDMA() error
	OpenDebug() error
	CloseDebug() error
}

// State is the firmware host mode state machine.
type State int

const (
	// StateNormal: sensitive tasks run, debug interface is closed.
	StateNormal State = iota
	// StateEnteringMaintenance: sensitive DMA shut down, awaiting the
	// zeroization confirmation before the debug interface may open.
	StateEnteringMaintenance
	// StateMaintenance: debug interface open, sensitive DMA off.
	StateMaintenance
	// StateLeavingMaintenance: debug close requested, sensitive tasks
	// stay disabled until the close is confirmed.
	StateLeavingMaintenance
	// StateFault: a driver failure left the host unable to prove a safe
	// configuration. Neither permission class is enabled; recovery
	// requires reboot.
	StateFault
)

func (s State) String() string {
	switch s {
	case StateNormal:
		return "Normal"
	case StateEnteringMaintenance:
		return "EnteringMaintenance"
	case StateMaintenance:
		return "Maintenance"
	case StateLeavingMaintenance:
		return "LeavingMaintenance"
	case StateFault:
		return "Fault"
	default:
		return "Unknown"
	}
}

// Authorization binds the physical approval to the boot challenge and a
// fixed deadline. The window is valid while Now() is strictly before the
// deadline and can never be extended.
type Authorization struct {
	challenge []byte
	deadline  time.Time
}

// Deadline returns the non-extendable end of the authorization window.
func (a Authorization) Deadline() time.Time { return a.deadline }

// Extend always fails: a physical authorization window cannot be prolonged.
func (a Authorization) Extend(time.Duration) (Authorization, error) {
	return Authorization{}, ErrWindowNotExtendable
}

// Host is the firmware host mode state machine. It guarantees the debug
// interface and sensitive DMA/tasks are never enabled at the same time.
type Host struct {
	clock         Clock
	drv           Driver
	bootChallenge []byte

	mu              sync.Mutex
	state           State
	auth            *Authorization
	debugOpen       bool
	sensitiveActive bool
}

// NewHost returns a host in StateNormal with sensitive tasks active and the
// debug interface closed. bootChallenge is regenerated at every boot and is
// what issued authorizations are bound to.
func NewHost(clk Clock, drv Driver, bootChallenge []byte) *Host {
	return &Host{
		clock:           clk,
		drv:             drv,
		bootChallenge:   append([]byte(nil), bootChallenge...),
		state:           StateNormal,
		sensitiveActive: true,
	}
}

// IssueAuthorization mints an authorization bound to the current boot
// challenge, expiring window after the current time.
func (h *Host) IssueAuthorization(window time.Duration) Authorization {
	h.mu.Lock()
	defer h.mu.Unlock()
	return Authorization{
		challenge: append([]byte(nil), h.bootChallenge...),
		deadline:  h.clock.Now().Add(window),
	}
}

// State reports the current state machine state.
func (h *Host) State() State {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state
}

// Permissions reports which permission classes the host believes are
// enabled. The safety invariant is that both are never true at once.
func (h *Host) Permissions() (debugOpen, sensitiveActive bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.debugOpen, h.sensitiveActive
}

func (h *Host) expired(a Authorization) bool {
	return !h.clock.Now().Before(a.deadline)
}

// EnterMaintenance begins a transition to maintenance mode. The sensitive
// DMA is shut down first; the debug interface only opens once the driver
// delivers the zeroization confirmation via HandleDMAZeroed.
func (h *Host) EnterMaintenance(auth Authorization) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.state != StateNormal {
		return ErrInvalidState
	}
	if !bytes.Equal(auth.challenge, h.bootChallenge) {
		return ErrChallengeMismatch
	}
	if h.expired(auth) {
		return ErrWindowExpired
	}
	if err := h.drv.ShutdownSensitiveDMA(); err != nil {
		return fmt.Errorf("netsec: shutdown sensitive DMA: %w", err)
	}
	h.sensitiveActive = false
	h.state = StateEnteringMaintenance
	h.auth = &auth
	return nil
}

// HandleDMAZeroed is the driver callback delivering the DMA zeroization
// confirmation. The debug interface opens only if the authorization window
// is still valid. Duplicate or stale confirmations are harmless no-ops.
func (h *Host) HandleDMAZeroed() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.state != StateEnteringMaintenance {
		return nil
	}
	if h.auth == nil || h.expired(*h.auth) {
		return h.abortEnteringLocked()
	}
	if err := h.drv.OpenDebug(); err != nil {
		rerr := h.restoreSensitiveLocked()
		return fmt.Errorf("netsec: open debug: %w (rollback: %v)", err, rerr)
	}
	h.debugOpen = true
	h.state = StateMaintenance
	return nil
}

// LeaveMaintenance closes the debug interface first and only then re-enables
// sensitive tasks. If the debug close cannot be confirmed, sensitive tasks
// stay disabled and the host enters StateFault.
func (h *Host) LeaveMaintenance() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.state != StateMaintenance {
		return ErrInvalidState
	}
	h.state = StateLeavingMaintenance
	if err := h.drv.CloseDebug(); err != nil {
		h.state = StateFault
		return fmt.Errorf("netsec: close debug: %w", err)
	}
	h.debugOpen = false
	if err := h.restoreSensitiveLocked(); err != nil {
		return fmt.Errorf("netsec: restore sensitive DMA: %w", err)
	}
	h.state = StateNormal
	h.auth = nil
	return nil
}

// Tick enforces the authorization deadline against the current time. It must
// be called periodically; on expiry it forces the debug interface closed and
// restores sensitive DMA, ending in StateNormal (or StateFault if the driver
// fails).
func (h *Host) Tick() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch h.state {
	case StateEnteringMaintenance:
		if h.auth == nil || h.expired(*h.auth) {
			return h.abortEnteringLocked()
		}
	case StateMaintenance:
		if h.auth != nil && h.expired(*h.auth) {
			if err := h.drv.CloseDebug(); err != nil {
				h.state = StateFault
				return fmt.Errorf("netsec: window expired, close debug: %w", err)
			}
			h.debugOpen = false
			if err := h.restoreSensitiveLocked(); err != nil {
				return fmt.Errorf("netsec: window expired, restore sensitive DMA: %w", err)
			}
			h.state = StateNormal
			h.auth = nil
			return ErrWindowExpired
		}
	}
	return nil
}

// abortEnteringLocked cancels a pending transition whose window expired:
// debug was never opened, so only sensitive DMA needs restoring.
func (h *Host) abortEnteringLocked() error {
	err := h.restoreSensitiveLocked()
	return fmt.Errorf("%w (rollback: %v)", ErrWindowExpired, err)
}

// restoreSensitiveLocked re-enables sensitive DMA. On failure the host moves
// to StateFault with neither permission class enabled.
func (h *Host) restoreSensitiveLocked() error {
	if err := h.drv.RestoreSensitiveDMA(); err != nil {
		h.state = StateFault
		return err
	}
	h.sensitiveActive = true
	if h.state != StateFault {
		h.state = StateNormal
	}
	h.auth = nil
	return nil
}
