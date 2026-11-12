package netsec

import (
	"errors"
	"net/netip"
	"testing"
	"time"
)

var (
	ifaceV4 = Interface{
		Name:     "eth0",
		Prefixes: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")},
	}
	ifaceV6 = Interface{
		Name:     "eth0",
		Prefixes: []netip.Prefix{netip.MustParsePrefix("2001:db8::/64")},
	}

	localV4    = netip.MustParseAddr("192.0.2.10")
	curGW      = netip.MustParseAddr("192.0.2.1")
	newGW      = netip.MustParseAddr("192.0.2.2")
	attackerV4 = netip.MustParseAddr("192.0.2.66")
	offLinkGW  = netip.MustParseAddr("203.0.113.1")
	dstV4      = netip.MustParseAddr("198.51.100.7")

	dstV6     = netip.MustParseAddr("2001:db8:1::7")
	curNextH6 = netip.MustParseAddr("fe80::1")
	target6   = netip.MustParseAddr("2001:db8::2")
	offLinkT6 = netip.MustParseAddr("2001:db8:ffff::2")
	globalSrc = netip.MustParseAddr("2001:db8::1")
)

func newV4Fixture() (*Admitter, *RouteCache, *SentLog, IPv4PacketID) {
	routes := NewRouteCache()
	routes.Set(dstV4, curGW)
	sent := NewSentLog(30 * time.Second)
	id := IPv4PacketID{
		Src: localV4, Dst: dstV4,
		Protocol: 6, SrcPort: 40000, DstPort: 80,
	}
	sent.Record(id)
	adm := NewAdmitter(routes, sent, []Interface{ifaceV4})
	return adm, routes, sent, id
}

func validV4Redirect(id IPv4PacketID) *ICMPv4Redirect {
	return &ICMPv4Redirect{
		Src: curGW, Code: ICMPv4CodeRedirectHost,
		Gateway: newGW, Quoted: id,
	}
}

func requireRoute(t *testing.T, routes *RouteCache, dst, want netip.Addr) {
	t.Helper()
	got, ok := routes.Lookup(dst)
	if !ok || got != want {
		t.Fatalf("route for %v = %v, %v; want %v", dst, got, ok, want)
	}
}

func TestICMPv4RedirectAccepted(t *testing.T) {
	adm, routes, _, id := newV4Fixture()
	if err := adm.AdmitICMPv4Redirect(validV4Redirect(id)); err != nil {
		t.Fatalf("AdmitICMPv4Redirect: %v", err)
	}
	requireRoute(t, routes, dstV4, newGW)
}

func TestICMPv4RedirectFromNonGatewayRejected(t *testing.T) {
	adm, routes, _, id := newV4Fixture()
	m := validV4Redirect(id)
	m.Src = attackerV4 // same-subnet attacker, not the gateway in use
	if err := adm.AdmitICMPv4Redirect(m); !errors.Is(err, ErrNotCurrentGateway) {
		t.Fatalf("err = %v, want %v", err, ErrNotCurrentGateway)
	}
	requireRoute(t, routes, dstV4, curGW)
}

func TestICMPv4RedirectQuoteMismatchRejected(t *testing.T) {
	adm, routes, _, id := newV4Fixture()
	m := validV4Redirect(id)
	m.Quoted.DstPort = 443 // we never sent this packet
	if err := adm.AdmitICMPv4Redirect(m); !errors.Is(err, ErrQuoteMismatch) {
		t.Fatalf("err = %v, want %v", err, ErrQuoteMismatch)
	}
	requireRoute(t, routes, dstV4, curGW)
}

func TestICMPv4RedirectOffLinkGatewayRejected(t *testing.T) {
	adm, routes, _, id := newV4Fixture()
	m := validV4Redirect(id)
	m.Gateway = offLinkGW
	if err := adm.AdmitICMPv4Redirect(m); !errors.Is(err, ErrGatewayOffLink) {
		t.Fatalf("err = %v, want %v", err, ErrGatewayOffLink)
	}
	requireRoute(t, routes, dstV4, curGW)
}

func TestICMPv4RedirectBroadcastDestinationRejected(t *testing.T) {
	adm, routes, sent, _ := newV4Fixture()
	bcast := netip.MustParseAddr("192.0.2.255")
	routes.Set(bcast, curGW)
	id := IPv4PacketID{Src: localV4, Dst: bcast, Protocol: 17, SrcPort: 5000, DstPort: 53}
	sent.Record(id)
	m := validV4Redirect(id)
	if err := adm.AdmitICMPv4Redirect(m); !errors.Is(err, ErrBadDestination) {
		t.Fatalf("err = %v, want %v", err, ErrBadDestination)
	}
	requireRoute(t, routes, bcast, curGW)
}

