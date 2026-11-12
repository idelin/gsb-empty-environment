// Package scope simulates Landlock ABI 6 scoping (signal and abstract Unix
// socket restrictions) for the Go sandbox launcher: ruleset compilation
// against a manifest, and signal-delivery permission checks.
package scope

import (
	"errors"
	"fmt"
)

// MinScopeABI is the first Landlock ABI version that supports scoping
// restrictions (signal and abstract Unix socket).
const MinScopeABI = 6

// ErrUnsupportedABI is returned when a manifest requests scoping features
// that the selected ABI version cannot enforce. Compilation refuses to
// start instead of silently downgrading the ruleset.
var ErrUnsupportedABI = errors.New("scope: ABI too old for requested scoping")

// ErrCrossScope is returned when a signal (or abstract Unix socket connect)
// crosses the sender's domain scope: the target lives in an ancestor
// domain, an unrelated domain, or outside any domain.
var ErrCrossScope = errors.New("scope: target outside sender domain")

// Signal is a simulated Linux signal number.
type Signal int

const (
	SIGKILL Signal = 9
	SIGURG  Signal = 23
	SIGIO   Signal = 29
)

// DeliveryPath identifies how a signal reaches its target.
type DeliveryPath int

const (
	PathKill DeliveryPath = iota
	PathTgkill
	PathPidfdSendSignal
	// PathFSetOwnSIGIO and PathFSetOwnSIGURG are delivered asynchronously
	// via an fd owner installed with fcntl(F_SETOWN); they are authorized
	// against the domain recorded at F_SETOWN time, not at delivery time.
	PathFSetOwnSIGIO
	PathFSetOwnSIGURG
)

func (p DeliveryPath) fsetown() bool {
	return p == PathFSetOwnSIGIO || p == PathFSetOwnSIGURG
}

// Manifest describes the ruleset the launcher was asked to compile.
type Manifest struct {
	// ABI is the Landlock ABI version in use (1-based).
	ABI int
	// ScopeSignal requests LANDLOCK_SCOPE_SIGNAL.
	ScopeSignal bool
	// ScopeAbstractUnixSocket requests LANDLOCK_SCOPE_ABSTRACT_UNIX_SOCKET.
	ScopeAbstractUnixSocket bool
}

// Ruleset is a compiled, immutable ruleset.
type Ruleset struct {
	abi                 int
	scopeSignal         bool
	scopeAbstractSocket bool
}

// Compile validates the manifest against the ABI and builds the ruleset.
// If the ABI is below MinScopeABI while the manifest requests any scoping,
// Compile returns an error wrapping ErrUnsupportedABI and no ruleset;
// it never downgrades by silently dropping the requested scopes.
func Compile(m Manifest) (*Ruleset, error) {
	if m.ABI < 1 {
		return nil, fmt.Errorf("scope: invalid ABI version %d", m.ABI)
	}
	if m.ScopeSignal && m.ABI < MinScopeABI {
		return nil, fmt.Errorf("scope: signal scoping requires ABI >= %d, got %d: %w",
			MinScopeABI, m.ABI, ErrUnsupportedABI)
	}
	if m.ScopeAbstractUnixSocket && m.ABI < MinScopeABI {
		return nil, fmt.Errorf("scope: abstract Unix socket scoping requires ABI >= %d, got %d: %w",
			MinScopeABI, m.ABI, ErrUnsupportedABI)
	}
	return &Ruleset{
		abi:                 m.ABI,
		scopeSignal:         m.ScopeSignal,
		scopeAbstractSocket: m.ScopeAbstractUnixSocket,
	}, nil
}

// SignalScoped reports whether the ruleset enforces signal scoping.
func (r *Ruleset) SignalScoped() bool { return r.scopeSignal }

// AbstractUnixScoped reports whether the ruleset enforces abstract Unix
// socket scoping.
func (r *Ruleset) AbstractUnixScoped() bool { return r.scopeAbstractSocket }

// Domain is a node in the nestable domain hierarchy. A child domain is
// created by a process restricting itself further; domains only stack.
type Domain struct {
	name   string
	parent *Domain
}

