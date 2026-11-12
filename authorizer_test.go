package netsec_test

import (
	"net/netip"
	"testing"
	"time"

	netsec "example.org/netsec/q0259"
)

var (
	transactionID = [netsec.TransactionIDSize]byte{
		0x10, 0x11, 0x12, 0x13,
		0x14, 0x15, 0x16, 0x17,
		0x18, 0x19, 0x1a, 0x1b,
	}

	clientTuple = netsec.FiveTuple{
		Client:   netip.MustParseAddrPort("198.51.100.10:50000"),
		Server:   netip.MustParseAddrPort("203.0.113.20:3478"),
		Protocol: "udp",
	}

	publicPeer = netip.MustParseAddrPort("8.8.8.8:53")
	otherPeer  = netip.MustParseAddrPort("1.1.1.1:53")
)

func testPolicy() []netip.Prefix {
	return []netip.Prefix{
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("169.254.169.254/32"),
		netip.MustParsePrefix("fd00::/8"),
	}
}

func newTestAuthorizer(now time.Time) *netsec.Authorizer {
	return netsec.NewAuthorizer(netsec.Config{
		DeniedPrefixes: testPolicy(),
		Now:            func() time.Time { return now },
	})
}

func currentCredentials(username string) netsec.Credentials {
	return netsec.Credentials{
		Username:     username,
		Nonce:        "nonce-" + username,
		NonceExpires: time.Now().Add(time.Minute),
	}
}

func requireErrorCode(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error code %d, got nil", want)
	}
	if got := netsec.ErrorCode(err); got != want {
		t.Fatalf("error code = %d, want %d (%v)", got, want, err)
	}
}

func allocate(t *testing.T, authorizer *netsec.Authorizer, tuple netsec.FiveTuple, username string) {
	t.Helper()
	if err := authorizer.AuthorizeAllocate(tuple, currentCredentials(username)); err != nil {
		t.Fatalf("AuthorizeAllocate(%s): %v", username, err)
	}
}

func TestCreatePermissionRejectsLoopbackAndMetadata(t *testing.T) {
	authorizer := newTestAuthorizer(time.Now())
	allocate(t, authorizer, clientTuple, "alice")

	tests := []struct {
		name string
		peer netip.AddrPort
		code int
	}{
		{name: "loopback", peer: netip.MustParseAddrPort("127.0.0.1:9"), code: netsec.CodeForbidden},
		{name: "metadata IPv4", peer: netip.MustParseAddrPort("169.254.169.254:80"), code: netsec.CodeForbidden},
		{name: "private", peer: netip.MustParseAddrPort("10.1.2.3:1234"), code: netsec.CodeForbidden},
		{name: "metadata through IPv6-mapped", peer: netip.MustParseAddrPort("[::ffff:169.254.169.254]:80"), code: netsec.CodeForbidden},
		{name: "IPv6 private", peer: netip.MustParseAddrPort("[fd00::1]:1234"), code: netsec.CodeForbidden},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			attr := xorPeerAttribute(t, test.peer, transactionID)
			err := authorizer.AuthorizeCreatePermission(
				clientTuple,
				currentCredentials("alice"),
				[][]byte{attr},
				transactionID,
			)
			requireErrorCode(t, err, test.code)
		})
	}

	allowed := xorPeerAttribute(t, publicPeer, transactionID)
	if err := authorizer.AuthorizeCreatePermission(
		clientTuple,
		currentCredentials("alice"),
		[][]byte{allowed},
		transactionID,
	); err != nil {
		t.Fatalf("public peer rejected: %v", err)
	}
}

func TestCreatePermissionRejectsRequestIfAnyPeerIsForbidden(t *testing.T) {
	authorizer := newTestAuthorizer(time.Now())
	allocate(t, authorizer, clientTuple, "alice")

	allowed := xorPeerAttribute(t, publicPeer, transactionID)
	denied := xorPeerAttribute(t, netip.MustParseAddrPort("127.0.0.1:9"), transactionID)

	err := authorizer.AuthorizeCreatePermission(
		clientTuple,
		currentCredentials("alice"),
		[][]byte{allowed, denied},
		transactionID,
	)
	requireErrorCode(t, err, netsec.CodeForbidden)

	if err := authorizer.AuthorizeCreatePermission(
		clientTuple,
		currentCredentials("alice"),
		nil,
		transactionID,
	); netsec.ErrorCode(err) != netsec.CodeBadRequest {
		t.Fatalf("empty permission list error = %v, want 400", err)
	}
}

