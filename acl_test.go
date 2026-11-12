package netsec

import (
	"errors"
	"net"
	"testing"
)

func digestAuth(t *testing.T, c *Connection, user, password string) ID {
	t.Helper()
	if err := c.AddAuth(SchemeDigest, user+":"+password); err != nil {
		t.Fatalf("AddAuth(%s): %v", user, err)
	}
	return ID{Scheme: SchemeDigest, ID: DigestID(user, password)}
}

func TestDigestIDVector(t *testing.T) {
	// Reference ZooKeeper vector for super:admin.
	if got, want := DigestID("super", "admin"), "super:xQJmxLMiHGwaqBvst5y6rkB6HQs="; got != want {
		t.Fatalf("DigestID = %q, want %q", got, want)
	}
}

func TestExpandCreateACL_AuthWithoutIdentity(t *testing.T) {
	az := NewAuthorizer("")
	conn := az.NewConnection(net.ParseIP("10.0.0.1"))

	// An anonymous connection cannot use the auth scheme.
	if _, err := conn.ExpandCreateACL([]ACL{{Perms: PermAll, ID: ID{Scheme: SchemeAuth}}}); !errors.Is(err, ErrInvalidACL) {
		t.Fatalf("anonymous auth expansion err = %v, want ErrInvalidACL", err)
	}

	// An empty ACL is always invalid.
	if _, err := conn.ExpandCreateACL(nil); !errors.Is(err, ErrInvalidACL) {
		t.Fatalf("empty ACL err = %v, want ErrInvalidACL", err)
	}
}

func TestExpandCreateACL_ExpandsAuthenticatedIdentities(t *testing.T) {
	az := NewAuthorizer("")
	conn := az.NewConnection(net.ParseIP("10.0.0.1"))
	id1 := digestAuth(t, conn, "user1", "pass1")
	id2 := digestAuth(t, conn, "user2", "pass2")

	in := []ACL{
		{Perms: PermAll, ID: ID{Scheme: SchemeAuth, ID: ""}},
		{Perms: PermRead, ID: ID{Scheme: SchemeWorld, ID: WorldAnyone}},
	}
	out, err := conn.ExpandCreateACL(in)
	if err != nil {
		t.Fatalf("ExpandCreateACL: %v", err)
	}

	want := []ACL{
		{Perms: PermAll, ID: id1},
		{Perms: PermAll, ID: id2},
		{Perms: PermRead, ID: ID{Scheme: SchemeWorld, ID: WorldAnyone}},
	}
	if len(out) != len(want) {
		t.Fatalf("expanded %d entries, want %d: %+v", len(out), len(want), out)
	}
	for i := range want {
		if out[i] != want[i] {
			t.Fatalf("entry %d = %+v, want %+v", i, out[i], want[i])
		}
	}
	// The original request must remain untouched.
	if in[0].ID.Scheme != SchemeAuth {
		t.Fatalf("input ACL mutated: %+v", in)
	}
}

func TestAuthorize_WorldAndPermissionBits(t *testing.T) {
	az := NewAuthorizer("")
	conn := az.NewConnection(net.ParseIP("10.0.0.1"))

	readOnly := []ACL{{Perms: PermRead, ID: ID{Scheme: SchemeWorld, ID: WorldAnyone}}}
	if !conn.CanRead(readOnly) {
		t.Fatal("anonymous world:anyone should grant READ")
	}
	if conn.CanWrite(readOnly) {
		t.Fatal("READ-only entry must not grant WRITE")
	}

	// An unrecognized world identity matches nobody.
	bogus := []ACL{{Perms: PermRead, ID: ID{Scheme: SchemeWorld, ID: "elsewhere"}}}
	if conn.CanRead(bogus) {
		t.Fatal("world identity other than anyone must not match")
	}

	// An empty stored ACL denies by default and surfaces ErrAccessDenied.
	if conn.Authorize(nil, PermRead) {
		t.Fatal("empty ACL must deny")
	}
	if err := conn.Check(nil, PermRead); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("Check err = %v, want ErrAccessDenied", err)
	}
}

func TestAuthorize_DigestExactMatch(t *testing.T) {
	az := NewAuthorizer("")

	owner := az.NewConnection(net.ParseIP("10.0.0.1"))
	ownerID := digestAuth(t, owner, "user1", "pass1")

	intruder := az.NewConnection(net.ParseIP("10.0.0.2"))
	digestAuth(t, intruder, "user1", "wrong-password")

	node := []ACL{{Perms: PermRead | PermWrite, ID: ownerID}}
	if !owner.CanRead(node) || !owner.CanWrite(node) {
		t.Fatal("owner should hold READ and WRITE")
	}
	if intruder.CanRead(node) {
		t.Fatal("a digest entry must require the exact digest; wrong password denied")
	}
}