// NewDomain creates a domain nested under parent (nil for a top-level
// domain).
func NewDomain(name string, parent *Domain) *Domain {
	return &Domain{name: name, parent: parent}
}

// Contains reports whether other is the same domain as d or a descendant
// of d. A nil other (unscoped process) is outside every domain.
func (d *Domain) Contains(other *Domain) bool {
	for cur := other; cur != nil; cur = cur.parent {
		if cur == d {
			return true
		}
	}
	return false
}

// Process is a simulated process carrying its current domain.
type Process struct {
	PID    int
	Domain *Domain
}

// checkScoped enforces the scoping rule: a scoped sender may only reach
// targets in its own domain or a descendant domain. An unscoped sender
// (nil domain) is unrestricted.
func (r *Ruleset) checkScoped(senderDomain *Domain, target *Process, what string) error {
	if senderDomain == nil {
		return nil
	}
	if target == nil {
		return fmt.Errorf("scope: %s has no target: %w", what, ErrCrossScope)
	}
	if senderDomain.Contains(target.Domain) {
		return nil
	}
	return fmt.Errorf("scope: %s to pid %d crosses domain scope: %w",
		what, target.PID, ErrCrossScope)
}

// SendSignal simulates a synchronous signal delivery via kill, tgkill or
// pidfd_send_signal. The check uses the sender's current domain.
func (r *Ruleset) SendSignal(sender, target *Process, sig Signal, path DeliveryPath) error {
	if path.fsetown() {
		return fmt.Errorf("scope: signal %d via F_SETOWN must go through FSetOwn/Deliver", sig)
	}
	if !r.scopeSignal {
		return nil
	}
	if sender == nil {
		return fmt.Errorf("scope: signal %d has no sender: %w", sig, ErrCrossScope)
	}
	return r.checkScoped(sender.Domain, target, fmt.Sprintf("signal %d", sig))
}

// FDOwner records the authorization snapshot taken by fcntl(F_SETOWN):
// the caller's domain at set time and the process that will receive
// SIGIO/SIGURG on the fd.
type FDOwner struct {
	owner  *Process
	domain *Domain
}

// FSetOwn simulates fcntl(F_SETOWN): it authorizes the caller against the
// future signal recipient now and records the caller's current domain.
// Later domain changes of the caller do not affect the record.
func (r *Ruleset) FSetOwn(caller, owner *Process) (*FDOwner, error) {
	if caller == nil || owner == nil {
		return nil, fmt.Errorf("scope: F_SETOWN needs caller and owner: %w", ErrCrossScope)
	}
	if r.scopeSignal {
		if err := r.checkScoped(caller.Domain, owner, "F_SETOWN"); err != nil {
			return nil, err
		}
	}
	return &FDOwner{owner: owner, domain: caller.Domain}, nil
}

// Deliver simulates asynchronous SIGIO/SIGURG delivery to the fd owner.
// Authorization uses the domain recorded at F_SETOWN time, never the
// caller's domain at delivery time.
func (r *Ruleset) Deliver(fd *FDOwner, sig Signal, path DeliveryPath) error {
	if !path.fsetown() {
		return fmt.Errorf("scope: Deliver only handles F_SETOWN-triggered signals")
	}
	if fd == nil {
		return fmt.Errorf("scope: no F_SETOWN record: %w", ErrCrossScope)
	}
	if !r.scopeSignal {
		return nil
	}
	return r.checkScoped(fd.domain, fd.owner, fmt.Sprintf("signal %d", sig))
}

// CheckAbstractUnixConnect simulates connecting to an abstract Unix socket
// created by creator: a scoped connector may only reach sockets created in
// its own domain or a descendant domain.
func (r *Ruleset) CheckAbstractUnixConnect(connector, creator *Process) error {
	if !r.scopeAbstractSocket {
		return nil
	}
	if connector == nil {
		return fmt.Errorf("scope: abstract Unix connect has no connector: %w", ErrCrossScope)
	}
	return r.checkScoped(connector.Domain, creator, "abstract Unix connect")
}
