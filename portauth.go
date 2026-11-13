package netsec

import (
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
)

// Mode selects how an IEEE 802.1X port tracks authenticated hosts.
type Mode uint8

const (
	// SingleHost authorizes only the first authenticated MAC. Frames from
	// any other source MAC are violations.
	SingleHost Mode = iota
	// MultiAuth authenticates each source MAC independently.
	MultiAuth
	// MultiHost opens the port to every source MAC after one host
	// authenticates.
	MultiHost
)

// String returns the configuration spelling of the mode.
func (m Mode) String() string {
	switch m {
	case SingleHost:
		return "single-host"
	case MultiAuth:
		return "multi-auth"
	case MultiHost:
		return "multi-host"
	default:
		return fmt.Sprintf("Mode(%d)", uint8(m))
	}
}

// ViolationAction is the action taken by a single-host port when a frame
// arrives from a source MAC other than the authorized host.
type ViolationAction uint8

const (
	// Restrict drops the offending frame and leaves the port enabled.
	Restrict ViolationAction = iota
	// Shutdown disables the port. It remains disabled until Reset is called.
	Shutdown
)

// String returns the configuration spelling of the violation action.
func (a ViolationAction) String() string {
	switch a {
	case Restrict:
		return "restrict"
	case Shutdown:
		return "shutdown"
	default:
		return fmt.Sprintf("ViolationAction(%d)", uint8(a))
	}
}

// Action is the operation a switch should perform for a frame.
type Action uint8

const (
	// ActionForward forwards the frame.
	ActionForward Action = iota
	// ActionDrop discards the frame.
	ActionDrop
	// ActionShutdown discards the frame and disables the port.
	ActionShutdown
)

// String returns the switch action as text.
func (a Action) String() string {
	switch a {
	case ActionForward:
		return "forward"
	case ActionDrop:
		return "drop"
	case ActionShutdown:
		return "shutdown"
	default:
		return fmt.Sprintf("Action(%d)", uint8(a))
	}
}

// Reason explains why a frame was forwarded or dropped.
type Reason uint8

const (
	// ReasonAuthorized means at least one authentication permits the frame.
	ReasonAuthorized Reason = iota
	// ReasonUnauthenticated means no authentication currently covers the
	// source MAC.
	ReasonUnauthenticated
	// ReasonForeignSource means a single-host port received a frame from a
	// MAC other than its authorized host.
	ReasonForeignSource
	// ReasonPortDisabled means an earlier shutdown violation disabled the
	// port.
	ReasonPortDisabled
)

// String returns a stable, human-readable reason.
func (r Reason) String() string {
	switch r {
	case ReasonAuthorized:
		return "authorized"
	case ReasonUnauthenticated:
		return "unauthenticated"
	case ReasonForeignSource:
		return "foreign-source"
	case ReasonPortDisabled:
		return "port-disabled"
	default:
		return fmt.Sprintf("Reason(%d)", uint8(r))
	}
}

// MAC is a six-byte IEEE 802 MAC address. Its array representation gives it
// value semantics, so it can be used directly as a map key.
type MAC [6]byte

// ParseMAC parses a six-byte colon- or hyphen-separated MAC address.
func ParseMAC(s string) (MAC, error) {
	var mac MAC
	hw, err := net.ParseMAC(strings.TrimSpace(s))
	if err != nil {
		return mac, err
	}
	if len(hw) != len(mac) {
		return mac, fmt.Errorf("netsec: MAC must have 6 bytes, got %d", len(hw))
	}
	copy(mac[:], hw)
	return mac, nil
}

// MustParseMAC parses s and panics if it is not a valid six-byte MAC.
func MustParseMAC(s string) MAC {
	mac, err := ParseMAC(s)
	if err != nil {
		panic(err)
	}
	return mac
}

// MACFromHardwareAddr converts a net.HardwareAddr to a MAC.
func MACFromHardwareAddr(hw net.HardwareAddr) (MAC, error) {
	var mac MAC
	if len(hw) != len(mac) {
		return mac, fmt.Errorf("netsec: MAC must have 6 bytes, got %d", len(hw))
	}
	copy(mac[:], hw)
	return mac, nil
}

// HardwareAddr returns the address in net.HardwareAddr form.
func (m MAC) HardwareAddr() net.HardwareAddr {
	hw := make(net.HardwareAddr, len(m))
	copy(hw, m[:])
	return hw
}

// String returns the canonical lower-case colon-separated form.
func (m MAC) String() string {
	return net.HardwareAddr(m[:]).String()
}

// Decision is the result of authorizing a frame's source MAC.
type Decision struct {
	Source  MAC
	Allowed bool
	Action  Action
	Reason  Reason
}

// String describes the decision for logging.
func (d Decision) String() string {
	status := "denied"
	if d.Allowed {
		status = "allowed"
	}
	return fmt.Sprintf("%s %s: %s (%s)", d.Source, d.Action, d.Reason, status)
}

// Port is a concurrency-safe IEEE 802.1X port authorization state machine.
// Authentication, logoff, and aging events update the state; Evaluate
// produces a per-frame forwarding decision.
type Port struct {
	mu sync.RWMutex

	mode   Mode
	action ViolationAction

	disabled bool
	primary  MAC
	hasAuth  bool
	auth     map[MAC]struct{}
}

// NewPort creates an unauthorized port. action applies only to single-host
// violations; the other modes always drop unauthorized frames.
func NewPort(mode Mode, action ViolationAction) *Port {
	return &Port{
		mode:   mode,
		action: action,
		auth:   make(map[MAC]struct{}),
	}
}

// Mode returns the port's authorization mode.
func (p *Port) Mode() Mode {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.mode
}

