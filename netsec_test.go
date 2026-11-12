package netsec_test

import (
	"context"
	"errors"
	"net"
	"testing"

	netsec "example.org/netsec/q0169"
)

func mustPermission(t *testing.T, host string, port int) netsec.Permission {
	t.Helper()
	perm, err := netsec.ParsePermission(host, port)
	if err != nil {
		t.Fatalf("ParsePermission(%q): %v", host, err)
	}
	return perm
}

func TestParseIDClassifiesFourClasses(t *testing.T) {
	cases := []struct {
		name   string
		host   string
		port   int
		family netsec.Family
		class  netsec.Class
		empty  bool
		mapped bool
	}{
		{"empty host is not wildcard", "", 8080, netsec.FamilyUnspecified, netsec.ClassEmpty, true, false},
		{"ipv4 wildcard", "0.0.0.0", 8080, netsec.FamilyIPv4, netsec.ClassWildcard, false, false},
		{"ipv6 wildcard", "::", 8080, netsec.FamilyIPv6, netsec.ClassWildcard, false, false},
		{"ipv4 loopback", "127.0.0.1", 8080, netsec.FamilyIPv4, netsec.ClassLoopback, false, false},
		{"ipv6 loopback", "::1", 8080, netsec.FamilyIPv6, netsec.ClassLoopback, false, false},
		{"ipv4 literal", "198.51.100.7", 8080, netsec.FamilyIPv4, netsec.ClassLiteral, false, false},
		{"ipv6 literal", "2001:db8::9", 8080, netsec.FamilyIPv6, netsec.ClassLiteral, false, false},
		{"mapped ipv6 is ipv4 under the hood", "::ffff:198.51.100.7", 8080, netsec.FamilyIPv4, netsec.ClassLiteral, false, true},
		{"mapped wildcard stays wildcard", "::ffff:0.0.0.0", 8080, netsec.FamilyIPv4, netsec.ClassWildcard, false, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, err := netsec.ParseID(tc.host, tc.port)
			if err != nil {
				t.Fatalf("ParseID(%q): %v", tc.host, err)
			}
			if id.Family != tc.family {
				t.Errorf("family = %s, want %s", id.Family, tc.family)
			}
			if id.Class != tc.class {
				t.Errorf("class = %s, want %s", id.Class, tc.class)
			}
			if id.Empty != tc.empty {
				t.Errorf("empty = %v, want %v", id.Empty, tc.empty)
			}
			if id.Mapped != tc.mapped {
				t.Errorf("mapped = %v, want %v", id.Mapped, tc.mapped)
			}
			if tc.mapped && !id.IP.Equal(net.ParseIP("198.51.100.7")) &&
				!id.IP.Equal(net.IPv4zero) {
				t.Errorf("canonical IP = %s, want embedded IPv4", id.IP)
			}
		})
	}
}

func TestParseIDRejectsHostnamesAndBadPorts(t *testing.T) {
	if _, err := netsec.ParseID("localhost", 8080); err == nil {
		t.Error("hostname must be rejected: implicit resolution could widen to wildcard")
	}
	if _, err := netsec.ParseID("127.0.0.1", -1); err == nil {
		t.Error("negative port must be rejected")
	}
	if _, err := netsec.ParseID("127.0.0.1", 70000); err == nil {
		t.Error("port above 65535 must be rejected")
	}
}

func TestPermissionContainment(t *testing.T) {
	loopback4 := mustPermission(t, "127.0.0.1", 0)
	wildcard4 := mustPermission(t, "0.0.0.0", 0)
	literal := mustPermission(t, "198.51.100.20", 0)
	wildcard6 := mustPermission(t, "::", 0)

	cases := []struct {
		name string
		perm netsec.Permission
		host string
		port int
		want bool
	}{
		{"loopback grant accepts loopback", loopback4, "127.0.0.5", 40001, true},
		{"loopback grant rejects public address", loopback4, "203.0.113.9", 40001, false},
		{"loopback grant rejects wildcard", loopback4, "0.0.0.0", 40001, false},
		{"wildcard grant accepts literal", wildcard4, "203.0.113.9", 40001, true},
		{"wildcard grant accepts loopback", wildcard4, "127.0.0.1", 40001, true},
		{"wildcard grant accepts wildcard", wildcard4, "0.0.0.0", 40001, true},
		{"wildcard grant rejects other family", wildcard6, "203.0.113.9", 40001, false},
		{"literal grant requires exact ip", literal, "198.51.100.20", 40001, true},
		{"literal grant rejects neighbor ip", literal, "198.51.100.21", 40001, false},
		{"mapped address matches ipv4 grant", wildcard4, "::ffff:203.0.113.9", 40001, true},
		{"mapped address cannot match native ipv6 grant", wildcard6, "::ffff:203.0.113.9", 40001, false},
		{"fixed port grant enforces port", mustPermission(t, "127.0.0.1", 9000), "127.0.0.1", 9001, false},
		{"fixed port grant accepts same port", mustPermission(t, "127.0.0.1", 9000), "127.0.0.1", 9000, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, err := netsec.ParseID(tc.host, tc.port)
			if err != nil {
				t.Fatalf("ParseID: %v", err)
			}
			if got := tc.perm.Contains(id); got != tc.want {
				t.Errorf("Contains(%s) = %v, want %v", id, got, tc.want)
			}
		})
	}
}

