package optmerge

import (
	"errors"
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

func mustPrefix(t *testing.T, s string) netip.Prefix {
	t.Helper()
	prefix, err := netip.ParsePrefix(s)
	if err != nil {
		t.Fatalf("ParsePrefix(%q): %v", s, err)
	}
	return prefix
}

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	addr, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("ParseAddr(%q): %v", s, err)
	}
	return addr
}

func prefixSet(prefixes []netip.Prefix) map[netip.Prefix]bool {
	out := make(map[netip.Prefix]bool, len(prefixes))
	for _, prefix := range prefixes {
		out[prefix.Masked()] = true
	}
	return out
}

// 1. Every certificate principal appearing in the principal file matches its
// own line, and swapping the order of either side must not change the result.
func TestMergeAllPrincipalsMatch_OrderIndependent(t *testing.T) {
	cert := &Certificate{
		Principals:      []string{"alice", "bob"},
		PermitPTY:       true,
		SourceAddresses: []netip.Prefix{mustPrefix(t, "10.0.0.0/8")},
	}
	lines := []PrincipalLine{
		{
			Principal:       "alice",
			PermitPTY:       true,
			SourceAddresses: []netip.Prefix{mustPrefix(t, "10.1.0.0/16")},
		},
		{
			Principal:       "bob",
			PermitPTY:       true,
			SourceAddresses: []netip.Prefix{mustPrefix(t, "10.1.2.0/24")},
		},
	}

	want := &MergedOptions{
		PermitPTY:       true,
		SourceAddresses: []netip.Prefix{mustPrefix(t, "10.1.2.0/24")},
	}

	got, err := Merge(cert, lines, netip.Addr{})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Merge = %+v, want %+v", got, want)
	}

	// Swap both the certificate principal order and the principal line order.
	reversedCert := &Certificate{
		Principals:      []string{"bob", "alice"},
		PermitPTY:       true,
		SourceAddresses: cert.SourceAddresses,
	}
	reversedLines := []PrincipalLine{lines[1], lines[0]}

	gotReversed, err := Merge(reversedCert, reversedLines, netip.Addr{})
	if err != nil {
		t.Fatalf("Merge reversed: %v", err)
	}
	if !reflect.DeepEqual(gotReversed, want) {
		t.Fatalf("order changed the result: %+v, want %+v", gotReversed, want)
	}

	// carol is in the certificate but missing from the file, so only alice
	// matches; an unrelated file line must never contribute options.
	partialCert := &Certificate{Principals: []string{"carol", "alice"}, PermitPTY: true}
	if _, err := Merge(partialCert, lines, netip.Addr{}); err != nil {
		t.Fatalf("Merge partial: %v", err)
	}
}

func TestMergeRejectsWhenNoPrincipalMatches(t *testing.T) {
	cert := &Certificate{Principals: []string{"mallory"}}
	lines := []PrincipalLine{{Principal: "alice"}}
	_, err := Merge(cert, lines, netip.Addr{})
	if !errors.Is(err, ErrNoMatchingPrincipal) {
		t.Fatalf("err = %v, want ErrNoMatchingPrincipal", err)
	}
}

