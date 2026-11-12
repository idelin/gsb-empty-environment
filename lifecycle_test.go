package taskpkg

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// TestRevocation verifies revocation both before dialing and while a dial is
// in flight. A connection finishing after Revoke is closed and never reaches
// the callback.
func TestRevocation(t *testing.T) {
	t.Run("revoked before dial", func(t *testing.T) {
		resolver := &fakeResolver{}
		dialer := newFakeDialer()
		done := &delivered{}
		binder := NewBinder(Config{Resolver: resolver, Dialer: dialer, OnConnect: done.onConnect})

		verified := "203.0.113.50"
		policy := testPolicy(t, []string{verified}, nil, 1)
		if err := binder.Update(policy); err != nil {
			t.Fatalf("Update: %v", err)
		}
		binder.Revoke()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, _, err := binder.Dial(ctx, "tcp", testHost, policy.Port)
		if !errors.Is(err, ErrRevoked) {
			t.Fatalf("Dial error = %v, want ErrRevoked", err)
		}
		if len(dialer.dialCalls()) != 0 {
			t.Fatal("revoked binder still attempted a dial")
		}
		if done.count() != 0 {
			t.Fatal("callback fired after revocation")
		}
	})

	t.Run("late connection closed on revoke", func(t *testing.T) {
		resolver := &fakeResolver{}
		dialer := newFakeDialer()
		done := &delivered{}
		binder := NewBinder(Config{Resolver: resolver, Dialer: dialer, OnConnect: done.onConnect})

		verified := "203.0.113.51"
		policy := testPolicy(t, []string{verified}, nil, 1)
		if err := binder.Update(policy); err != nil {
			t.Fatalf("Update: %v", err)
		}

		conn := newFakeConn(verified, policy.Port)
		target := dialTarget(verified, policy.Port)
		dialer.setConn(target, conn)
		entering, release := dialer.gate(target)
		resolver.setAnswers(net.ParseIP(verified))

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		errCh := make(chan error, 1)
		go func() {
			_, _, err := binder.Dial(ctx, "tcp", testHost, policy.Port)
			errCh <- err
		}()

		<-entering
		binder.Revoke()
		close(release)

		err := <-errCh
		if !errors.Is(err, ErrRevoked) {
			t.Fatalf("Dial error = %v, want ErrRevoked", err)
		}
		if !conn.isClosed() {
			t.Fatal("late connection was not closed after revocation")
		}
		if done.count() != 0 {
			t.Fatal("callback fired for a connection delivered after revocation")
		}
	})
}

// TestGenerationSwitch rejects connections bound to a stale generation after
// Update installs a new policy, and refuses non-monotonic generations.
func TestGenerationSwitch(t *testing.T) {
	resolver := &fakeResolver{}
	dialer := newFakeDialer()
	done := &delivered{}
	binder := NewBinder(Config{Resolver: resolver, Dialer: dialer, OnConnect: done.onConnect})

	oldIP := "203.0.113.60"
	newIP := "203.0.113.61"
	oldPolicy := testPolicy(t, []string{oldIP}, nil, 1)
	if err := binder.Update(oldPolicy); err != nil {
		t.Fatalf("first Update: %v", err)
	}
	if gen := binder.CurrentGeneration(); gen != 1 {
		t.Fatalf("generation = %d, want 1", gen)
	}

	conn := newFakeConn(oldIP, oldPolicy.Port)
	oldTarget := dialTarget(oldIP, oldPolicy.Port)
	dialer.setConn(oldTarget, conn)
	entering, release := dialer.gate(oldTarget)
	resolver.setAnswers(net.ParseIP(oldIP))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		_, _, err := binder.Dial(ctx, "tcp", testHost, oldPolicy.Port)
		errCh <- err
	}()

	<-entering
	newPolicy := testPolicy(t, []string{newIP}, nil, 2)
	if err := binder.Update(newPolicy); err != nil {
		t.Fatalf("second Update: %v", err)
	}
	close(release)

	if err := <-errCh; !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("stale Dial error = %v, want ErrStaleGeneration", err)
	}
	if !conn.isClosed() {
		t.Fatal("stale-generation connection was not closed")
	}
	if done.count() != 0 {
		t.Fatal("callback fired under a stale generation")
	}

	stale := testPolicy(t, []string{newIP}, nil, 2)
	if err := binder.Update(stale); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("equal-generation Update error = %v, want ErrStaleGeneration", err)
	}

	// New generation dials the new verified set and delivers normally.
	newConn := newFakeConn(newIP, newPolicy.Port)
	newTarget := dialTarget(newIP, newPolicy.Port)
	dialer.setConn(newTarget, newConn)
	resolver.setAnswers(net.ParseIP(newIP))

	deliveredConn, peer, err := binder.Dial(ctx, "tcp", testHost, newPolicy.Port)
	if err != nil {
		t.Fatalf("new-generation Dial: %v", err)
	}
	if peer.String() != newTarget {
		t.Fatalf("peer = %q, want %q", peer, newTarget)
	}
	_ = deliveredConn.Close()
}

