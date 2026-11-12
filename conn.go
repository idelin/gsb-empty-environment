package netsec

import (
	"sync"
	"sync/atomic"
)

// Authenticator validates a peer's handshake response. Implementations
// verify that the response proves possession of the challenge and matches
// the credentials captured when the connection was created, then invoke
// result with nil on success or a non-nil error to reject the connection.
//
// The callback is intentionally asynchronous: a transport may deliver the
// authentication response (or a failure notice) at any later time. Calling
// result more than once, or calling it after the connection was closed or
// its endpoint revoked, has no effect.
type Authenticator interface {
	Authenticate(creds PeerCreds, challenge Challenge, result func(error))
}

// AuthenticatorFunc adapts a plain function to the Authenticator interface.
type AuthenticatorFunc func(creds PeerCreds, challenge Challenge, result func(error))

// Authenticate implements Authenticator.
func (f AuthenticatorFunc) Authenticate(creds PeerCreds, challenge Challenge, result func(error)) {
	f(creds, challenge, result)
}

// SyncAuthenticator wraps a synchronous verification function so it runs in
// its own goroutine and reports through the asynchronous callback contract.
type SyncAuthenticator struct {
	Verify func(creds PeerCreds, challenge Challenge) error
}

// Authenticate implements Authenticator.
func (s SyncAuthenticator) Authenticate(creds PeerCreds, challenge Challenge, result func(error)) {
	go func() { result(s.Verify(creds, challenge)) }()
}

// Message is an application payload delivered over an authenticated
// connection. It carries the fixed instance identity the connection was
// bound to, so receivers always know which endpoint generation handled the
// message even after the name has been re-registered.
type Message struct {
	Endpoint string
	Instance InstanceID
	Peer     PeerCreds
	Payload  []byte
}

// Conn is a transport connection bound at creation time to one endpoint
// instance, one peer credential snapshot and one handshake challenge. The
// binding is immutable for the life of the connection.
type Conn struct {
	ep        *Endpoint
	instance  InstanceID
	creds     PeerCreds
	challenge Challenge
	auth      Authenticator

	mu       sync.Mutex
	closed   bool
	authDone bool
	authErr  error
	waiters  []chan error
	doneCh   chan struct{}

	completed atomic.Bool
}

// Endpoint returns the endpoint instance this connection is bound to. The
// pointer always refers to the generation that accepted the connection,
// never to a later same-name replacement.
func (c *Conn) Endpoint() *Endpoint { return c.ep }

// Instance returns the fixed instance identity of the bound endpoint.
func (c *Conn) Instance() InstanceID { return c.instance }

// PeerCreds returns the credentials captured when the connection was
// created.
func (c *Conn) PeerCreds() PeerCreds { return c.creds }

// Challenge returns the handshake challenge issued for this connection.
func (c *Conn) Challenge() Challenge { return c.challenge }

// authed is the one-shot callback handed to the Authenticator. It is
// idempotent: duplicate invocations are ignored, and an invocation after
// Close cannot resurrect the connection. A callback landing after
// revocation still records its verdict, but Deliver rejects every message
// on the revoked instance regardless.
func (c *Conn) authed(err error) {
	c.mu.Lock()
	if c.closed || c.authDone {
		c.mu.Unlock()
		return
	}
	c.authDone = true
	c.authErr = err
	waiters := c.waiters
	c.waiters = nil
	c.mu.Unlock()

	for _, waiter := range waiters {
		close(waiter)
	}
}

// AuthDone reports whether the authentication callback has completed.
func (c *Conn) AuthDone() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.authDone
}

// WaitAuth blocks until the authentication callback completes or the
// connection closes, then returns the authentication error (nil means
// accepted). Endpoint revocation does not cancel a pending callback: the
// verdict is still recorded, and Deliver separately rejects messages on
// revoked instances.
func (c *Conn) WaitAuth() error {
	c.mu.Lock()
	if c.authDone {
		err := c.authErr
		c.mu.Unlock()
		return err
	}
	if c.closed {
		c.mu.Unlock()
		return ErrConnClosed
	}
	waiter := make(chan error)
	c.waiters = append(c.waiters, waiter)
	if c.doneCh == nil {
		c.doneCh = make(chan struct{})
	}
	done := c.doneCh
	c.mu.Unlock()

	select {
	case <-waiter:
		c.mu.Lock()
		err := c.authErr
		c.mu.Unlock()
		return err
	case <-done:
		return ErrConnClosed
	}
}

// Close terminates the connection. Pending authentication callbacks become
// no-ops; no further messages are accepted. Already accepted messages stay
// queued on the bound endpoint instance.
func (c *Conn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	waiters := c.waiters
	c.waiters = nil
	if c.doneCh == nil {
		c.doneCh = make(chan struct{})
	}
	done := c.doneCh
	c.mu.Unlock()

	close(done)
	for _, waiter := range waiters {
		close(waiter)
	}

	c.ep.mu.Lock()
	delete(c.ep.conns, c)
	c.ep.mu.Unlock()
	return nil
}

// Closed reports whether the connection has been closed.
func (c *Conn) Closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// Completed reports whether at least one message has been admitted on the
// connection.
func (c *Conn) Completed() bool { return c.completed.Load() }

// Deliver submits a message on the connection. It enforces the full
// admission order:
//
//  1. the connection must still be open;
//  2. the authentication callback must have completed successfully;
//  3. the bound endpoint instance must not have been revoked or replaced.
//
// The connection can never rebound to a same-name replacement, so the
// instance identity checked here is the one fixed at creation. Once
// admitted the message is recorded against that fixed instance, so work
// already received can complete even if the name is revoked afterwards.
func (c *Conn) Deliver(payload []byte) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrConnClosed
	}
	if !c.authDone {
		c.mu.Unlock()
		return ErrConnUnauthenticated
	}
	if c.authErr != nil {
		err := c.authErr
		c.mu.Unlock()
		return err
	}
	c.mu.Unlock()

	ep := c.ep
	ep.mu.Lock()
	if ep.revoked {
		ep.mu.Unlock()
		return ErrEndpointRevoked
	}
	if !ep.instance.Equal(c.instance) {
		// Unreachable by construction: the connection never rebinds. Kept
		// as a defense-in-depth check of the identity invariant.
		ep.mu.Unlock()
		return ErrConnRejected
	}
	msg := Message{
		Endpoint: ep.name,
		Instance: c.instance,
		Peer:     c.creds,
		Payload:  append([]byte(nil), payload...),
	}
	ep.inbox = append(ep.inbox, msg)
	ep.cond.Broadcast()
	ep.mu.Unlock()

	c.completed.Store(true)
	return nil
}