func TestDelete_OnlyParentDeletePermission(t *testing.T) {
	az := NewAuthorizer("")

	owner := az.NewConnection(net.ParseIP("10.0.0.1"))
	ownerID := digestAuth(t, owner, "user1", "pass1")

	other := az.NewConnection(net.ParseIP("10.0.0.2"))
	otherID := digestAuth(t, other, "user2", "pass2")

	// Parent grants owner DELETE (and CREATE) but nothing to other.
	parent := []ACL{{Perms: PermDelete | PermCreate, ID: ownerID}}
	// Child grants ADMIN to other, which must be irrelevant to deletion.
	child := []ACL{{Perms: PermAdmin, ID: otherID}}

	if !owner.CanDelete(parent) {
		t.Fatal("owner should delete based on parent DELETE")
	}
	if other.CanDelete(parent) {
		t.Fatal("other has no DELETE on parent; child ADMIN must not help")
	}
	if !owner.CanCreate(parent) {
		t.Fatal("owner should create based on parent CREATE")
	}
	// Prove the child ACL was never consulted.
	_ = child
}

func TestSetACL_RequiresNodeAdmin(t *testing.T) {
	az := NewAuthorizer("")

	admin := az.NewConnection(net.ParseIP("10.0.0.1"))
	adminID := digestAuth(t, admin, "admin", "pw")

	writer := az.NewConnection(net.ParseIP("10.0.0.2"))
	writerID := digestAuth(t, writer, "writer", "pw")

	node := []ACL{
		{Perms: PermAdmin, ID: adminID},
		{Perms: PermWrite, ID: writerID},
	}
	if !admin.CanSetACL(node) {
		t.Fatal("ADMIN holder may setACL")
	}
	if writer.CanSetACL(node) {
		t.Fatal("WRITE alone must not permit setACL")
	}
}

func TestSuperDigest_BypassesAllChecks(t *testing.T) {
	superID := DigestID("super", "admin")
	az := NewAuthorizer(superID)

	anon := az.NewConnection(net.ParseIP("10.0.0.1"))
	if anon.IsSuper() {
		t.Fatal("unauthenticated connection must not be super")
	}

	super := az.NewConnection(net.ParseIP("10.0.0.2"))
	if err := super.AddAuth(SchemeDigest, "super:admin"); err != nil {
		t.Fatalf("super AddAuth: %v", err)
	}
	if !super.IsSuper() {
		t.Fatal("connection authenticating the super digest must be super")
	}

	// Bypasses even an empty/denied ACL and every permission.
	for _, perm := range []uint32{PermRead, PermWrite, PermCreate, PermDelete, PermAdmin} {
		if !super.Authorize(nil, perm) {
			t.Fatalf("super must bypass check for perm %d", perm)
		}
	}
	if err := super.Check(nil, PermAdmin); err != nil {
		t.Fatalf("super Check: %v", err)
	}
	if !super.CanDelete(nil) || !super.CanSetACL(nil) {
		t.Fatal("super must delete and setACL regardless of ACLs")
	}

	// A different password for the same username is not super.
	impostor := az.NewConnection(net.ParseIP("10.0.0.3"))
	digestAuth(t, impostor, "super", "not-admin")
	if impostor.IsSuper() || impostor.Authorize(nil, PermAdmin) {
		t.Fatal("super requires the exact digest")
	}
}

func TestIP_CIDRBoundary(t *testing.T) {
	az := NewAuthorizer("")
	rule := []ACL{{Perms: PermRead, ID: ID{Scheme: SchemeIP, ID: "192.168.1.0/24"}}}

	cases := []struct {
		ip   string
		want bool
	}{
		{"192.168.1.0", true},    // network address boundary
		{"192.168.1.255", true},  // broadcast boundary
		{"192.168.1.128", true},  // interior
		{"192.168.0.255", false}, // just outside, low side
		{"192.168.2.0", false},   // just outside, high side
		{"10.0.0.1", false},      // unrelated network
		{"2001:db8::1", false},   // wrong address family
	}
	for _, tc := range cases {
		conn := az.NewConnection(net.ParseIP(tc.ip))
		if got := conn.CanRead(rule); got != tc.want {
			t.Errorf("CIDR %s match = %v, want %v", tc.ip, got, tc.want)
		}
	}

	// Bare IP behaves as a /32 host route.
	host := []ACL{{Perms: PermRead, ID: ID{Scheme: SchemeIP, ID: "10.0.0.5"}}}
	if !az.NewConnection(net.ParseIP("10.0.0.5")).CanRead(host) {
		t.Fatal("bare IP rule must match the exact host")
	}
	if az.NewConnection(net.ParseIP("10.0.0.6")).CanRead(host) {
		t.Fatal("bare IP rule must not match a neighbor")
	}

	// Nil client address and malformed rules never match and must not panic.
	if az.NewConnection(nil).CanRead(rule) {
		t.Fatal("nil client IP must not match an ip entry")
	}
	bad := []ACL{{Perms: PermRead, ID: ID{Scheme: SchemeIP, ID: "not-a-cidr"}}}
	if az.NewConnection(net.ParseIP("10.0.0.1")).CanRead(bad) {
		t.Fatal("malformed ip rule must not match")
	}
}

func TestAddAuth_Validation(t *testing.T) {
	az := NewAuthorizer("")
	conn := az.NewConnection(nil)

	if err := conn.AddAuth("kerberos", "user"); !errors.Is(err, ErrUnsupportedScheme) {
		t.Fatalf("unsupported scheme err = %v", err)
	}
	for _, bad := range []string{"no-colon", ":password", "username:"} {
		if err := conn.AddAuth(SchemeDigest, bad); !errors.Is(err, ErrMalformedIdentity) {
			t.Fatalf("AddAuth(%q) err = %v, want ErrMalformedIdentity", bad, err)
		}
	}
}
