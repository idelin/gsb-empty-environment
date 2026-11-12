package netsec

import (
	"context"
	"net"
	"net/netip"
)

// TCPConnector is the production Connector backed by the real TCP stack.
type TCPConnector struct{}

// Listen binds a TCP endpoint. A zero addr listens on an ephemeral loopback
// port; an explicit zero port binds the wildcard ephemeral port.
func (TCPConnector) Listen(_ context.Context, addr netip.AddrPort) (DataListener, netip.AddrPort, error) {
	bind := addr
	if bind == (netip.AddrPort{}) {
		bind = netip.AddrPortFrom(netip.IPv4Unspecified(), 0)
	}

	ln, err := net.ListenTCP("tcp", net.TCPAddrFromAddrPort(bind))
	if err != nil {
		return nil, netip.AddrPort{}, err
	}
	listener := &tcpListener{TCPListener: ln}
	return listener, listener.LocalAddrPort(), nil
}

// Dial opens a TCP connection to addr, honoring ctx cancellation.
func (TCPConnector) Dial(ctx context.Context, addr netip.AddrPort) (net.Conn, error) {
	dialer := net.Dialer{}
	return dialer.DialContext(ctx, "tcp", addr.String())
}

type tcpListener struct {
	*net.TCPListener
}

func (l *tcpListener) LocalAddrPort() netip.AddrPort {
	addr, _ := netip.ParseAddrPort(l.Addr().String())
	return addr
}