// ViolationAction returns the configured single-host violation action.
func (p *Port) ViolationAction() ViolationAction {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.action
}

// Disabled reports whether a shutdown violation has disabled the port.
func (p *Port) Disabled() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.disabled
}

// Authorized reports whether any current authentication covers the port.
func (p *Port) Authorized() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return !p.disabled && p.hasAuth
}

// Primary returns the first authenticated MAC and whether one exists.
func (p *Port) Primary() (MAC, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.primary, p.hasAuth
}

// AuthorizedMACs returns the authenticated MAC records in lexical order. A
// multi-host port records only the host whose authentication opened the port;
// all source MACs are nevertheless forwarded while that record is active.
func (p *Port) AuthorizedMACs() []MAC {
	p.mu.RLock()
	defer p.mu.RUnlock()

	macs := make([]MAC, 0, len(p.auth)+1)
	if p.mode == MultiAuth {
		for mac := range p.auth {
			macs = append(macs, mac)
		}
	} else if p.hasAuth {
		macs = append(macs, p.primary)
	}
	sort.Slice(macs, func(i, j int) bool {
		return macs[i].String() < macs[j].String()
	})
	return macs
}

// Authenticate records successful EAP authentication for mac. It returns
// true when the port authorizes mac after the event.
//
// In single-host mode only the first MAC is accepted. In multi-auth mode every
// MAC can be added independently. In multi-host mode one authentication opens
// the port to all MACs; later authentications do not replace the primary
// record.
func (p *Port) Authenticate(mac MAC) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.disabled {
		return false
	}
	switch p.mode {
	case SingleHost:
		if p.hasAuth {
			return p.primary == mac
		}
		p.primary = mac
		p.hasAuth = true
		return true
	case MultiAuth:
		p.auth[mac] = struct{}{}
		if !p.hasAuth {
			p.primary = mac
			p.hasAuth = true
		}
		return true
	case MultiHost:
		if !p.hasAuth {
			p.primary = mac
			p.hasAuth = true
		}
		return true
	default:
		return false
	}
}

// Logoff applies an EAPOL-Logoff from mac. It reports whether port state
// changed. A multi-host logoff affects the port only when it comes from the
// primary MAC, because that host's authentication opened the port.
func (p *Port) Logoff(mac MAC) bool {
	return p.remove(mac)
}

// AgeOut applies expiration of mac's authentication or aging cache entry. A
// switch's aging timer should call it when the authenticated host becomes
// stale. It reports whether port state changed.
func (p *Port) AgeOut(mac MAC) bool {
	return p.remove(mac)
}

// Expire is an alias for AgeOut.
func (p *Port) Expire(mac MAC) bool {
	return p.remove(mac)
}

func (p *Port) remove(mac MAC) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.disabled || !p.hasAuth {
		return false
	}
	switch p.mode {
	case SingleHost:
		if p.primary != mac {
			return false
		}
		p.primary = MAC{}
		p.hasAuth = false
		return true
	case MultiAuth:
		if _, ok := p.auth[mac]; !ok {
			return false
		}
		delete(p.auth, mac)
		if p.primary == mac {
			p.primary = MAC{}
			p.hasAuth = false
			for remaining := range p.auth {
				if !p.hasAuth || remaining.String() < p.primary.String() {
					p.primary = remaining
					p.hasAuth = true
				}
			}
		}
		return true
	case MultiHost:
		if p.primary != mac {
			return false
		}
		p.primary = MAC{}
		p.hasAuth = false
		return true
	default:
		return false
	}
}

// Evaluate returns the forwarding decision for a frame from source. A
// single-host foreign source produces the configured Restrict or Shutdown
// action; multi-auth and multi-host drop unauthenticated sources.
func (p *Port) Evaluate(source MAC) Decision {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.disabled {
		return Decision{
			Source: source,
			Action: ActionDrop,
			Reason: ReasonPortDisabled,
		}
	}

	switch p.mode {
	case SingleHost:
		if p.hasAuth && p.primary == source {
			return Decision{
				Source:  source,
				Allowed: true,
				Action:  ActionForward,
				Reason:  ReasonAuthorized,
			}
		}
		if !p.hasAuth {
			return Decision{
				Source: source,
				Action: ActionDrop,
				Reason: ReasonUnauthenticated,
			}
		}
		if p.action == Shutdown {
			p.disabled = true
			p.primary = MAC{}
			p.hasAuth = false
			return Decision{
				Source: source,
				Action: ActionShutdown,
				Reason: ReasonForeignSource,
			}
		}
		return Decision{
			Source: source,
			Action: ActionDrop,
			Reason: ReasonForeignSource,
		}

	case MultiAuth:
		if _, ok := p.auth[source]; ok {
			return Decision{
				Source:  source,
				Allowed: true,
				Action:  ActionForward,
				Reason:  ReasonAuthorized,
			}
		}
		return Decision{
			Source: source,
			Action: ActionDrop,
			Reason: ReasonUnauthenticated,
		}

	case MultiHost:
		if p.hasAuth {
			return Decision{
				Source:  source,
				Allowed: true,
				Action:  ActionForward,
				Reason:  ReasonAuthorized,
			}
		}
		return Decision{
			Source: source,
			Action: ActionDrop,
			Reason: ReasonUnauthenticated,
		}

	default:
		return Decision{
			Source: source,
			Action: ActionDrop,
			Reason: ReasonUnauthenticated,
		}
	}
}

// Reset clears authentication and re-enables a port disabled by a shutdown
// violation. The port returns to the unauthorized state and requires a new
// authentication.
func (p *Port) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.disabled = false
	p.primary = MAC{}
	p.hasAuth = false
	clear(p.auth)
}