func TestAllocateBindsFiveTupleToFirstUsername(t *testing.T) {
	authorizer := newTestAuthorizer(time.Now())
	allocate(t, authorizer, clientTuple, "alice")

	if err := authorizer.AuthorizeAllocate(clientTuple, currentCredentials("alice")); err != nil {
		t.Fatalf("repeat Allocate for same user: %v", err)
	}

	requireErrorCode(t,
		authorizer.AuthorizeAllocate(clientTuple, currentCredentials("bob")),
		netsec.CodeWrongCredentials,
	)

	attr := xorPeerAttribute(t, publicPeer, transactionID)
	requireErrorCode(t,
		authorizer.AuthorizeCreatePermission(
			clientTuple, currentCredentials("bob"), [][]byte{attr}, transactionID,
		),
		netsec.CodeWrongCredentials,
	)
	requireErrorCode(t,
		authorizer.AuthorizeChannelBind(
			clientTuple, currentCredentials("bob"), 0x4000, attr, transactionID,
		),
		netsec.CodeWrongCredentials,
	)

	otherTuple := netsec.FiveTuple{
		Client:   netip.MustParseAddrPort("198.51.100.11:50001"),
		Server:   clientTuple.Server,
		Protocol: "udp",
	}
	allocate(t, authorizer, otherTuple, "bob")
}

func TestFiveTupleNormalizationPreventsEquivalentBypass(t *testing.T) {
	authorizer := newTestAuthorizer(time.Now())
	allocate(t, authorizer, clientTuple, "alice")

	equivalent := netsec.FiveTuple{
		Client:   netip.MustParseAddrPort("[::ffff:198.51.100.10]:50000"),
		Server:   netip.MustParseAddrPort("[::ffff:203.0.113.20]:3478"),
		Protocol: "UDP",
	}
	requireErrorCode(t,
		authorizer.AuthorizeAllocate(equivalent, currentCredentials("bob")),
		netsec.CodeWrongCredentials,
	)

	tcpTuple := clientTuple
	tcpTuple.Protocol = "tcp"
	allocate(t, authorizer, tcpTuple, "bob")
}

func TestExpiredNonceReturns438(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	authorizer := newTestAuthorizer(now)

	expired := netsec.Credentials{
		Username:     "alice",
		Nonce:        "old",
		NonceExpires: now.Add(-time.Second),
	}
	requireErrorCode(t,
		authorizer.AuthorizeAllocate(clientTuple, expired),
		netsec.CodeStaleNonce,
	)

	atExpiry := expired
	atExpiry.NonceExpires = now
	requireErrorCode(t,
		authorizer.AuthorizeAllocate(clientTuple, atExpiry),
		netsec.CodeStaleNonce,
	)

	fresh := expired
	fresh.NonceExpires = now.Add(time.Second)
	if err := authorizer.AuthorizeAllocate(clientTuple, fresh); err != nil {
		t.Fatalf("fresh nonce rejected: %v", err)
	}

	attr := xorPeerAttribute(t, publicPeer, transactionID)
	requireErrorCode(t,
		authorizer.AuthorizeCreatePermission(
			clientTuple, expired, [][]byte{attr}, transactionID,
		),
		netsec.CodeStaleNonce,
	)
	requireErrorCode(t,
		authorizer.AuthorizeChannelBind(
			clientTuple, expired, 0x4000, attr, transactionID,
		),
		netsec.CodeStaleNonce,
	)

	zeroExpiry := netsec.Credentials{Username: "alice"}
	if err := authorizer.AuthorizeCreatePermission(
		clientTuple, zeroExpiry, [][]byte{attr}, transactionID,
	); err != nil {
		t.Fatalf("zero nonce expiry should mean no expiry: %v", err)
	}
}

func TestChannelNumberRange(t *testing.T) {
	authorizer := newTestAuthorizer(time.Now())
	allocate(t, authorizer, clientTuple, "alice")
	attr := xorPeerAttribute(t, publicPeer, transactionID)

	for _, channel := range []uint16{0x0000, 0x3fff, 0x8000, 0xffff} {
		requireErrorCode(t,
			authorizer.AuthorizeChannelBind(
				clientTuple, currentCredentials("alice"), channel, attr, transactionID,
			),
			netsec.CodeBadRequest,
		)
	}

	for _, channel := range []uint16{netsec.ChannelNumberMin, netsec.ChannelNumberMax} {
		if !netsec.ValidChannelNumber(channel) {
			t.Fatalf("ValidChannelNumber(%#x) = false", channel)
		}
		if err := authorizer.AuthorizeChannelBind(
			clientTuple, currentCredentials("alice"), channel, attr, transactionID,
		); err != nil {
			t.Fatalf("valid channel %#x rejected: %v", channel, err)
		}
	}
}

