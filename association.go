package netsec

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"time"
)

var (
	// ErrAssociationClosed means the TCP control connection is gone or the
	// association was explicitly closed.
	ErrAssociationClosed = errors.New("socks5: UDP association is closed")
	// ErrControlClosed means the TCP control connection ended.
	ErrControlClosed = errors.New("socks5: TCP control connection closed")
	// ErrSourceAddress means a datagram did not come from the client address
	// bound by UDP ASSOCIATE.
	ErrSourceAddress = errors.New("socks5: datagram source address is not the associated client")
	// ErrSourcePort means the client's UDP source port changed after it was
	// pinned.
	ErrSourcePort = errors.New("socks5: datagram source port does not match the pinned port")
	// ErrTargetDenied means the request target is in a forbidden network.
	ErrTargetDenied = errors.New("socks5: target address is forbidden")
	// ErrTargetUnresolvable means a domain target could not be checked against
	// the forbidden networks.
	ErrTargetUnresolvable = errors.New("socks5: target domain cannot be resolved for policy checking")
	// ErrNoClientPort means no valid client datagram has fixed the client port.
	ErrNoClientPort = errors.New("socks5: client UDP port has not been established")
)

const defaultResolveTimeout = 2 * time.Second

// Resolver resolves a domain target so Admission can apply forbidden-network
// policy to every address the domain represents.
type Resolver func(ctx context.Context, host string) ([]net.IP, error)

// Policy contains admission policy supplied by the proxy. Forbidden networks
// apply to IP-literal targets directly. A domain target is checked by resolving
// it with Resolver; if forbidden networks are configured and Resolver is nil,
// domain targets are rejected rather than allowed to bypass the policy.
type Policy struct {
	Forbidden      []*net.IPNet
	Resolver       Resolver
	ResolveTimeout time.Duration
}

// Association is the per-UDP-ASSOCIATE admission state. It is alive exactly as
// long as its TCP control connection. The relay loop must call Admit for every
// datagram read from the relay socket and WriteReply for every datagram sent
// back to the client.
type Association struct {
	control net.Conn
	policy  Policy

	clientIP   netip.Addr
	clientPort uint16
	portPinned bool

	mu        sync.Mutex
	err       error
	done      chan struct{}
	closeOnce sync.Once
}

// NewAssociation binds a UDP association to control and to the client address
// declared in the UDP ASSOCIATE request. An unspecified requested IP (for
// example 0.0.0.0) is replaced by the TCP peer address. A requested port of 0
// leaves the UDP source port to be pinned by the first valid datagram.
func NewAssociation(control net.Conn, requested *net.UDPAddr, forbidden []*net.IPNet) (*Association, error) {
	return NewAssociationWithPolicy(control, requested, Policy{Forbidden: forbidden})
}

// NewAssociationWithPolicy is NewAssociation with full target policy.
func NewAssociationWithPolicy(control net.Conn, requested *net.UDPAddr, policy Policy) (*Association, error) {
	if control == nil {
		return nil, errors.New("socks5: nil TCP control connection")
	}

	remoteIP, err := addrIP(control.RemoteAddr())
	if err != nil {
		return nil, fmt.Errorf("socks5: TCP control peer address: %w", err)
	}

	clientIP := remoteIP
	var clientPort uint16
	portPinned := false
	if requested != nil {
		if len(requested.IP) != 0 {
			ip, ok := netip.AddrFromSlice(requested.IP)
			if !ok {
				return nil, fmt.Errorf("socks5: invalid requested UDP IP %q", requested.IP)
			}
			ip = normalizeIP(ip)
			if !ip.IsUnspecified() {
				clientIP = ip
			}
		}
		if requested.Port < 0 || requested.Port > 65535 {
			return nil, fmt.Errorf("socks5: invalid requested UDP port %d", requested.Port)
		}
		clientPort = uint16(requested.Port)
		portPinned = clientPort != 0
	}

	if !clientIP.IsValid() || clientIP.IsUnspecified() {
		return nil, errors.New("socks5: cannot determine client IP address")
	}

	policy.Forbidden = append([]*net.IPNet(nil), policy.Forbidden...)
	if policy.ResolveTimeout <= 0 {
		policy.ResolveTimeout = defaultResolveTimeout
	}

	a := &Association{
		control:    control,
		policy:     policy,
		clientIP:   clientIP,
		clientPort: clientPort,
		portPinned: portPinned,
		done:       make(chan struct{}),
	}
	go a.watchControl()
	return a, nil
}

