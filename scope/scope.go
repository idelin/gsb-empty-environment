// Package scope simulates Landlock ABI 6 scoping decisions for a sandbox
// launcher: ruleset compilation against the kernel ABI, signal delivery
// checks across domain hierarchies, and F_SETOWN authorization snapshots.
package scope

import (
	"errors"
	"fmt"
)

// ABI is a Landlock ABI version as reported by landlock_get_abi_version(2).
type ABI uint

// MinScopedABI is the first ABI that supports scoping a ruleset to the
// domain hierarchy (signal and abstract Unix socket restrictions).
const MinScopedABI ABI = 6

// Signal is a signal number relevant to scoping checks.
type Signal int

const (
	SIGKILL Signal = 9
	SIGIO   Signal = 29
	SIGURG  Signal = 23
)

// Manifest describes what the launcher configuration asks the ruleset to
// enforce.
type Manifest struct {
	ScopeSignal         bool
	ScopeAbstractSocket bool
}

// RequiresScope reports whether the manifest needs ABI >= MinScopedABI.
func (m Manifest) RequiresScope() bool {
	return m.ScopeSignal || m.ScopeAbstractSocket
}

// ErrABITooOld is returned when the manifest requires scoping but the
// kernel ABI is older than MinScopedABI. The launcher must refuse to
// start; silently dropping the scope flags is not allowed.
var ErrABITooOld = errors.New("landlock: ABI too old for requested scopes")

// Ruleset is a compiled, launch-ready configuration.
type Ruleset struct {
	abi      ABI
	manifest Manifest
}

// Compile validates the manifest against the kernel ABI. If the manifest
// requires scoping and the ABI predates MinScopedABI, Compile fails and
// returns no ruleset: the launcher must abort rather than downgrade.
func Compile(m Manifest, abi ABI) (*Ruleset, error) {
	if m.RequiresScope() && abi < MinScopedABI {
		return nil, fmt.Errorf("%w: manifest requires scopes (signal=%t, abstract_socket=%t) but ABI is %d, need >= %d; refusing to launch without downgrade",
			ErrABITooOld, m.ScopeSignal, m.ScopeAbstractSocket, abi, MinScopedABI)
	}
	return &Ruleset{abi: abi, manifest: m}, nil
}

// ABI returns the ABI the ruleset was compiled against.
func (r *Ruleset) ABI() ABI { return r.abi }

// SignalScoped reports whether signal scoping is enforced.
func (r *Ruleset) SignalScoped() bool { return r.manifest.ScopeSignal }

// AbstractSocketScoped reports whether abstract socket scoping is enforced.
func (r *Ruleset) AbstractSocketScoped() bool { return r.manifest.ScopeAbstractSocket }

// Domain is one layer of the Landlock domain hierarchy. A domain nests
// inside its parent; the root domain has a nil parent. A nil *Domain
// represents an unrestricted (unsandboxed) process.
type Domain struct {
	name   string
	parent *Domain
}

// NewDomain creates a domain nested under parent (nil for the root).
func NewDomain(name string, parent *Domain) *Domain {
	return &Domain{name: name, parent: parent}
}

// Name returns the domain's name.
func (d *Domain) Name() string { return d.name }

// Contains reports whether other is d itself or a descendant of d.
// A nil other (unrestricted process) is contained in no domain.
func (d *Domain) Contains(other *Domain) bool {
	for cur := other; cur != nil; cur = cur.parent {
		if cur == d {
			return true
		}
	}
	return false
}

// Process is a sender or target subject to scoping checks.
type Process struct {
	PID    int
	Name   string
	Domain *Domain // nil means unrestricted
}

// Path identifies how a signal reaches its target.
type Path int

const (
	Kill Path = iota
	Tgkill
	PidfdSendSignal
	// SetOwnSIGIO and SetOwnSIGURG are deferred deliveries arranged by an
	// earlier fcntl(F_SETOWN); the authorization was captured at set time.
	SetOwnSIGIO
	SetOwnSIGURG
)