// 2. A forced command on any matched line becomes mandatory, and two distinct
// forced commands (line vs line, or line vs certificate) reject the login.
func TestMergeForcedCommandConflict(t *testing.T) {
	lines := []PrincipalLine{
		{Principal: "alice", HasCommand: true, Command: "/bin/one"},
		{Principal: "bob", HasCommand: true, Command: "/bin/two"},
	}
	cert := &Certificate{Principals: []string{"alice", "bob"}}

	if _, err := Merge(cert, lines, netip.Addr{}); !errors.Is(err, ErrCommandConflict) {
		t.Fatalf("line/line conflict: err = %v, want ErrCommandConflict", err)
	}

	// Certificate force-command disagreeing with a matched line also rejects.
	certWithCommand := &Certificate{
		Principals:      []string{"alice"},
		ForceCommand:    "/bin/cert",
		HasForceCommand: true,
	}
	_, err := Merge(certWithCommand, lines[:1], netip.Addr{})
	if !errors.Is(err, ErrCommandConflict) {
		t.Fatalf("cert/line conflict: err = %v, want ErrCommandConflict", err)
	}

	// Equal commands merge into that single mandatory command.
	agreeingCert := &Certificate{
		Principals:      []string{"alice", "bob"},
		ForceCommand:    "/bin/same",
		HasForceCommand: true,
	}
	agreeingLines := []PrincipalLine{
		{Principal: "alice", HasCommand: true, Command: "/bin/same"},
		{Principal: "bob", HasCommand: true, Command: "/bin/same"},
	}
	got, err := Merge(agreeingCert, agreeingLines, netip.Addr{})
	if err != nil {
		t.Fatalf("Merge agreeing commands: %v", err)
	}
	if !got.HasForceCommand || got.ForceCommand != "/bin/same" {
		t.Fatalf("forced command = %q (has=%v), want /bin/same", got.ForceCommand, got.HasForceCommand)
	}

	// A command on only one matched line still becomes mandatory even though
	// the other matched line and the certificate carry none.
	oneSided := &Certificate{Principals: []string{"alice", "bob"}}
	oneSidedLines := []PrincipalLine{
		{Principal: "alice"},
		{Principal: "bob", HasCommand: true, Command: "/bin/only"},
	}
	got, err = Merge(oneSided, oneSidedLines, netip.Addr{})
	if err != nil {
		t.Fatalf("Merge one-sided command: %v", err)
	}
	if !got.HasForceCommand || got.ForceCommand != "/bin/only" {
		t.Fatalf("forced command = %q (has=%v), want /bin/only", got.ForceCommand, got.HasForceCommand)
	}
}

// 3. source-address is the intersection across the certificate and every
// matched line; disjoint networks reject, nested networks narrow to the
// smallest, and the peer address is checked against the result.
func TestMergeSourceAddressIntersection(t *testing.T) {
	cert := &Certificate{
		Principals:      []string{"alice", "bob"},
		SourceAddresses: []netip.Prefix{mustPrefix(t, "10.0.0.0/8")},
	}
	lines := []PrincipalLine{
		{Principal: "alice", SourceAddresses: []netip.Prefix{mustPrefix(t, "10.1.0.0/16")}},
		{Principal: "bob", SourceAddresses: []netip.Prefix{mustPrefix(t, "192.168.0.0/16")}},
	}

	_, err := Merge(cert, lines, netip.Addr{})
	if !errors.Is(err, ErrEmptySourceIntersection) {
		t.Fatalf("empty intersection: err = %v, want ErrEmptySourceIntersection", err)
	}

	// Overlapping constraints narrow down to the most specific network.
	nestedLines := []PrincipalLine{
		{Principal: "alice", SourceAddresses: []netip.Prefix{mustPrefix(t, "10.1.0.0/16")}},
		{Principal: "bob", SourceAddresses: []netip.Prefix{mustPrefix(t, "10.1.2.0/24")}},
	}
	got, err := Merge(cert, nestedLines, netip.Addr{})
	if err != nil {
		t.Fatalf("Merge nested: %v", err)
	}
	want := map[netip.Prefix]bool{mustPrefix(t, "10.1.2.0/24"): true}
	if !reflect.DeepEqual(prefixSet(got.SourceAddresses), want) {
		t.Fatalf("intersection = %v, want %v", got.SourceAddresses, want)
	}

	// Peer inside the merged network is accepted; outside is rejected.
	if _, err := Merge(cert, nestedLines, mustAddr(t, "10.1.2.9")); err != nil {
		t.Fatalf("peer inside intersection: %v", err)
	}
	if _, err := Merge(cert, nestedLines, mustAddr(t, "10.1.9.9")); !errors.Is(err, ErrSourceAddressDenied) {
		t.Fatalf("peer outside intersection: err = %v, want ErrSourceAddressDenied", err)
	}

	// Different IP families never intersect.
	v6Cert := &Certificate{
		Principals:      []string{"alice"},
		SourceAddresses: []netip.Prefix{mustPrefix(t, "2001:db8::/32")},
	}
	v4Line := []PrincipalLine{{
		Principal:       "alice",
		SourceAddresses: []netip.Prefix{mustPrefix(t, "10.0.0.0/8")},
	}}
	if _, err := Merge(v6Cert, v4Line, netip.Addr{}); !errors.Is(err, ErrEmptySourceIntersection) {
		t.Fatalf("v4/v6 intersection: err = %v, want ErrEmptySourceIntersection", err)
	}
}

