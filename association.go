// Package netsec provides admission control for SOCKS5 UDP ASSOCIATE
// relay loops. An Association binds the relay's UDP endpoint to the
// single client that requested it over a TCP control connection, so
// that internet hosts cannot abuse the open relay port to send or
// reflect traffic through the proxy.
package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
)

// Drop reasons returned by Admit. Every rejected datagram is silently
// discarded by the relay loop; the error only explains why.
var (
	// ErrClosed: the TCP control connection is gone, so the
	// association (and its relay port) is dead.
	ErrClosed = errors.New("association closed with control connection")
	// ErrBadSource: datagram does not come from the declared client
	// IP, or its source port drifted from the pinned port.
	ErrBadSource = errors.New("datagram from unexpected client address")
	// ErrReserved: RSV field is not zero.
	ErrReserved = errors.New("non-zero reserved field")
	// ErrFragmented: FRAG is non-zero; fragmentation is unsupported.
	ErrFragmented = errors.New("fragmented datagram unsupported")
	// ErrMalformed: header truncated, unknown ATYP, or domain length
	// runs past the end of the datagram.
	ErrMalformed = errors.New("malformed UDP request header")
	// ErrBlockedTarget: destination falls inside a caller-forbidden
	// network (e.g. loopback, link-local, internal ranges).
	ErrBlockedTarget = errors.New("target address in forbidden network")
)

// Address is a parsed SOCKS5 destination address.
type Address struct {
	IP     net.IP // set for IPv4/IPv6 targets
	Domain string // set for domain targets
	Port   uint16
}

func (a Address) String() string {
	host := a.Domain
	if host == "" {
		host = a.IP.String()
	}
	return fmt.Sprintf("%s:%d", host, a.Port)
}

// Association is the admission state of one UDP ASSOCIATE. Its
// lifetime is exactly the lifetime of the TCP control connection:
// once done is closed, every further datagram is dropped.
type Association struct {
	clientIP netip.Addr
	done     <-chan struct{}
	blocked  []*net.IPNet

	mu         sync.Mutex
	clientPort uint16 // 0 until pinned by the first admitted datagram
}

// NewAssociation binds the relay to the client address declared in the
// UDP ASSOCIATE request. A declared IP of 0.0.0.0/:: (or nil) means
// "use the TCP control connection's source address". A non-zero
// declared port pins the client port immediately; otherwise the port
// of the first admitted datagram is pinned and may never change.
// done must be closed when the TCP control connection terminates.
// blocked lists destination networks the relay must never send to.
func NewAssociation(declared *net.UDPAddr, control *net.TCPAddr, done <-chan struct{}, blocked []*net.IPNet) *Association {
	ip := declared.IP
	if ip == nil || ip.IsUnspecified() {
		ip = control.IP
	}
	addr, _ := netip.ParseAddr(ip.String())
	a := &Association{
		clientIP: addr,
		done:     done,
		blocked:  blocked,
	}
	if declared.Port > 0 && declared.Port <= 0xffff {
		a.clientPort = uint16(declared.Port)
	}
	return a
}

// Alive reports whether the TCP control connection is still open.
func (a *Association) Alive() bool {
	select {
	case <-a.done:
		return false
	default:
		return true
	}
}

// ClientAddr returns the pinned client address that return traffic
// must be sent to, or nil if no datagram has pinned the port yet.
// Replies must go to this address and nowhere else.
func (a *Association) ClientAddr() *net.UDPAddr {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.clientPort == 0 {
		return nil
	}
	return &net.UDPAddr{IP: a.clientIP.AsSlice(), Port: int(a.clientPort)}
}

// Admit validates one inbound datagram arriving from src. On success
// it returns the parsed target address and the payload to relay. On
// failure the datagram must be dropped; the error says why.
func (a *Association) Admit(src *net.UDPAddr, datagram []byte) (Address, []byte, error) {
	if !a.Alive() {
		return Address{}, nil, ErrClosed
	}
	if err := a.admitSource(src); err != nil {
		return Address{}, nil, err
	}
	target, payload, err := parseRequest(datagram)
	if err != nil {
		return Address{}, nil, err
	}
	if a.isBlocked(target) {
		return Address{}, nil, ErrBlockedTarget
	}
	return target, payload, nil
}