func (p Path) String() string {
	switch p {
	case Kill:
		return "kill"
	case Tgkill:
		return "tgkill"
	case PidfdSendSignal:
		return "pidfd_send_signal"
	case SetOwnSIGIO:
		return "fcntl(F_SETOWN)/SIGIO"
	case SetOwnSIGURG:
		return "fcntl(F_SETOWN)/SIGURG"
	}
	return "unknown"
}

// ErrCrossScope is returned when a scoped process tries to reach a target
// outside its domain and that domain's descendants.
var ErrCrossScope = errors.New("landlock: target outside signal scope")

// inScope reports whether sender may reach target under signal scoping:
// the target must live in the sender's domain or a descendant of it.
// Unrestricted senders (nil domain) are never blocked; a scoped sender
// can never reach an unrestricted (nil domain) target.
func inScope(sender, target *Process) bool {
	if sender.Domain == nil {
		return true
	}
	return sender.Domain.Contains(target.Domain)
}

// CheckSignal decides whether sender may deliver sig to target via path.
// Same-domain and descendant-domain targets are allowed; ancestor and
// unrelated domains are denied.
func (r *Ruleset) CheckSignal(sender, target *Process, sig Signal, path Path) error {
	if !r.manifest.ScopeSignal {
		return nil
	}
	if inScope(sender, target) {
		return nil
	}
	return fmt.Errorf("%w: %s: %s(pid=%d, domain=%s) cannot send signal %d to %s(pid=%d, domain=%s)",
		ErrCrossScope, path, sender.Name, sender.PID, domainName(sender.Domain),
		sig, target.Name, target.PID, domainName(target.Domain))
}

// CheckAbstractConnect decides whether a scoped process may connect to an
// abstract Unix socket created in socketDomain (nil if created outside any
// domain).
func (r *Ruleset) CheckAbstractConnect(connector *Process, socketDomain *Domain) error {
	if !r.manifest.ScopeAbstractSocket {
		return nil
	}
	if connector.Domain == nil || connector.Domain.Contains(socketDomain) {
		return nil
	}
	return fmt.Errorf("%w: %s(pid=%d, domain=%s) cannot connect to abstract socket from domain=%s",
		ErrCrossScope, connector.Name, connector.PID, domainName(connector.Domain), domainName(socketDomain))
}

// Grant is an F_SETOWN authorization snapshot. The domain check runs once,
// when fcntl(F_SETOWN) executes; later SIGIO/SIGURG deliveries rely on the
// grant and are not re-evaluated against the then-current domains.
type Grant struct {
	sig       Signal
	ownerPID  int
	setDomain *Domain
	ownDomain *Domain
}

// SetOwn simulates fcntl(F_SETOWN): setter arranges for owner to receive
// sig (SIGIO or SIGURG) later. The scope check is performed now, against
// the current domains, and frozen into the returned Grant.
func (r *Ruleset) SetOwn(setter, owner *Process, sig Signal) (*Grant, error) {
	path := SetOwnSIGIO
	if sig == SIGURG {
		path = SetOwnSIGURG
	}
	if err := r.CheckSignal(setter, owner, sig, path); err != nil {
		return nil, err
	}
	return &Grant{
		sig:       sig,
		ownerPID:  owner.PID,
		setDomain: setter.Domain,
		ownDomain: owner.Domain,
	}, nil
}

// ErrGrantMismatch is returned when a grant is presented for a process
// other than the owner recorded at F_SETOWN time.
var ErrGrantMismatch = errors.New("landlock: grant does not cover this recipient")

// Deliver delivers a deferred SIGIO/SIGURG to recipient under a grant.
// Authorization was decided at SetOwn time; domain changes since then
// (by setter or owner) do not affect the outcome.
func (r *Ruleset) Deliver(g *Grant, recipient *Process) error {
	if g == nil {
		return errors.New("landlock: nil grant")
	}
	if recipient.PID != g.ownerPID {
		return fmt.Errorf("%w: grant covers pid=%d, not pid=%d", ErrGrantMismatch, g.ownerPID, recipient.PID)
	}
	return nil
}

func domainName(d *Domain) string {
	if d == nil {
		return "<unrestricted>"
	}
	return d.name
}