// 4. A principal line can only tighten permissions: granting permit-pty when
// the certificate lacks the extension is rejected; otherwise the extension is
// revoked if any matched line omits it.
func TestMergePTYTighteningOnly(t *testing.T) {
	lines := []PrincipalLine{
		{Principal: "alice", PermitPTY: true},
		{Principal: "bob", PermitPTY: true},
	}
	certWithoutPTY := &Certificate{Principals: []string{"alice", "bob"}}

	_, err := Merge(certWithoutPTY, lines, netip.Addr{})
	if !errors.Is(err, ErrPTYElevation) {
		t.Fatalf("pty elevation: err = %v, want ErrPTYElevation", err)
	}

	// Certificate grants PTY, every matched line keeps it: PTY survives.
	certWithPTY := &Certificate{Principals: []string{"alice", "bob"}, PermitPTY: true}
	got, err := Merge(certWithPTY, lines, netip.Addr{})
	if err != nil {
		t.Fatalf("Merge pty kept: %v", err)
	}
	if !got.PermitPTY {
		t.Fatalf("PermitPTY = false, want true")
	}

	// One matched line omits permit-pty: it tightens the merge to no PTY.
	tightening := []PrincipalLine{
		{Principal: "alice", PermitPTY: true},
		{Principal: "bob"},
	}
	got, err = Merge(certWithPTY, tightening, netip.Addr{})
	if err != nil {
		t.Fatalf("Merge pty tightened: %v", err)
	}
	if got.PermitPTY {
		t.Fatalf("PermitPTY = true, want false after a tightening line")
	}
}

func TestMergeRejectsUnknownOptions(t *testing.T) {
	cert := &Certificate{
		Principals:             []string{"alice"},
		UnknownCriticalOptions: []string{"permit-port-forwarding"},
	}
	_, err := Merge(cert, []PrincipalLine{{Principal: "alice"}}, netip.Addr{})
	if !errors.Is(err, ErrUnknownOption) {
		t.Fatalf("unknown critical option: err = %v, want ErrUnknownOption", err)
	}

	if _, err := ParsePrincipalFile(strings.NewReader("permit-X11-forwarding alice\n")); !errors.Is(err, ErrUnknownOption) {
		t.Fatalf("unknown line option: err = %v, want ErrUnknownOption", err)
	}
}

func TestParsePrincipalFile(t *testing.T) {
	const file = `# comment line
alice
command="/srv/bin/jump -s prod",permit-pty bob
source-address="10.0.0.0/8, 2001:db8::/32" carol
`
	lines, err := ParsePrincipalFile(strings.NewReader(file))
	if err != nil {
		t.Fatalf("ParsePrincipalFile: %v", err)
	}

	want := []PrincipalLine{
		{Principal: "alice"},
		{
			Principal:  "bob",
			HasCommand: true,
			Command:    "/srv/bin/jump -s prod",
			PermitPTY:  true,
		},
		{
			Principal: "carol",
			SourceAddresses: []netip.Prefix{
				mustPrefix(t, "10.0.0.0/8"),
				mustPrefix(t, "2001:db8::/32"),
			},
		},
	}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("parsed lines = %+v\nwant %+v", lines, want)
	}
}

func TestParsePrincipalFileRejects(t *testing.T) {
	cases := map[string]string{
		"unknown option":     "permit-agent-forwarding alice\n",
		"duplicate option":   "permit-pty,permit-pty alice\n",
		"bad cidr":           "source-address=\"not-a-network\" alice\n",
		"flag with value":    "permit-pty=true alice\n",
		"command missing":    "command= alice\n",
		"missing principal":  "permit-pty\n",
		"unterminated quote": "command=\"/bin/x alice\n",
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParsePrincipalFile(strings.NewReader(input)); err == nil {
				t.Fatalf("ParsePrincipalFile(%q) succeeded, want error", input)
			}
		})
	}
}

func TestIntersectUnions(t *testing.T) {
	constraints := [][]netip.Prefix{
		{mustPrefix(t, "10.0.0.0/8"), mustPrefix(t, "192.168.0.0/16")},
		{mustPrefix(t, "10.1.0.0/16"), mustPrefix(t, "172.16.0.0/12")},
		{mustPrefix(t, "10.1.2.0/24")},
	}
	got := intersectUnions(constraints)
	want := map[netip.Prefix]bool{mustPrefix(t, "10.1.2.0/24"): true}
	if !reflect.DeepEqual(prefixSet(got), want) {
		t.Fatalf("intersectUnions = %v, want %v", got, want)
	}

	if got := intersectUnions(nil); got != nil {
		t.Fatalf("intersectUnions(nil) = %v, want nil", got)
	}
}
