package taskpkg

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
)

var (
	// ErrRevoked is returned by Dial once the binder has been revoked, and is
	// the reason a delivered connection is rejected if revocation wins the race.
	ErrRevoked = errors.New("taskpkg: target binding revoked")

	// ErrNoPolicy is returned when no policy has ever been installed.
	ErrNoPolicy = errors.New("taskpkg: no target-binding policy installed")

	// ErrHostMismatch is returned when the dialed host or port is not the one
	// pinned by the current policy.
	ErrHostMismatch = errors.New("taskpkg: host or port is not licensed by the current policy")

	// ErrNoVerifiedEndpoint means the resolver returned no address covered by
	// the policy's verified IP set.
	ErrNoVerifiedEndpoint = errors.New("taskpkg: resolver returned no verified endpoint")

	// ErrStaleGeneration is returned when a connection's policy generation is
	// no longer the generation in force at delivery time.
	ErrStaleGeneration = errors.New("taskpkg: policy generation changed before delivery")

	// ErrPeerNotVerified is returned when the actual peer is outside the
	// verified IP set.
	ErrPeerNotVerified = errors.New("taskpkg: actual peer is not a verified IP")

	// ErrPeerForbidden is returned when the actual peer is covered by a
	// currently forbidden CIDR.
	ErrPeerForbidden = errors.New("taskpkg: actual peer is covered by a forbidden CIDR")

	// ErrPortMismatch is returned when the actual peer uses another port than
	// the one pinned by the policy.
	ErrPortMismatch = errors.New("taskpkg: actual peer port does not match the policy port")

	// ErrLosesRace is recorded for a verified connection that reaches the
	// delivery gate after another connection from the same Dial has already
	// been delivered. It is closed by the binder.
	ErrLosesRace = errors.New("taskpkg: connection lost the parallel dial race")
)

// Resolver performs the single host-name lookup a dial is allowed to make.
// net.Resolver satisfies this interface.
type Resolver interface {
	LookupIP(ctx context.Context, network, host string) ([]net.IP, error)
}

// Dialer establishes one connection to an IP literal. Implementations must not
// resolve a host name: the binder only ever passes ip:port targets.
// net.Dialer satisfies this interface via (*net.Dialer).DialContext.
type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// OnConnect receives every connection that passes the delivery gate, along
// with its verified actual peer. The callback runs while the binder lock is
// held, so its ordering against Update and Revoke is deterministic; keep it
// short and never call back into the binder.
type OnConnect func(conn net.Conn, peer net.Addr)

// Config wires a binder. Zero fields fall back to the standard library
// resolver and a zero-value net.Dialer.
type Config struct {
	Resolver  Resolver
	Dialer    Dialer
	OnConnect OnConnect
}

// Binder serializes every configuration change and every connection delivery
// through one mutex, giving revocation and delivery a single global order.
type Binder struct {
	mu        sync.Mutex
	policy    *Policy
	revoked   bool
	resolver  Resolver
	dialer    Dialer
	onConnect OnConnect
}

// NewBinder returns a binder with no installed policy.
func NewBinder(cfg Config) *Binder {
	resolver := cfg.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	dialer := cfg.Dialer
	if dialer == nil {
		dialer = &net.Dialer{}
	}
	return &Binder{
		resolver:  resolver,
		dialer:    dialer,
		onConnect: cfg.OnConnect,
	}
}

// Update installs policy, which must carry a generation strictly greater than
// the generation currently in force. In-flight connections bound to an older
// generation are rejected at delivery time and closed.
func (b *Binder) Update(policy *Policy) error {
	if policy == nil {
		return errors.New("taskpkg: Update requires a non-nil policy")
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.revoked {
		return ErrRevoked
	}
	if b.policy != nil && policy.Generation <= b.policy.Generation {
		return fmt.Errorf("%w: generation %d is not newer than %d",
			ErrStaleGeneration, policy.Generation, b.policy.Generation)
	}
	b.policy = policy
	return nil
}

// Revoke withdraws the current binding. Connections delivered afterwards,
// including parallel attempts that only finish after this call, are closed.
// Already delivered connections stay in the hands of the host.
func (b *Binder) Revoke() {
	b.mu.Lock()
	b.policy = nil
	b.revoked = true
	b.mu.Unlock()
}

// CurrentGeneration returns the generation of the binding currently in force,
// or -1 if no policy is installed or the binder has been revoked.
func (b *Binder) CurrentGeneration() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.policy == nil {
		return -1
	}
	return b.policy.Generation
}

// dialAttempt tracks one parallel IP-literal dial for a single Dial call.
type dialAttempt struct {
	mu      sync.Mutex
	reasons []error
}

func (a *dialAttempt) record(err error) {
	a.mu.Lock()
	a.reasons = append(a.reasons, err)
	a.mu.Unlock()
}

