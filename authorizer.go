package netsec

import (
	"net/netip"
	"strings"
	"sync"
	"time"
)

// Channel number bounds defined by RFC 8656.
const (
	ChannelNumberMin uint16 = 0x4000
	ChannelNumberMax uint16 = 0x7FFF
)

// FiveTuple identifies one TURN allocation. The transport protocol field is
// normally "udp"; it is part of the key so TCP and UDP allocations can never
// share state.
type FiveTuple struct {
	Client   netip.AddrPort
	Server   netip.AddrPort
	Protocol string
}

// Credentials are the identity and nonce information from a request that has
// already passed long-term-credential authentication. NonceExpires is the
// expiry time of the nonce; the zero value means the caller did not supply an
// expiry and the nonce is treated as current. An expiry at or before the
// authorizer's current time produces 438 (Stale Nonce).
type Credentials struct {
	Username     string
	Nonce        string
	NonceExpires time.Time
}

// Config controls the authorizer's peer-address policy and clock.
type Config struct {
	// DeniedPrefixes are peer-address ranges that may never be relayed to.
	// The slice is copied by NewAuthorizer.
	DeniedPrefixes []netip.Prefix

	// Now overrides the clock used for nonce expiry checks. It defaults to
	// time.Now and is useful for deterministic tests.
	Now func() time.Time
}

// Authorizer enforces TURN allocation identity and peer-address policy. Calling
// handlers must use the same FiveTuple values for requests arriving on the same
// allocation.
type Authorizer struct {
	mu          sync.Mutex
	forbidden   []netip.Prefix
	now         func() time.Time
	allocations map[FiveTuple]*allocation
}

type allocation struct {
	username string
	channels map[uint16]netip.AddrPort
}

func newAllocation(username string) *allocation {
	return &allocation{
		username: username,
		channels: make(map[uint16]netip.AddrPort),
	}
}

// NewAuthorizer constructs an authorizer. The forbidden prefix slice is copied
// so later changes by the caller cannot alter policy.
func NewAuthorizer(cfg Config) *Authorizer {
	forbidden := make([]netip.Prefix, len(cfg.DeniedPrefixes))
	copy(forbidden, cfg.DeniedPrefixes)

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &Authorizer{
		forbidden:   forbidden,
		now:         now,
		allocations: make(map[FiveTuple]*allocation),
	}
}

// AuthorizeAllocate authenticates an Allocate request and binds the five-tuple
// to the first username that successfully allocated on it. A subsequent
// Allocate with the same username is idempotent; a different username is
// rejected with 441 (Wrong Credentials).
func (a *Authorizer) AuthorizeAllocate(tuple FiveTuple, creds Credentials) error {
	tuple, err := normalizeTuple(tuple)
	if err != nil {
		return err
	}
	if err := a.checkCredentials(creds); err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if existing, ok := a.allocations[tuple]; ok {
		if existing.username != creds.Username {
			return ErrWrongCredentials
		}
		return nil
	}

	a.allocations[tuple] = newAllocation(creds.Username)
	return nil
}

// AuthorizeCreatePermission decodes and authorizes every XOR-PEER-ADDRESS
// attribute in a CreatePermission request. All addresses are validated before
// success is returned, so a request containing one forbidden peer is rejected
// as a whole.
func (a *Authorizer) AuthorizeCreatePermission(
	tuple FiveTuple,
	creds Credentials,
	peerAttributes [][]byte,
	transactionID [TransactionIDSize]byte,
) error {
	tuple, err := normalizeTuple(tuple)
	if err != nil {
		return err
	}
	if err := a.checkCredentials(creds); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	if _, err := a.lookupAllocation(tuple, creds.Username); err != nil {
		return err
	}
	if len(peerAttributes) == 0 {
		return badRequest("CreatePermission requires at least one XOR-PEER-ADDRESS")
	}

	for _, attr := range peerAttributes {
		_, err := a.decodeAllowedPeer(attr, transactionID)
		if err != nil {
			return err
		}
	}
	return nil
}

// AuthorizeChannelBind validates the channel number, decodes the peer address,
// and binds the channel to the peer for this allocation. Channel numbers
// outside 0x4000-0x7FFF are rejected with 400. Rebinding a channel to a
// different peer is also rejected, while repeating the same binding is
// idempotent.
func (a *Authorizer) AuthorizeChannelBind(
	tuple FiveTuple,
	creds Credentials,
	channel uint16,
	peerAttribute []byte,
	transactionID [TransactionIDSize]byte,
) error {
	tuple, err := normalizeTuple(tuple)
	if err != nil {
		return err
	}
	if err := a.checkCredentials(creds); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	alloc, err := a.lookupAllocation(tuple, creds.Username)
	if err != nil {
		return err
	}
	if !ValidChannelNumber(channel) {
		return badRequest("channel number outside 0x4000-0x7FFF")
	}

	peer, err := a.decodeAllowedPeer(peerAttribute, transactionID)
	if err != nil {
		return err
	}

	if boundPeer, ok := alloc.channels[channel]; ok && boundPeer != peer {
		return badRequest("channel already bound to another peer")
	}

	alloc.channels[channel] = peer
	return nil
}

// Release removes all state for a five-tuple when the allocation ends.
func (a *Authorizer) Release(tuple FiveTuple) {
	tuple, err := normalizeTuple(tuple)
	if err != nil {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.allocations, tuple)
}

// ValidChannelNumber reports whether channel is in the RFC 8656 range.
func ValidChannelNumber(channel uint16) bool {
	return channel >= ChannelNumberMin && channel <= ChannelNumberMax
}

func (a *Authorizer) checkCredentials(creds Credentials) error {
	if creds.Username == "" {
		return badRequest("missing username")
	}
	if !creds.NonceExpires.IsZero() && !a.now().Before(creds.NonceExpires) {
		return ErrStaleNonce
	}
	return nil
}

func (a *Authorizer) lookupAllocation(tuple FiveTuple, username string) (*allocation, error) {
	alloc, ok := a.allocations[tuple]
	if !ok {
		return nil, ErrAllocationMismatch
	}
	if alloc.username != username {
		return nil, ErrWrongCredentials
	}
	return alloc, nil
}

func (a *Authorizer) decodeAllowedPeer(attr []byte, transactionID [TransactionIDSize]byte) (netip.AddrPort, error) {
	peer, err := DecodeXORPeerAddress(attr, transactionID)
	if err != nil {
		return netip.AddrPort{}, err
	}
	if PeerForbidden(peer.Addr(), a.forbidden) {
		return netip.AddrPort{}, forbiddenPeer(peer.String())
	}
	return peer, nil
}

func normalizeTuple(tuple FiveTuple) (FiveTuple, error) {
	if !tuple.Client.IsValid() || !tuple.Server.IsValid() {
		return FiveTuple{}, badRequest("invalid five-tuple address")
	}

	if tuple.Protocol == "" {
		tuple.Protocol = "udp"
	}
	tuple.Protocol = strings.ToLower(tuple.Protocol)
	tuple.Client = normalizeAddrPort(tuple.Client)
	tuple.Server = normalizeAddrPort(tuple.Server)
	return tuple, nil
}

func normalizeAddrPort(addrPort netip.AddrPort) netip.AddrPort {
	return netip.AddrPortFrom(addrPort.Addr().Unmap(), addrPort.Port())
}