func addrIP(addr net.Addr) (netip.Addr, error) {
	if addr == nil {
		return netip.Addr{}, errors.New("missing remote address")
	}
	switch value := addr.(type) {
	case *net.TCPAddr:
		if value == nil {
			return netip.Addr{}, errors.New("missing TCP remote address")
		}
		ip, ok := netip.AddrFromSlice(value.IP)
		if !ok {
			return netip.Addr{}, fmt.Errorf("invalid TCP remote IP %q", value.IP)
		}
		return normalizeIP(ip), nil
	case *net.UDPAddr:
		if value == nil {
			return netip.Addr{}, errors.New("missing UDP remote address")
		}
		ip, ok := netip.AddrFromSlice(value.IP)
		if !ok {
			return netip.Addr{}, fmt.Errorf("invalid UDP remote IP %q", value.IP)
		}
		return normalizeIP(ip), nil
	default:
		host, _, err := net.SplitHostPort(addr.String())
		if err != nil {
			return netip.Addr{}, err
		}
		ip, err := netip.ParseAddr(host)
		if err != nil {
			return netip.Addr{}, err
		}
		return normalizeIP(ip), nil
	}
}

func normalizeIP(ip netip.Addr) netip.Addr {
	ip = ip.Unmap()
	if ip.Zone() != "" {
		ip = ip.WithZone("")
	}
	return ip
}

func (a *Association) watchControl() {
	var buf [256]byte
	for {
		if _, err := a.control.Read(buf[:]); err != nil {
			a.closeWith(fmt.Errorf("%w: %v", ErrControlClosed, err))
			return
		}
	}
}

// Done is closed when the TCP control connection ends or Close is called.
func (a *Association) Done() <-chan struct{} {
	return a.done
}

// Err returns the reason the association ended, or nil while it is alive.
func (a *Association) Err() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.err
}

// Alive reports whether the TCP control connection is still usable.
func (a *Association) Alive() bool {
	select {
	case <-a.done:
		return false
	default:
		return true
	}
}

// Close ends the association and closes the TCP control connection. Closing the
// control connection from either side also ends the association.
func (a *Association) Close() error {
	a.closeWith(ErrAssociationClosed)
	err := a.control.Close()
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func (a *Association) closeWith(err error) {
	a.closeOnce.Do(func() {
		a.mu.Lock()
		a.err = err
		close(a.done)
		a.mu.Unlock()
	})
}

// ClientAddr returns the bound client address. Its port remains 0 until the
// first valid datagram fixes it when the UDP ASSOCIATE request used port 0.
func (a *Association) ClientAddr() *net.UDPAddr {
	a.mu.Lock()
	defer a.mu.Unlock()
	return &net.UDPAddr{
		IP:   net.IP(a.clientIP.AsSlice()),
		Port: int(a.clientPort),
	}
}

// Admit validates a datagram received on the relay socket. src is the source
// reported by the UDP socket. A datagram is admitted only if the association is
// alive, src matches the associated client, its source port matches the pinned
// port, its SOCKS5 header is valid, and its target is allowed by Policy.
//
// When the requested client port was 0, the first admitted datagram pins the
// source port. Invalid probes, including FRAG or malformed domain packets, do
// not pin it.
func (a *Association) Admit(src *net.UDPAddr, packet []byte) (Address, []byte, error) {
	if src == nil {
		return Address{}, nil, ErrSourceAddress
	}
	if src.Port < 0 || src.Port > 65535 {
		return Address{}, nil, ErrSourcePort
	}
	srcIP, ok := netip.AddrFromSlice(src.IP)
	if !ok {
		return Address{}, nil, ErrSourceAddress
	}
	srcIP = normalizeIP(srcIP)

	if err := a.checkSource(srcIP, uint16(src.Port)); err != nil {
		return Address{}, nil, err
	}

	target, payload, err := ParseDatagram(packet)
	if err != nil {
		return Address{}, nil, err
	}
	if err := a.checkTarget(target); err != nil {
		return Address{}, nil, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return Address{}, nil, fmt.Errorf("%w: %w", ErrAssociationClosed, a.err)
	}
	if srcIP != a.clientIP {
		return Address{}, nil, ErrSourceAddress
	}
	if a.portPinned {
		if uint16(src.Port) != a.clientPort {
			return Address{}, nil, ErrSourcePort
		}
	} else {
		a.clientPort = uint16(src.Port)
		a.portPinned = true
	}
	return target, payload, nil
}

func (a *Association) checkSource(srcIP netip.Addr, srcPort uint16) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return fmt.Errorf("%w: %w", ErrAssociationClosed, a.err)
	}
	if srcIP != a.clientIP {
		return ErrSourceAddress
	}
	if a.portPinned && srcPort != a.clientPort {
		return ErrSourcePort
	}
	return nil
}

