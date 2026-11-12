// Package netsec implements admission control for ICMP redirect messages
// in a userspace network stack. A redirect is only allowed to update the
// route cache after proving it came from the gateway currently serving the
// destination and, for IPv4, that it quotes a packet this host really sent.
package netsec

import (
	"errors"
	"net/netip"
)

// ICMPv4 redirect codes (RFC 792). Only host redirects are honored.
const (
	ICMPv4CodeRedirectNetwork       = 0
	ICMPv4CodeRedirectHost          = 1
	ICMPv4CodeRedirectNetworkAndTOS = 2
	ICMPv4CodeRedirectHostAndTOS    = 3
)

// icmpv6RequiredHopLimit is the hop limit a received ICMPv6 redirect must
// carry (RFC 4861 §8.1) to prove it was not forwarded off-link.
const icmpv6RequiredHopLimit = 255

var (
	ErrRedirectCode        = errors.New("netsec: not an ICMPv4 redirect-for-host code")
	ErrNotCurrentGateway   = errors.New("netsec: redirect source is not the gateway in use for the destination")
	ErrQuoteMismatch       = errors.New("netsec: quoted IP header matches no recently sent packet")
	ErrGatewayOffLink      = errors.New("netsec: advertised gateway is not an on-link unicast host")
	ErrBadDestination      = errors.New("netsec: redirected destination is not a unicast host address")
	ErrBadHopLimit         = errors.New("netsec: ICMPv6 redirect hop limit is not 255")
	ErrSourceNotLinkLocal  = errors.New("netsec: ICMPv6 redirect source is not a link-local address")
	ErrTargetNotAcceptable = errors.New("netsec: ICMPv6 target is neither on-link nor the destination itself")
)

// Interface describes a local interface by its on-link prefixes.
type Interface struct {
	Name     string
	Prefixes []netip.Prefix
}

// OnLink reports whether addr falls inside one of the interface prefixes.
func (i *Interface) OnLink(addr netip.Addr) bool {
	for _, p := range i.Prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// Admitter validates ICMP redirects against the current route cache, the
// log of recently sent packets, and the local interface configuration.
type Admitter struct {
	routes *RouteCache
	sent   *SentLog
	ifaces []Interface
}

func NewAdmitter(routes *RouteCache, sent *SentLog, ifaces []Interface) *Admitter {
	return &Admitter{routes: routes, sent: sent, ifaces: ifaces}
}

// ICMPv4Redirect is a parsed ICMPv4 redirect message.
type ICMPv4Redirect struct {
	Src     netip.Addr   // source address of the ICMP message
	Code    uint8        // ICMP code field
	Gateway netip.Addr   // advertised new gateway
	Quoted  IPv4PacketID // original IP header + 8 payload bytes quoted in the message
}

// AdmitICMPv4Redirect validates m and, only if every check passes, points
// the route for the quoted destination at the advertised gateway. On any
// rejection the route cache is left untouched.
func (a *Admitter) AdmitICMPv4Redirect(m *ICMPv4Redirect) error {
	if m.Code != ICMPv4CodeRedirectHost {
		return ErrRedirectCode
	}
	dst := m.Quoted.Dst
	if !a.isUsableHostV4(dst) {
		return ErrBadDestination
	}
	gw, ok := a.routes.Lookup(dst)
	if !ok || gw != m.Src {
		return ErrNotCurrentGateway
	}
	if !a.sent.Seen(m.Quoted) {
		return ErrQuoteMismatch
	}
	if !a.isOnLinkHostV4(m.Gateway) {
		return ErrGatewayOffLink
	}
	a.routes.Set(dst, m.Gateway)
	return nil
}

// ICMPv6Redirect is a parsed ICMPv6 redirect message (RFC 4861 §4.5).
type ICMPv6Redirect struct {
	Src      netip.Addr // source address of the ICMPv6 message
	HopLimit uint8      // IPv6 hop limit the message arrived with
	Target   netip.Addr // target address: advertised better next hop
	Dst      netip.Addr // destination address that triggered the redirect
}

// AdmitICMPv6Redirect validates m and, only if every check passes, points
// the route for Dst at Target. On any rejection the route cache is left
// untouched.
func (a *Admitter) AdmitICMPv6Redirect(m *ICMPv6Redirect) error {
	if m.HopLimit != icmpv6RequiredHopLimit {
		return ErrBadHopLimit
	}
	if !m.Src.IsLinkLocalUnicast() {
		return ErrSourceNotLinkLocal
	}
	if m.Dst.IsMulticast() || m.Target.IsMulticast() || !m.Target.Is6() {
		return ErrBadDestination
	}
	nh, ok := a.routes.Lookup(m.Dst)
	if !ok || nh != m.Src {
		return ErrNotCurrentGateway
	}
	if m.Target != m.Dst && !a.onLink(m.Target) {
		return ErrTargetNotAcceptable
	}
	a.routes.Set(m.Dst, m.Target)
	return nil
}

// onLink reports whether addr belongs to any local interface prefix.
func (a *Admitter) onLink(addr netip.Addr) bool {
	for _, iface := range a.ifaces {
		if iface.OnLink(addr) {
			return true
		}
	}
	return false
}

// isUsableHostV4 reports whether addr is a unicast IPv4 host address: not
// multicast, not unspecified, not the limited broadcast, and not the
// network or broadcast address of any local prefix that contains it.
func (a *Admitter) isUsableHostV4(addr netip.Addr) bool {
	if !addr.Is4() || addr.IsMulticast() || addr.IsUnspecified() {
		return false
	}
	if addr == netip.AddrFrom4([4]byte{255, 255, 255, 255}) {
		return false
	}
	for _, iface := range a.ifaces {
		for _, p := range iface.Prefixes {
			if p.Addr().Is4() && p.Contains(addr) && isV4NetworkOrBroadcast(p, addr) {
				return false
			}
		}
	}
	return true
}

// isOnLinkHostV4 reports whether addr is a unicast host address inside one
// of the local interface prefixes, excluding network and broadcast
// addresses.
func (a *Admitter) isOnLinkHostV4(addr netip.Addr) bool {
	if !addr.Is4() || addr.IsMulticast() || addr.IsUnspecified() {
		return false
	}
	for _, iface := range a.ifaces {
		for _, p := range iface.Prefixes {
			if !p.Addr().Is4() || !p.Contains(addr) {
				continue
			}
			if !isV4NetworkOrBroadcast(p, addr) {
				return true
			}
		}
	}
	return false
}

// isV4NetworkOrBroadcast reports whether addr is the network or directed
// broadcast address of p. Prefixes of /31 and longer have neither.
func isV4NetworkOrBroadcast(p netip.Prefix, addr netip.Addr) bool {
	if p.Bits() >= 31 {
		return false
	}
	network := p.Masked().Addr()
	if addr == network {
		return true
	}
	b := network.As4()
	for hostBit := 0; hostBit < 32-p.Bits(); hostBit++ {
		b[3-hostBit/8] |= 1 << (hostBit % 8)
	}
	return addr == netip.AddrFrom4(b)
}
