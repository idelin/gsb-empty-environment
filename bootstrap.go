package netsec

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// Identity primitives bind a credential to exactly one bootstrap window.
// A credential is accepted only when every field matches the window that is
// currently open for its network address; matching the address alone is never
// sufficient.
type (
	// InstanceID is the trusted identity reported by the instance monitor.
	InstanceID string

	// Address is the reusable network address (for example an IP address).
	Address string

	// BootNonce is the per-boot random value generated when an instance starts.
	BootNonce string

	// Generation is the strictly increasing, one-time deployment generation.
	Generation int64

	// CredentialKind distinguishes credentials that share one bootstrap window.
	CredentialKind string
)

// Sentinel errors returned by the state machine. They are wrapped with
// context, so callers must use errors.Is rather than direct comparison.
var (
	ErrEmptyInstanceID         = errors.New("bootstrap: empty instance id")
	ErrEmptyAddress            = errors.New("bootstrap: empty network address")
	ErrEmptyNonce              = errors.New("bootstrap: empty boot nonce")
	ErrInvalidGeneration       = errors.New("bootstrap: generation must be positive")
	ErrEmptyCredentialKind     = errors.New("bootstrap: empty credential kind")
	ErrEmptyMaterial           = errors.New("bootstrap: empty credential material")
	ErrNoBootstrapWindow       = errors.New("bootstrap: no open window for address")
	ErrWindowRevoked           = errors.New("bootstrap: bootstrap window has been revoked")
	ErrStaleGeneration         = errors.New("bootstrap: credential generation does not match window")
	ErrStaleNonce              = errors.New("bootstrap: credential nonce does not match window")
	ErrStaleInstance           = errors.New("bootstrap: credential instance id does not match window")
	ErrCredentialAlreadyActive = errors.New("bootstrap: credential already installed in window")
	ErrGenerationReused        = errors.New("bootstrap: deployment generation was already used for this address")
	ErrGenerationReverted      = errors.New("bootstrap: deployment generation is not greater than the current one")
	ErrInstallFailed           = errors.New("bootstrap: credential installation failed")
)

// Credential is an identity file that must be installed while a matching
// bootstrap window is open.
type Credential struct {
	Kind       CredentialKind
	InstanceID InstanceID
	Address    Address
	Nonce      BootNonce
	Generation Generation
	Material   []byte
}

func (c Credential) validate() error {
	switch {
	case strings.TrimSpace(string(c.Kind)) == "":
		return ErrEmptyCredentialKind
	case strings.TrimSpace(string(c.InstanceID)) == "":
		return ErrEmptyInstanceID
	case strings.TrimSpace(string(c.Address)) == "":
		return ErrEmptyAddress
	case strings.TrimSpace(string(c.Nonce)) == "":
		return ErrEmptyNonce
	case c.Generation <= 0:
		return ErrInvalidGeneration
	case len(c.Material) == 0:
		return ErrEmptyMaterial
	}
	return nil
}

// bindingKey uniquely identifies a credential inside one bootstrap window.
func (c Credential) bindingKey() string {
	return strings.Join([]string{
		string(c.Kind),
		string(c.InstanceID),
		string(c.Address),
		string(c.Nonce),
		strconv.FormatInt(int64(c.Generation), 10),
	}, "\x00")
}

// CredentialSink is the running environment that actually holds installed
// credentials. Implementations must make Stage side-effect free with respect
// to the running environment: staged material is invisible until Commit, and
// Abort removes every trace, including after a failed Commit.
type CredentialSink interface {
	// Purge removes every credential previously installed for addr. It runs
	// before a new instance takes over a reused network address.
	Purge(addr Address) error

	// Stage prepares cred for installation without exposing it to the running
	// environment. The returned handle must be committed or aborted.
	Stage(cred Credential) (StagedCredential, error)
}

// StagedCredential is a staged install produced by CredentialSink.Stage.
type StagedCredential interface {
	// Commit publishes the credential to the running environment.
	Commit() error

	// Abort discards the staged credential. It must be idempotent and leave
	// no credential material in the running environment.
	Abort() error
}

// Decision records why a credential delivery was accepted or refused, so the
// bootstrap host can audit late and replayed files.
type Decision int