func TestZeroPermissionIsFailClosed(t *testing.T) {
	var perm netsec.Permission
	id, _ := netsec.ParseID("127.0.0.1", 8080)
	if perm.Contains(id) {
		t.Error("zero-value Permission must not contain any listener")
	}
}

func TestGrantAllocatesThenBindsTicket(t *testing.T) {
	driver := newMockDriver()
	perm := mustPermission(t, "127.0.0.1", 0)

	ticket, err := netsec.Grant(context.Background(), driver, perm, netsec.Request{
		Network: "tcp",
		Host:    "127.0.0.1",
		Port:    0,
	})
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if ticket.ID == "" {
		t.Error("ticket must carry an opaque id")
	}
	if ticket.Granted.Port == 0 {
		t.Error("driver must have replaced the zero port with an allocated port")
	}
	if ticket.Granted.Class != netsec.ClassLoopback {
		t.Errorf("granted class = %s, want loopback", ticket.Granted.Class)
	}
	if driver.activeCount() != 1 {
		t.Errorf("active leases = %d, want 1", driver.activeCount())
	}

	if err := ticket.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err := ticket.Release(); err != nil {
		t.Errorf("second Release must be idempotent: %v", err)
	}
	if driver.activeCount() != 0 || driver.releasedCount() != 1 {
		t.Errorf("after release: active=%d released=%d, want 0 and 1",
			driver.activeCount(), driver.releasedCount())
	}
}

func TestGrantRejectsRoguePublicListener(t *testing.T) {
	driver := newMockDriver()
	driver.override = func(netsec.ListenID) (string, string) {
		// The driver was asked for loopback but hands back a public socket.
		return "tcp4", "203.0.113.66"
	}
	perm := mustPermission(t, "127.0.0.1", 0)

	ticket, err := netsec.Grant(context.Background(), driver, perm, netsec.Request{
		Network: "tcp4",
		Host:    "127.0.0.1",
		Port:    0,
	})
	if !errors.Is(err, netsec.ErrOutsidePermission) {
		t.Fatalf("err = %v, want ErrOutsidePermission", err)
	}
	if ticket != nil {
		t.Error("no ticket may be issued for an out-of-scope listener")
	}
	if driver.activeCount() != 0 {
		t.Errorf("rogue lease still active = %d, want 0 (released on validation failure)",
			driver.activeCount())
	}
	if driver.releasedCount() != 1 {
		t.Errorf("recycled leases = %d, want 1", driver.releasedCount())
	}
}

func TestGrantRejectsMappedAddressEscalation(t *testing.T) {
	// Native IPv6 loopback permission; a mapped IPv4 endpoint returned on a
	// tcp6 socket must not masquerade as an IPv6 grant.
	driver := newMockDriver()
	driver.override = func(netsec.ListenID) (string, string) {
		return "tcp6", "::ffff:203.0.113.77"
	}
	perm := mustPermission(t, "::1", 0)

	_, err := netsec.Grant(context.Background(), driver, perm, netsec.Request{
		Network: "tcp6",
		Host:    "::1",
		Port:    0,
	})
	if !errors.Is(err, netsec.ErrOutsidePermission) {
		t.Fatalf("err = %v, want ErrOutsidePermission for mapped IPv4 on IPv6 grant", err)
	}
	if driver.activeCount() != 0 || driver.releasedCount() != 1 {
		t.Errorf("lease not recycled: active=%d released=%d",
			driver.activeCount(), driver.releasedCount())
	}

	// The same mapped endpoint is acceptable when the grant is IPv4.
	ipv4Driver := newMockDriver()
	ipv4Driver.override = func(netsec.ListenID) (string, string) {
		return "tcp6", "::ffff:203.0.113.77"
	}
	ipv4Perm := mustPermission(t, "0.0.0.0", 0)
	ticket, err := netsec.Grant(context.Background(), ipv4Driver, ipv4Perm, netsec.Request{
		Network: "tcp",
		Host:    "0.0.0.0",
		Port:    0,
	})
	if err != nil {
		t.Fatalf("mapped endpoint should satisfy an IPv4 wildcard grant: %v", err)
	}
	if !ticket.Granted.Mapped || ticket.Granted.Family != netsec.FamilyIPv4 {
		t.Errorf("granted = %+v, want mapped IPv4 classification", ticket.Granted)
	}
}