// TestHostAndPortBinding proves only the pinned canonical host and port are
// licensed and lookups are case-insensitive.
func TestHostAndPortBinding(t *testing.T) {
	resolver := &fakeResolver{}
	dialer := newFakeDialer()
	binder := NewBinder(Config{Resolver: resolver, Dialer: dialer})

	policy := testPolicy(t, []string{"203.0.113.70"}, nil, 1)
	if err := binder.Update(policy); err != nil {
		t.Fatalf("Update: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, _, err := binder.Dial(ctx, "tcp", "evil.example.com", policy.Port); !errors.Is(err, ErrHostMismatch) {
		t.Fatalf("foreign host error = %v, want ErrHostMismatch", err)
	}
	if _, _, err := binder.Dial(ctx, "tcp", testHost, policy.Port+1); !errors.Is(err, ErrHostMismatch) {
		t.Fatalf("foreign port error = %v, want ErrHostMismatch", err)
	}

	// Upper-case host must canonicalize to the pinned name before lookup.
	resolver.setAnswers(net.ParseIP("203.0.113.70"))
	dialer.setConn(dialTarget("203.0.113.70", policy.Port), newFakeConn("203.0.113.70", policy.Port))
	conn, _, err := binder.Dial(ctx, "tcp", strings.ToUpper(testHost), policy.Port)
	if err != nil {
		t.Fatalf("canonical Dial: %v", err)
	}
	_ = conn.Close()
	if hosts := resolver.queriedHosts(); len(hosts) != 1 || hosts[0] != testHost {
		t.Fatalf("resolver hosts = %v, want [%s]", hosts, testHost)
	}
}

func TestPolicyValidation(t *testing.T) {
	if _, err := NewPolicy("", 443, []net.IP{net.ParseIP("203.0.113.1")}, nil, 1); err == nil {
		t.Fatal("empty host accepted")
	}
	if _, err := NewPolicy("203.0.113.1", 443, []net.IP{net.ParseIP("203.0.113.1")}, nil, 1); err == nil {
		t.Fatal("IP-literal host accepted")
	}
	if _, err := NewPolicy(testHost, 70000, []net.IP{net.ParseIP("203.0.113.1")}, nil, 1); err == nil {
		t.Fatal("out-of-range port accepted")
	}
	if _, err := NewPolicy(testHost, 443, nil, nil, 1); err == nil {
		t.Fatal("empty verified set accepted")
	}
	if _, err := NewPolicy(testHost, 443, []net.IP{net.ParseIP("203.0.113.1")}, []string{"not-a-cidr"}, 1); err == nil {
		t.Fatal("invalid CIDR accepted")
	}

	policy, err := NewPolicy(testHost, 443,
		[]net.IP{net.ParseIP("203.0.113.1"), net.ParseIP("::ffff:203.0.113.1")},
		nil, 3)
	if err != nil {
		t.Fatalf("mapped duplicate policy: %v", err)
	}
	if len(policy.VerifiedIPs) != 1 {
		t.Fatalf("verified set size = %d, want 1 after 4-in-6 unwrap", len(policy.VerifiedIPs))
	}
}

func TestNoPolicyAndUpdateGuards(t *testing.T) {
	binder := NewBinder(Config{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, _, err := binder.Dial(ctx, "tcp", testHost, 443); !errors.Is(err, ErrNoPolicy) {
		t.Fatalf("Dial error = %v, want ErrNoPolicy", err)
	}
	if err := binder.Update(nil); err == nil {
		t.Fatal("nil policy accepted")
	}

	policy := testPolicy(t, []string{"203.0.113.80"}, nil, 1)
	if err := binder.Update(policy); err != nil {
		t.Fatalf("Update: %v", err)
	}
	binder.Revoke()
	if err := binder.Update(testPolicy(t, []string{"203.0.113.81"}, nil, 2)); !errors.Is(err, ErrRevoked) {
		t.Fatalf("Update after Revoke error = %v, want ErrRevoked", err)
	}
	if gen := binder.CurrentGeneration(); gen != -1 {
		t.Fatalf("generation after revoke = %d, want -1", gen)
	}
}