// admitSource pins the client port on first contact and enforces the
// declared IP plus pinned port on every datagram.
func (a *Association) admitSource(src *net.UDPAddr) error {
	srcIP, err := netip.ParseAddr(src.IP.String())
	if err != nil || srcIP != a.clientIP {
		return ErrBadSource
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case a.clientPort == 0:
		if src.Port <= 0 || src.Port > 0xffff {
			return ErrBadSource
		}
		a.clientPort = uint16(src.Port)
	case uint16(src.Port) != a.clientPort:
		return ErrBadSource
	}
	return nil
}

// isBlocked reports whether the target IP falls in a forbidden
// network. Domain targets cannot be checked here; the caller should
// re-check after DNS resolution.
func (a *Association) isBlocked(target Address) bool {
	if target.IP == nil {
		return false
	}
	for _, n := range a.blocked {
		if n.Contains(target.IP) {
			return true
		}
	}
	return false
}

// parseRequest decodes a SOCKS5 UDP request header:
//
//	+----+------+------+----------+----------+----------+
//	|RSV | FRAG | ATYP | DST.ADDR | DST.PORT |   DATA   |
//	+----+------+------+----------+----------+----------+
//	| 2  |  1   |  1   | Variable |    2     | Variable |
func parseRequest(d []byte) (Address, []byte, error) {
	if len(d) < 4 {
		return Address{}, nil, ErrMalformed
	}
	if d[0] != 0 || d[1] != 0 {
		return Address{}, nil, ErrReserved
	}
	if d[2] != 0 {
		return Address{}, nil, ErrFragmented
	}
	var target Address
	off := 4
	switch d[3] {
	case 0x01: // IPv4
		if len(d) < off+4+2 {
			return Address{}, nil, ErrMalformed
		}
		target.IP = net.IP(d[off : off+4]).To4()
		off += 4
	case 0x04: // IPv6
		if len(d) < off+16+2 {
			return Address{}, nil, ErrMalformed
		}
		target.IP = net.IP(d[off : off+16]).To16()
		off += 16
	case 0x03: // domain
		if len(d) < off+1 {
			return Address{}, nil, ErrMalformed
		}
		n := int(d[off])
		off++
		if n == 0 || len(d) < off+n+2 {
			return Address{}, nil, ErrMalformed
		}
		target.Domain = string(d[off : off+n])
		off += n
	default:
		return Address{}, nil, ErrMalformed
	}
	target.Port = binary.BigEndian.Uint16(d[off : off+2])
	return target, d[off+2:], nil
}

// BuildReply wraps payload from a remote target into a SOCKS5 UDP
// reply datagram destined for the pinned client. It returns nil once
// the association is dead or no client port is pinned, so the relay
// loop drops the packet instead of leaking it elsewhere.
func (a *Association) BuildReply(from Address, payload []byte) (dst *net.UDPAddr, datagram []byte) {
	if !a.Alive() {
		return nil, nil
	}
	dst = a.ClientAddr()
	if dst == nil {
		return nil, nil
	}
	head := []byte{0, 0, 0}
	switch {
	case from.Domain != "":
		if len(from.Domain) > 255 {
			return nil, nil
		}
		head = append(head, 0x03, byte(len(from.Domain)))
		head = append(head, from.Domain...)
	case from.IP.To4() != nil:
		ip4 := from.IP.To4()
		head = append(head, 0x01)
		head = append(head, ip4...)
	default:
		head = append(head, 0x04)
		head = append(head, from.IP.To16()...)
	}
	var port [2]byte
	binary.BigEndian.PutUint16(port[:], from.Port)
	head = append(head, port[:]...)
	return dst, append(head, payload...)
}
