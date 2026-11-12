package netsec

import (
	"context"
	"crypto/sha256"
	"errors"
	"net"
	"net/netip"
)

// Mode selects how a transfer's data channel is established.
type Mode int

const (
	// Passive makes the proxy listen and wait for the peer to connect.
	Passive Mode = iota + 1
	// Active makes the proxy dial an address offered by the peer.
	Active
)

// String renders the FTP-style mode name.
func (m Mode) String() string {
	switch m {
	case Passive:
		return "passive"
	case Active:
		return "active"
	default:
		return "unknown"
	}
}

// Digest is a fixed operation summary bound to an approval.
type Digest [sha256.Size]byte

// DigestOf returns the SHA-256 digest of op, used as the operation summary.
func DigestOf(op []byte) Digest {
	return sha256.Sum256(op)
}

var (
	// ErrInvalidApproval is returned when an approval is malformed.
	ErrInvalidApproval = errors.New("netsec: invalid transfer approval")
	// ErrPeerNotAllowed is returned when the data peer is outside the approved set.
	ErrPeerNotAllowed = errors.New("netsec: peer not in approved set")
	// ErrModeMismatch is returned when the open mode differs from the approval.
	ErrModeMismatch = errors.New("netsec: transfer mode does not match approval")
	// ErrTransferRevoked is returned after cancel, reauth or session close.
	ErrTransferRevoked = errors.New("netsec: transfer instance revoked")
	// ErrTransferClosed is returned when a one-shot instance has terminated.
	ErrTransferClosed = errors.New("netsec: transfer instance already closed")
	// ErrTransferInUse is returned when the one-shot channel is consumed twice.
	ErrTransferInUse = errors.New("netsec: transfer instance already in use")
	// ErrNoDataConnection is returned when the data channel never arrives.
	ErrNoDataConnection = errors.New("netsec: no matching data connection")
	// ErrConnectionRefused is the connector-level refusal sentinel.
	ErrConnectionRefused = errors.New("netsec: connection refused")
)

// Approval is the fixed, single-use authorization envelope for one transfer.
//
// Every field is bound at authorization time and checked again when the data
// channel is attached. An approval can never be opened twice.
type Approval struct {
	// Digest identifies the exact authorized operation (summary).
	Digest Digest
	// Account is the authenticated session account bound to the transfer.
	Account string
	// Mode pins the data channel direction.
	Mode Mode
	// AllowedPeers is the complete set of acceptable data-channel endpoints.
	AllowedPeers []netip.AddrPort
}

func (a Approval) clone() Approval {
	peers := make([]netip.AddrPort, len(a.AllowedPeers))
	copy(peers, a.AllowedPeers)
	a.AllowedPeers = peers
	return a
}

func (a Approval) validate() error {
	if a.Digest == (Digest{}) {
		return ErrInvalidApproval
	}
	if a.Account == "" {
		return ErrInvalidApproval
	}
	if a.Mode != Passive && a.Mode != Active {
		return ErrInvalidApproval
	}
	if len(a.AllowedPeers) == 0 {
		return ErrInvalidApproval
	}
	for _, peer := range a.AllowedPeers {
		if !peer.IsValid() || peer.Port() == 0 {
			return ErrInvalidApproval
		}
	}
	return nil
}

func (a Approval) allows(peer netip.AddrPort) bool {
	for _, allowed := range a.AllowedPeers {
		if allowed == peer {
			return true
		}
	}
	return false
}

// Connector abstracts the transport so the binding logic can be tested
// deterministically without real sockets.
type Connector interface {
	// Listen opens a data listener at addr. A zero addr picks an endpoint.
	Listen(ctx context.Context, addr netip.AddrPort) (DataListener, netip.AddrPort, error)
	// Dial opens a data connection to addr.
	Dial(ctx context.Context, addr netip.AddrPort) (net.Conn, error)
}

// DataListener accepts inbound data connections.
type DataListener interface {
	net.Listener
	// LocalAddrPort is the bound endpoint the peer must dial.
	LocalAddrPort() netip.AddrPort
}

// RemoteAddrPort extracts the peer endpoint from a data connection.
func RemoteAddrPort(conn net.Conn) (netip.AddrPort, bool) {
	if conn == nil || conn.RemoteAddr() == nil {
		return netip.AddrPort{}, false
	}
	addr, err := netip.ParseAddrPort(conn.RemoteAddr().String())
	if err != nil {
		return netip.AddrPort{}, false
	}
	return addr, true
}

// mergeContext returns a context canceled when either parent is canceled.
func mergeContext(parent, lifecycle context.Context) context.Context {
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(lifecycle, cancel)
	context.AfterFunc(ctx, func() { stop() })
	return ctx
}