const (
	// DecisionInstalled means the credential matched the open window and was
	// committed to the running environment.
	DecisionInstalled Decision = iota

	// DecisionAlreadyActive means an identical credential was already installed.
	DecisionAlreadyActive

	// DecisionStaleGeneration means the generation belongs to a retired window.
	DecisionStaleGeneration

	// DecisionStaleNonce means the boot nonce does not match the open window.
	DecisionStaleNonce

	// DecisionStaleInstance means the instance id does not match the open window.
	DecisionStaleInstance

	// DecisionRevokedWindow means the window was replaced by a newer instance.
	DecisionRevokedWindow

	// DecisionNoWindow means no instance is currently booting at that address.
	DecisionNoWindow

	// DecisionRejected means the credential was malformed or could not stage.
	DecisionRejected

	// DecisionInstallFailed means Commit failed and all material was rolled back.
	DecisionInstallFailed
)

// String returns a stable audit label for the decision.
func (d Decision) String() string {
	switch d {
	case DecisionInstalled:
		return "installed"
	case DecisionAlreadyActive:
		return "already_active"
	case DecisionStaleGeneration:
		return "stale_generation"
	case DecisionStaleNonce:
		return "stale_nonce"
	case DecisionStaleInstance:
		return "stale_instance"
	case DecisionRevokedWindow:
		return "revoked_window"
	case DecisionNoWindow:
		return "no_window"
	case DecisionRejected:
		return "rejected"
	case DecisionInstallFailed:
		return "install_failed"
	default:
		return "unknown"
	}
}

// WindowState describes the lifecycle of a bootstrap window.
type WindowState int

const (
	// WindowOpen accepts credentials that match every binding field.
	WindowOpen WindowState = iota

	// WindowRevoked is a retired window that refuses every credential.
	WindowRevoked
)

func (s WindowState) String() string {
	switch s {
	case WindowOpen:
		return "open"
	case WindowRevoked:
		return "revoked"
	default:
		return "unknown"
	}
}

// Window is a single bootstrap opportunity for one instance boot. It is open
// until another instance boots at the same address, at which point it is
// revoked and retained only so that late files can be recognized and refused.
type Window struct {
	InstanceID InstanceID
	Address    Address
	Nonce      BootNonce
	Generation Generation
	state      WindowState

	// committed holds binding keys of credentials already installed, giving
	// at-least-once file delivery idempotent semantics.
	committed map[string]struct{}
}

// ActiveWindow is an immutable snapshot of the window open for an address.
type ActiveWindow struct {
	InstanceID InstanceID
	Address    Address
	Nonce      BootNonce
	Generation Generation
	Installed  int
}

// Supervisor runs the bootstrap credential state machine. All methods are
// safe for concurrent use.
type Supervisor struct {
	mu sync.Mutex

	sink CredentialSink

	// windows holds the single open window per network address.
	windows map[Address]*Window

	// retired retains revoked windows keyed by generation, so that a late
	// file from a previous instance is refused even after address reuse.
	retired map[Address]map[Generation]*Window
}

// NewSupervisor creates a state machine that installs credentials through
// sink.
func NewSupervisor(sink CredentialSink) *Supervisor {
	if sink == nil {
		panic("netsec: NewSupervisor requires a non-nil CredentialSink")
	}
	return &Supervisor{
		sink:    sink,
		windows: make(map[Address]*Window),
		retired: make(map[Address]map[Generation]*Window),
	}
}

// NewBootNonce returns 128 bits of fresh randomness identifying one instance
// boot. Every new instance gets a nonce that cannot match a previous window.
func NewBootNonce() (BootNonce, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("bootstrap: generate boot nonce: %w", err)
	}
	return BootNonce(hex.EncodeToString(raw[:])), nil
}

// BootInstance opens a new bootstrap window when an instance starts. Any
// previously open window for addr is revoked immediately, the running
// environment is purged, and generations must be strictly increasing and
// never reused for that address.
func (s *Supervisor) BootInstance(id InstanceID, addr Address, nonce BootNonce, gen Generation) error {
	if err := validateBoot(id, addr, nonce, gen); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if set, ok := s.retired[addr]; ok {
		if _, used := set[gen]; used {
			return fmt.Errorf("%w: generation %d for address %s", ErrGenerationReused, gen, addr)
		}
		var highest Generation
		for oldGen := range set {
			if oldGen > highest {
				highest = oldGen
			}
		}
		if gen <= highest {
			return fmt.Errorf("%w: address %s generation %d <= retired generation %d",
				ErrGenerationReverted, addr, gen, highest)
		}
	}

	if active, ok := s.windows[addr]; ok && active.Generation >= gen {
		return fmt.Errorf("%w: address %s generation %d <= active generation %d",
			ErrGenerationReverted, addr, gen, active.Generation)
	}

	// Purge first: if the running environment cannot be cleaned, refuse the
	// boot and leave the previous window intact rather than mix identities.
	if err := s.sink.Purge(addr); err != nil {
		return fmt.Errorf("bootstrap: purge runtime for address %s: %w", addr, err)
	}

	if previous, ok := s.windows[addr]; ok {
		previous.state = WindowRevoked
		s.retireLocked(addr, previous)
		delete(s.windows, addr)
	}

	s.windows[addr] = &Window{
		InstanceID: id,
		Address:    addr,
		Nonce:      nonce,
		Generation: gen,
		state:      WindowOpen,
		committed:  make(map[string]struct{}),
	}
	return nil
}