func TestChannelBindConflicts(t *testing.T) {
	authorizer := newTestAuthorizer(time.Now())
	allocate(t, authorizer, clientTuple, "alice")

	peerA := xorPeerAttribute(t, publicPeer, transactionID)
	peerB := xorPeerAttribute(t, otherPeer, transactionID)

	if err := authorizer.AuthorizeChannelBind(
		clientTuple, currentCredentials("alice"), 0x4000, peerA, transactionID,
	); err != nil {
		t.Fatalf("first bind: %v", err)
	}
	if err := authorizer.AuthorizeChannelBind(
		clientTuple, currentCredentials("alice"), 0x4000, peerA, transactionID,
	); err != nil {
		t.Fatalf("idempotent bind: %v", err)
	}

	requireErrorCode(t,
		authorizer.AuthorizeChannelBind(
			clientTuple, currentCredentials("alice"), 0x4000, peerB, transactionID,
		),
		netsec.CodeBadRequest,
	)
	if err := authorizer.AuthorizeChannelBind(
		clientTuple, currentCredentials("alice"), 0x4001, peerA, transactionID,
	); err != nil {
		t.Fatalf("same peer on a different channel: %v", err)
	}
}

func TestCreatePermissionAndChannelBindRequireAllocation(t *testing.T) {
	authorizer := newTestAuthorizer(time.Now())
	attr := xorPeerAttribute(t, publicPeer, transactionID)

	requireErrorCode(t,
		authorizer.AuthorizeCreatePermission(
			clientTuple, currentCredentials("alice"), [][]byte{attr}, transactionID,
		),
		netsec.CodeAllocationMismatch,
	)
	requireErrorCode(t,
		authorizer.AuthorizeChannelBind(
			clientTuple, currentCredentials("alice"), 0x4000, attr, transactionID,
		),
		netsec.CodeAllocationMismatch,
	)
}

func TestIPv6PeerPolicyUsesDecodedAddress(t *testing.T) {
	authorizer := netsec.NewAuthorizer(netsec.Config{
		DeniedPrefixes: []netip.Prefix{
			netip.MustParsePrefix("2001:db8::/32"),
			netip.MustParsePrefix("127.0.0.0/8"),
		},
	})
	tuple := clientTuple
	tuple.Client = netip.MustParseAddrPort("[2001:db8:ffff::10]:50000")
	tuple.Server = netip.MustParseAddrPort("[2001:db8:ffff::20]:3478")
	allocate(t, authorizer, tuple, "alice")

	denied := xorPeerAttribute(t, netip.MustParseAddrPort("[2001:db8::1]:443"), transactionID)
	requireErrorCode(t,
		authorizer.AuthorizeCreatePermission(
			tuple, currentCredentials("alice"), [][]byte{denied}, transactionID,
		),
		netsec.CodeForbidden,
	)

	allowed := xorPeerAttribute(t, netip.MustParseAddrPort("[2606:4700:4700::1111]:53"), transactionID)
	if err := authorizer.AuthorizeCreatePermission(
		tuple, currentCredentials("alice"), [][]byte{allowed}, transactionID,
	); err != nil {
		t.Fatalf("public IPv6 peer rejected: %v", err)
	}

	mappedLoopback := xorPeerAttribute(t, netip.MustParseAddrPort("[::ffff:127.0.0.1]:9"), transactionID)
	requireErrorCode(t,
		authorizer.AuthorizeChannelBind(
			tuple, currentCredentials("alice"), 0x4000, mappedLoopback, transactionID,
		),
		netsec.CodeForbidden,
	)
}

func TestReleaseAllowsNewFiveTupleOwner(t *testing.T) {
	authorizer := newTestAuthorizer(time.Now())
	allocate(t, authorizer, clientTuple, "alice")

	authorizer.Release(clientTuple)

	if err := authorizer.AuthorizeAllocate(clientTuple, currentCredentials("bob")); err != nil {
		t.Fatalf("Allocate after Release: %v", err)
	}
}

func TestMissingUsernameIsBadRequest(t *testing.T) {
	authorizer := newTestAuthorizer(time.Now())
	requireErrorCode(t,
		authorizer.AuthorizeAllocate(clientTuple, netsec.Credentials{}),
		netsec.CodeBadRequest,
	)
}