func (a *Association) checkTarget(target Address) error {
	if len(a.policy.Forbidden) == 0 {
		return nil
	}

	if ip, err := netip.ParseAddr(target.Host); err == nil {
		if isForbidden(a.policy.Forbidden, normalizeIP(ip)) {
			return fmt.Errorf("%w: %s", ErrTargetDenied, target.Host)
		}
		return nil
	}

	if a.policy.Resolver == nil {
		return fmt.Errorf("%w: %s", ErrTargetUnresolvable, target.Host)
	}

	ctx, cancel := context.WithTimeout(context.Background(), a.policy.ResolveTimeout)
	defer cancel()
	ips, err := a.policy.Resolver(ctx, target.Host)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrTargetUnresolvable, target.Host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("%w: %s returned no addresses", ErrTargetUnresolvable, target.Host)
	}

	for _, raw := range ips {
		ip, ok := netip.AddrFromSlice(raw)
		if !ok {
			return fmt.Errorf("%w: %s returned an invalid address", ErrTargetUnresolvable, target.Host)
		}
		if isForbidden(a.policy.Forbidden, normalizeIP(ip)) {
			return fmt.Errorf("%w: %s resolves to %s", ErrTargetDenied, target.Host, ip)
		}
	}
	return nil
}

func isForbidden(networks []*net.IPNet, ip netip.Addr) bool {
	raw := net.IP(ip.AsSlice())
	for _, network := range networks {
		if network != nil && network.Contains(raw) {
			return true
		}
	}
	return false
}

// ReplyPacket builds a SOCKS5 UDP reply naming remote as the data source.
func (a *Association) ReplyPacket(remote Address, payload []byte) ([]byte, error) {
	if !a.Alive() {
		return nil, fmt.Errorf("%w: %w", ErrAssociationClosed, a.Err())
	}
	return EncodeDatagram(remote, payload)
}

// WriteReply writes a SOCKS5 UDP reply to the bound client only. relay is the
// UDP socket on which the association's relay loop received client datagrams.
func (a *Association) WriteReply(relay net.PacketConn, remote Address, payload []byte) error {
	if relay == nil {
		return errors.New("socks5: nil UDP relay connection")
	}

	a.mu.Lock()
	if a.err != nil {
		err := a.err
		a.mu.Unlock()
		return fmt.Errorf("%w: %w", ErrAssociationClosed, err)
	}
	if !a.portPinned {
		a.mu.Unlock()
		return ErrNoClientPort
	}
	client := &net.UDPAddr{
		IP:   net.IP(a.clientIP.AsSlice()),
		Port: int(a.clientPort),
	}
	a.mu.Unlock()

	packet, err := EncodeDatagram(remote, payload)
	if err != nil {
		return err
	}

	a.mu.Lock()
	err = a.err
	a.mu.Unlock()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrAssociationClosed, err)
	}

	_, err = relay.WriteTo(packet, client)
	return err
}