func (s *Supervisor) retireLocked(addr Address, w *Window) {
	set, ok := s.retired[addr]
	if !ok {
		set = make(map[Generation]*Window)
		s.retired[addr] = set
	}
	set[w.Generation] = w
}

func validateBoot(id InstanceID, addr Address, nonce BootNonce, gen Generation) error {
	switch {
	case strings.TrimSpace(string(id)) == "":
		return ErrEmptyInstanceID
	case strings.TrimSpace(string(addr)) == "":
		return ErrEmptyAddress
	case strings.TrimSpace(string(nonce)) == "":
		return ErrEmptyNonce
	case gen <= 0:
		return ErrInvalidGeneration
	}
	return nil
}

// HandleCredential delivers one identity file. The credential reaches the
// running environment only when its instance id, boot nonce and generation
// all match the window currently open for its address. The sink is never
// invoked for stale, replayed or windowless credentials.
func (s *Supervisor) HandleCredential(cred Credential) (Decision, error) {
	if err := cred.validate(); err != nil {
		return DecisionRejected, err
	}

	// The lock covers the sink interaction as well, so a concurrent
	// BootInstance cannot purge and replace the runtime between validation
	// and commit: a stale credential can never land in a new instance.
	s.mu.Lock()
	defer s.mu.Unlock()

	window, ok := s.windows[cred.Address]
	if !ok {
		return DecisionNoWindow, fmt.Errorf("%w: address %s", ErrNoBootstrapWindow, cred.Address)
	}
	if window.state != WindowOpen {
		return DecisionRevokedWindow, fmt.Errorf("%w: address %s generation %d",
			ErrWindowRevoked, cred.Address, window.Generation)
	}

	if window.Generation != cred.Generation {
		return DecisionStaleGeneration, fmt.Errorf("%w: window generation %d, credential generation %d",
			ErrStaleGeneration, window.Generation, cred.Generation)
	}
	if subtle.ConstantTimeCompare([]byte(window.Nonce), []byte(cred.Nonce)) != 1 {
		return DecisionStaleNonce, fmt.Errorf("%w: address %s", ErrStaleNonce, cred.Address)
	}
	if subtle.ConstantTimeCompare([]byte(window.InstanceID), []byte(cred.InstanceID)) != 1 {
		return DecisionStaleInstance, fmt.Errorf("%w: address %s", ErrStaleInstance, cred.Address)
	}

	key := cred.bindingKey()
	if _, already := window.committed[key]; already {
		return DecisionAlreadyActive, fmt.Errorf("%w: %s", ErrCredentialAlreadyActive, key)
	}

	staged, err := s.sink.Stage(cred)
	if err != nil {
		return DecisionRejected, fmt.Errorf("bootstrap: stage credential: %w", err)
	}
	if err := staged.Commit(); err != nil {
		// Abort is contractually required to remove every trace, so a failed
		// commit leaves nothing in the running environment.
		if abortErr := staged.Abort(); abortErr != nil {
			return DecisionInstallFailed, fmt.Errorf("%w: commit: %v; abort: %w",
				ErrInstallFailed, err, abortErr)
		}
		return DecisionInstallFailed, fmt.Errorf("%w: %w", ErrInstallFailed, err)
	}

	window.committed[key] = struct{}{}
	return DecisionInstalled, nil
}

// ActiveWindow returns a snapshot of the window currently open for addr.
func (s *Supervisor) ActiveWindow(addr Address) (ActiveWindow, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	window, ok := s.windows[addr]
	if !ok {
		return ActiveWindow{}, false
	}
	return ActiveWindow{
		InstanceID: window.InstanceID,
		Address:    window.Address,
		Nonce:      window.Nonce,
		Generation: window.Generation,
		Installed:  len(window.committed),
	}, true
}