func TestGrantRecyclesLeaseWhenCanceledAfterAllocation(t *testing.T) {
	driver := newMockDriver()
	perm := mustPermission(t, "127.0.0.1", 0)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// The driver observes the canceled context and refuses before allocating.
	ticket, err := netsec.Grant(ctx, driver, perm, netsec.Request{
		Network: "tcp",
		Host:    "127.0.0.1",
		Port:    0,
	})
	if !errors.Is(err, netsec.ErrDriverRejected) {
		t.Fatalf("err = %v, want ErrDriverRejected", err)
	}
	if ticket != nil {
		t.Error("canceled grant must not return a ticket")
	}
	if driver.activeCount() != 0 {
		t.Errorf("leaked leases after cancellation = %d", driver.activeCount())
	}

	// Post-allocation recycling path: the grant succeeds on a live context,
	// then the host cancels and releases the bound ticket.
	liveDriver := newMockDriver()
	ticket, err = netsec.Grant(context.Background(), liveDriver, perm, netsec.Request{
		Network: "tcp",
		Host:    "127.0.0.1",
		Port:    0,
	})
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	cancel()
	if err := ticket.Release(); err != nil {
		t.Fatalf("Release after cancel: %v", err)
	}
	if liveDriver.activeCount() != 0 || liveDriver.releasedCount() != 1 {
		t.Errorf("lease not recycled after cancellation: active=%d released=%d",
			liveDriver.activeCount(), liveDriver.releasedCount())
	}
}

func TestEmptyHostRequiresExplicitPolicy(t *testing.T) {
	driver := newMockDriver()
	perm := mustPermission(t, "127.0.0.1", 0)

	// Default behavior: empty host is denied rather than widened to wildcard.
	if _, err := netsec.Grant(context.Background(), driver, perm, netsec.Request{
		Network: "tcp",
		Host:    "",
		Port:    0,
	}); !errors.Is(err, netsec.ErrUnresolvedEmpty) {
		t.Fatalf("err = %v, want ErrUnresolvedEmpty", err)
	}

	// Explicit loopback policy resolves it within the grant.
	ticket, err := netsec.Grant(context.Background(), driver, perm, netsec.Request{
		Network: "tcp",
		Host:    "",
		Port:    0,
		Policy:  netsec.LoopbackEmptyPolicy(netsec.FamilyIPv4),
	})
	if err != nil {
		t.Fatalf("Grant with loopback empty policy: %v", err)
	}
	if ticket.Granted.Class != netsec.ClassLoopback ||
		ticket.Granted.Family != netsec.FamilyIPv4 {
		t.Errorf("granted = %+v, want IPv4 loopback", ticket.Granted)
	}

	// A wildcard policy cannot squeeze through a loopback permission.
	if _, err := netsec.Grant(context.Background(), newMockDriver(), perm, netsec.Request{
		Network: "tcp",
		Host:    "",
		Port:    0,
		Policy:  netsec.WildcardEmptyPolicy(netsec.FamilyIPv4),
	}); !errors.Is(err, netsec.ErrOutsidePermission) {
		t.Fatalf("err = %v, want ErrOutsidePermission", err)
	}
}

func TestGrantRejectsRequestOutsidePermissionBeforeAllocation(t *testing.T) {
	driver := newMockDriver()
	perm := mustPermission(t, "127.0.0.1", 0)

	_, err := netsec.Grant(context.Background(), driver, perm, netsec.Request{
		Network: "tcp",
		Host:    "203.0.113.5",
		Port:    0,
	})
	if !errors.Is(err, netsec.ErrOutsidePermission) {
		t.Fatalf("err = %v, want ErrOutsidePermission", err)
	}
	if driver.activeCount() != 0 || driver.releasedCount() != 0 {
		t.Errorf("driver must not be contacted for an out-of-scope request: "+
			"active=%d released=%d", driver.activeCount(), driver.releasedCount())
	}
}

func TestGrantPropagatesDriverFailure(t *testing.T) {
	driver := newMockDriver()
	driver.failWith = errors.New("socket exhaustion")
	perm := mustPermission(t, "127.0.0.1", 0)

	if _, err := netsec.Grant(context.Background(), driver, perm, netsec.Request{
		Network: "tcp",
		Host:    "127.0.0.1",
		Port:    0,
	}); !errors.Is(err, netsec.ErrDriverRejected) {
		t.Fatalf("err = %v, want ErrDriverRejected", err)
	}
}