func TestICMPv4RedirectBroadcastGatewayRejected(t *testing.T) {
	adm, routes, _, id := newV4Fixture()
	m := validV4Redirect(id)
	m.Gateway = netip.MustParseAddr("192.0.2.255")
	if err := adm.AdmitICMPv4Redirect(m); !errors.Is(err, ErrGatewayOffLink) {
		t.Fatalf("err = %v, want %v", err, ErrGatewayOffLink)
	}
	requireRoute(t, routes, dstV4, curGW)
}

func TestICMPv4RedirectNonHostCodeRejected(t *testing.T) {
	adm, routes, _, id := newV4Fixture()
	for _, code := range []uint8{
		ICMPv4CodeRedirectNetwork,
		ICMPv4CodeRedirectNetworkAndTOS,
		ICMPv4CodeRedirectHostAndTOS,
	} {
		m := validV4Redirect(id)
		m.Code = code
		if err := adm.AdmitICMPv4Redirect(m); !errors.Is(err, ErrRedirectCode) {
			t.Fatalf("code %d: err = %v, want %v", code, err, ErrRedirectCode)
		}
	}
	requireRoute(t, routes, dstV4, curGW)
}

func newV6Fixture() (*Admitter, *RouteCache) {
	routes := NewRouteCache()
	routes.Set(dstV6, curNextH6)
	adm := NewAdmitter(routes, NewSentLog(30*time.Second), []Interface{ifaceV6})
	return adm, routes
}

func validV6Redirect() *ICMPv6Redirect {
	return &ICMPv6Redirect{
		Src: curNextH6, HopLimit: 255,
		Target: target6, Dst: dstV6,
	}
}

func TestICMPv6RedirectAccepted(t *testing.T) {
	adm, routes := newV6Fixture()
	if err := adm.AdmitICMPv6Redirect(validV6Redirect()); err != nil {
		t.Fatalf("AdmitICMPv6Redirect: %v", err)
	}
	requireRoute(t, routes, dstV6, target6)
}

func TestICMPv6RedirectTargetEqualsDestinationAccepted(t *testing.T) {
	adm, routes := newV6Fixture()
	m := validV6Redirect()
	m.Target = dstV6 // destination itself is on-link; target need not be in a local prefix
	if err := adm.AdmitICMPv6Redirect(m); err != nil {
		t.Fatalf("AdmitICMPv6Redirect: %v", err)
	}
	requireRoute(t, routes, dstV6, dstV6)
}

func TestICMPv6RedirectBadHopLimitRejected(t *testing.T) {
	adm, routes := newV6Fixture()
	m := validV6Redirect()
	m.HopLimit = 254
	if err := adm.AdmitICMPv6Redirect(m); !errors.Is(err, ErrBadHopLimit) {
		t.Fatalf("err = %v, want %v", err, ErrBadHopLimit)
	}
	requireRoute(t, routes, dstV6, curNextH6)
}

func TestICMPv6RedirectNonLinkLocalSourceRejected(t *testing.T) {
	adm, routes := newV6Fixture()
	routes.Set(dstV6, globalSrc) // route via a global next hop this time
	m := validV6Redirect()
	m.Src = globalSrc
	if err := adm.AdmitICMPv6Redirect(m); !errors.Is(err, ErrSourceNotLinkLocal) {
		t.Fatalf("err = %v, want %v", err, ErrSourceNotLinkLocal)
	}
	requireRoute(t, routes, dstV6, globalSrc)
}

func TestICMPv6RedirectFromNonNextHopRejected(t *testing.T) {
	adm, routes := newV6Fixture()
	m := validV6Redirect()
	m.Src = netip.MustParseAddr("fe80::dead") // link-local, but not our next hop
	if err := adm.AdmitICMPv6Redirect(m); !errors.Is(err, ErrNotCurrentGateway) {
		t.Fatalf("err = %v, want %v", err, ErrNotCurrentGateway)
	}
	requireRoute(t, routes, dstV6, curNextH6)
}

func TestICMPv6RedirectOffLinkTargetRejected(t *testing.T) {
	adm, routes := newV6Fixture()
	m := validV6Redirect()
	m.Target = offLinkT6
	if err := adm.AdmitICMPv6Redirect(m); !errors.Is(err, ErrTargetNotAcceptable) {
		t.Fatalf("err = %v, want %v", err, ErrTargetNotAcceptable)
	}
	requireRoute(t, routes, dstV6, curNextH6)
}