// Dial resolves host once, dials every verified answer in parallel and
// delivers the first connection whose actual peer still satisfies the current
// binding. The host name is never passed to the underlying dialer.
func (b *Binder) Dial(ctx context.Context, network, host string, port int) (net.Conn, net.Addr, error) {
	policy, err := b.snapshot(host, port)
	if err != nil {
		return nil, nil, err
	}
	generation := policy.Generation

	answers, err := b.resolver.LookupIP(ctx, network, policy.CanonicalHost)
	if err != nil {
		return nil, nil, fmt.Errorf("taskpkg: target lookup failed: %w", err)
	}

	endpoints := filterVerified(policy, answers)
	if len(endpoints) == 0 {
		return nil, nil, ErrNoVerifiedEndpoint
	}

	dialCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg      sync.WaitGroup
		winner  net.Conn
		winAddr net.Addr
		attempt = &dialAttempt{}
	)

	for _, endpoint := range endpoints {
		wg.Add(1)
		go func(addr netip.Addr) {
			defer wg.Done()

			target := joinHostPort(addr, policy.Port)
			conn, dialErr := b.dialer.DialContext(dialCtx, network, target)
			if dialErr != nil {
				attempt.record(dialErr)
				return
			}

			peer, deliverErr := b.tryDeliver(conn, policy, generation, &winner)
			if deliverErr != nil {
				attempt.record(deliverErr)
				_ = conn.Close()
				return
			}

			winAddr = peer
			cancel()
		}(endpoint)
	}

	wg.Wait()

	if winner != nil {
		return winner, winAddr, nil
	}
	return nil, nil, fmt.Errorf("taskpkg: no connection to %s could be delivered: %w",
		policy.CanonicalHost, errors.Join(attempt.failures()...))
}

// snapshot validates the dial request against the binding currently in force.
func (b *Binder) snapshot(host string, port int) (*Policy, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.revoked {
		return nil, ErrRevoked
	}
	if b.policy == nil {
		return nil, ErrNoPolicy
	}
	policy := b.policy
	if !matchHost(policy.CanonicalHost, host) || port != policy.Port {
		return nil, ErrHostMismatch
	}
	return policy, nil
}

// tryDeliver is the single delivery gate. The winner decision, the callback
// and the ordering against Update/Revoke all happen under the binder mutex, so
// exactly one connection per Dial is ever delivered. winner points at the
// Dial-local winning connection and is only touched while holding the lock.
func (b *Binder) tryDeliver(conn net.Conn, bound *Policy, generation int64, winner *net.Conn) (net.Addr, error) {
	peer, err := peerTuple(conn, bound.Port)
	if err != nil {
		return nil, err
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.revoked || b.policy == nil {
		return nil, ErrRevoked
	}
	if b.policy.Generation != generation {
		return nil, ErrStaleGeneration
	}
	if *winner != nil {
		return nil, ErrLosesRace
	}

	addr, ok := canonicalIP(peer.IP)
	if !ok || !b.policy.allowsIP(addr) {
		return nil, fmt.Errorf("%w: %s", ErrPeerNotVerified, peer.IP)
	}
	if b.policy.forbitsIP(addr) {
		return nil, fmt.Errorf("%w: %s", ErrPeerForbidden, peer.IP)
	}

	*winner = conn
	if b.onConnect != nil {
		b.onConnect(conn, peer)
	}
	return peer, nil
}

// peerTuple reads the connection's actual peer and enforces the pinned port.
func peerTuple(conn net.Conn, wantPort int) (*net.TCPAddr, error) {
	remote := conn.RemoteAddr()
	if remote == nil {
		return nil, errors.New("taskpkg: connection reports no remote address")
	}
	peer, ok := remote.(*net.TCPAddr)
	if !ok {
		parsed, err := net.ResolveTCPAddr("tcp", remote.String())
		if err != nil {
			return nil, fmt.Errorf("taskpkg: cannot parse peer %q: %w", remote.String(), err)
		}
		peer = parsed
	}
	if peer.Port != wantPort {
		return nil, fmt.Errorf("%w: peer %d, want %d", ErrPortMismatch, peer.Port, wantPort)
	}
	if _, ok := canonicalIP(peer.IP); !ok {
		return nil, fmt.Errorf("%w: %s", ErrPeerNotVerified, peer.IP)
	}
	return peer, nil
}

// filterVerified keeps only answers licensed by the policy and drops
// duplicates, so a changed DNS answer cannot widen the dial target.
func filterVerified(policy *Policy, answers []net.IP) []netip.Addr {
	seen := make(map[netip.Addr]struct{}, len(answers))
	endpoints := make([]netip.Addr, 0, len(answers))
	for _, answer := range answers {
		addr, ok := canonicalIP(answer)
		if !ok || !policy.allowsIP(addr) {
			continue
		}
		if _, dup := seen[addr]; dup {
			continue
		}
		seen[addr] = struct{}{}
		endpoints = append(endpoints, addr)
	}
	return endpoints
}

func (a *dialAttempt) failures() []error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.reasons) == 0 {
		return []error{errors.New("all attempts were rejected without a recorded reason")}
	}
	return append([]error(nil), a.reasons...)
}
