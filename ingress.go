package netsec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
)

// DefaultMaxEarlyData bounds bytes read from a client before the target has
// been authorized, dialed, and answered with a successful CONNECT response.
const DefaultMaxEarlyData = 32 << 10

const maxConfigRetries = 16

// Conn is the client side of a tunnel. net.Conn values satisfy it.
type Conn interface {
	io.Reader
	io.Writer
	io.Closer
}

// Authorizer decides whether a target may be dialed under a policy snapshot.
// A nil error authorizes the exact Request it is given.
type Authorizer interface {
	Authorize(context.Context, Request) error
}

// AuthorizerFunc adapts a function to Authorizer.
type AuthorizerFunc func(context.Context, Request) error

func (f AuthorizerFunc) Authorize(ctx context.Context, req Request) error {
	if f == nil {
		return errors.New("netsec: nil authorizer denies all targets")
	}
	return f(ctx, req)
}

// Dialer opens the authorized target.
type Dialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

// DialerFunc adapts a function to Dialer.
type DialerFunc func(context.Context, string, string) (net.Conn, error)

func (f DialerFunc) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if f == nil {
		return nil, errors.New("netsec: nil dialer")
	}
	return f(ctx, network, address)
}

// Ingress admits CONNECT tunnels. Its barriers are ordered and non-optional:
//
//  1. authorization succeeds;
//  2. the authorized configuration revision is still current;
//  3. the target is dialed;
//  4. the revision is checked again;
//  5. a complete successful response is written;
//  6. the revision is checked one final time;
//  7. only then is bounded early data flushed to the target.
//
// A target change or policy change invalidates the snapshot and sends the
// tunnel back through authorization before any target byte is forwarded.
type Ingress struct {
	Authorizer Authorizer
	Dialer     Dialer
	Responder  Responder

	// MaxEarlyData is the pre-authorization read cap. Zero selects
	// DefaultMaxEarlyData.
	MaxEarlyData int

	// ConfigSource, when non-nil, is authoritative for the target and policy
	// of every tunnel. Its revision is checked at each commit barrier.
	ConfigSource ConfigSource
}

// Option configures an Ingress.
type Option func(*Ingress)

// WithAuthorizer sets the authorization callback.
func WithAuthorizer(authorizer Authorizer) Option {
	return func(in *Ingress) { in.Authorizer = authorizer }
}

// WithAuthorizeFunc sets the authorization callback.
func WithAuthorizeFunc(fn func(context.Context, Request) error) Option {
	return WithAuthorizer(AuthorizerFunc(fn))
}

// WithDialer sets the target dialer.
func WithDialer(dialer Dialer) Option {
	return func(in *Ingress) { in.Dialer = dialer }
}

// WithDialFunc sets the target dialer.
func WithDialFunc(fn func(context.Context, string, string) (net.Conn, error)) Option {
	return WithDialer(DialerFunc(fn))
}

// WithResponder sets the CONNECT response writer.
func WithResponder(responder Responder) Option {
	return func(in *Ingress) { in.Responder = responder }
}

// WithResponseWriter sets only the successful CONNECT response writer.
func WithResponseWriter(fn func(context.Context, io.Writer) error) Option {
	return func(in *Ingress) {
		in.Responder = ResponderFuncs{Success: fn}
	}
}

// WithMaxEarlyData sets the pre-authorization read cap.
func WithMaxEarlyData(n int) Option {
	return func(in *Ingress) { in.MaxEarlyData = n }
}

// WithConfigSource sets the authoritative target/policy source.
func WithConfigSource(source ConfigSource) Option {
	return func(in *Ingress) { in.ConfigSource = source }
}

// NewIngress returns an ingress. Missing authorization denies every tunnel;
// the default dialer is net.Dialer and the default responder writes plain
// HTTP/1.1 CONNECT responses.
func NewIngress(options ...Option) *Ingress {
	in := &Ingress{}
	for _, option := range options {
		if option != nil {
			option(in)
		}
	}
	return in
}

// NewServer is an alias for NewIngress.
func NewServer(options ...Option) *Ingress {
	return NewIngress(options...)
}

// Serve admits one already-parsed CONNECT request. The client stream starts at
// the first byte after the request headers, so bytes sent early remain in this
// stream and are held by the bounded prefetch queue.
//
// Serve returns as soon as the tunnel closes or admission fails. It is safe to
// change ConfigSource while Serve is blocked on a callback: a revision change
// forces another authorization round before the target is dialed.
func (in *Ingress) Serve(ctx context.Context, client Conn, req Request) error {
	if in == nil {
		return errors.New("netsec: nil ingress")
	}
	if client == nil {
		return errors.New("netsec: nil client")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer client.Close()

	stopClientClose := context.AfterFunc(ctx, func() {
		_ = client.Close()
	})
	defer stopClientClose()

	queue := newPrefetchQueue(in.MaxEarlyData)
	go queue.readFrom(client)

	var target net.Conn
	defer func() {
		if target != nil {
			_ = target.Close()
		}
		queue.close()
	}()

	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if attempt >= maxConfigRetries {
			if err := ctx.Err(); err != nil {
				return err
			}
			in.writeFailure(ctx, client, 503)
			return ErrConfigChanged
		}

		config, revision, err := in.snapshot(req)
		if err != nil {
			in.writeFailure(ctx, client, 400)
			return err
		}

		authRequest := req
		authRequest.Target = config.Target
		authRequest.Policy = config.Policy
		authRequest.Revision = revision

		if err := runWithContext(ctx, func() error {
			return in.authorizer().Authorize(ctx, authRequest)
		}); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			in.writeFailure(ctx, client, 403)
			return fmt.Errorf("%w: %v", ErrUnauthorized, err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		// A target or policy change between authorization and dial requires a
		// fresh authorization of the new snapshot.
		if !in.currentRevision(revision) {
			continue
		}

		conn, err := dialWithContext(ctx, in.dialer(), config.Target)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			in.writeFailure(ctx, client, 502)
			return fmt.Errorf("%w: %v", ErrDialFailed, err)
		}
		if conn == nil {
			in.writeFailure(ctx, client, 502)
			return fmt.Errorf("%w: dialer returned a nil connection", ErrDialFailed)
		}
		if err := ctx.Err(); err != nil {
			_ = conn.Close()
			return err
		}

		// The old target must receive zero bytes if the snapshot changed while
		// the dial was in flight.
		if !in.currentRevision(revision) {
			_ = conn.Close()
			continue
		}

		// WriteSuccess returning is the data-forwarding barrier.
		if err := runWithContext(ctx, func() error {
			return in.responder().WriteSuccess(ctx, client)
		}); err != nil {
			_ = conn.Close()
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return fmt.Errorf("%w: %v", ErrResponseFailed, err)
		}
		if err := ctx.Err(); err != nil {
			_ = conn.Close()
			return err
		}

		// There is no safe way to change destinations after a 200 response.
		// Abort without forwarding rather than send bytes to a target whose
		// authorization no longer covers the current configuration.
		if !in.currentRevision(revision) {
			_ = conn.Close()
			return ErrConfigChanged
		}

		target = conn
		if err := queue.flush(conn); err != nil {
			return fmt.Errorf("netsec: flushing early tunnel data: %w", err)
		}
		return in.transfer(ctx, client, target, queue)
	}
}

// ServeTarget is a convenience wrapper for callers that already know the
// target and policy.
func (in *Ingress) ServeTarget(ctx context.Context, client Conn, target Target, policy Policy) error {
	return in.Serve(ctx, client, Request{Target: target, Policy: policy})
}

func (in *Ingress) snapshot(req Request) (Config, uint64, error) {
	if in.ConfigSource != nil {
		config, revision := in.ConfigSource.Snapshot()
		if err := config.Target.Validate(); err != nil {
			return Config{}, revision, err
		}
		config.Target = config.Target.normalized()
		return config, revision, nil
	}
	config := Config{Target: req.Target.normalized(), Policy: req.Policy}
	if err := config.Target.Validate(); err != nil {
		return Config{}, 0, err
	}
	return config, 0, nil
}

func (in *Ingress) currentRevision(revision uint64) bool {
	if in.ConfigSource == nil {
		return true
	}
	_, current := in.ConfigSource.Snapshot()
	return current == revision
}

func (in *Ingress) authorizer() Authorizer {
	if in.Authorizer == nil {
		return AuthorizerFunc(nil)
	}
	return in.Authorizer
}

func (in *Ingress) dialer() Dialer {
	if in.Dialer == nil {
		return DialerFunc(func(ctx context.Context, network, address string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, address)
		})
	}
	return in.Dialer
}

func (in *Ingress) responder() Responder {
	if in.Responder == nil {
		return DefaultResponder{}
	}
	return in.Responder
}

func (in *Ingress) writeFailure(ctx context.Context, client io.Writer, status int) {
	if err := ctx.Err(); err != nil {
		return
	}
	_ = runWithContext(ctx, func() error {
		return in.responder().WriteFailure(ctx, client, status)
	})
}

func runWithContext(ctx context.Context, fn func() error) error {
	result := make(chan error, 1)
	go func() {
		result <- fn()
	}()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func dialWithContext(ctx context.Context, dialer Dialer, target Target) (net.Conn, error) {
	target = target.normalized()
	type dialResult struct {
		conn net.Conn
		err  error
	}
	result := make(chan dialResult, 1)
	go func() {
		conn, err := dialer.DialContext(ctx, target.Network, target.Address)
		select {
		case result <- dialResult{conn: conn, err: err}:
		case <-ctx.Done():
			if conn != nil {
				_ = conn.Close()
			}
		}
	}()
	select {
	case res := <-result:
		return res.conn, res.err
	case <-ctx.Done():
		// The dialer may ignore cancellation. Consume its eventual result so a
		// connection cannot escape without being closed.
		go func() {
			res := <-result
			if res.conn != nil {
				_ = res.conn.Close()
			}
		}()
		return nil, ctx.Err()
	}
}

func (in *Ingress) transfer(ctx context.Context, client Conn, target net.Conn, queue *prefetchQueue) error {
	stop := context.AfterFunc(ctx, func() {
		_ = target.Close()
		_ = client.Close()
	})
	defer stop()

	up := make(chan error, 1)
	down := make(chan error, 1)
	go func() { up <- copyStream(client, target) }()
	go func() { down <- queue.forward(target) }()

	select {
	case err := <-up:
		if ctxErr := ctx.Err(); ctxErr != nil {
			_ = target.Close()
			_ = client.Close()
			<-down
			return ctxErr
		}
		err = normalizeStreamError(err)
		if err != nil {
			_ = target.Close()
			_ = client.Close()
			<-down
			return err
		}
		closeWrite(client)
		downErr := normalizeStreamError(<-down)
		if ctxErr := ctx.Err(); ctxErr != nil {
			_ = target.Close()
			return ctxErr
		}
		if downErr != nil {
			_ = target.Close()
			return downErr
		}
		closeWrite(target)
		return nil
	case err := <-down:
		if ctxErr := ctx.Err(); ctxErr != nil {
			_ = target.Close()
			_ = client.Close()
			<-up
			return ctxErr
		}
		err = normalizeStreamError(err)
		if err != nil {
			_ = target.Close()
			_ = client.Close()
			<-up
			return err
		}
		closeWrite(target)
		upErr := normalizeStreamError(<-up)
		if ctxErr := ctx.Err(); ctxErr != nil {
			_ = client.Close()
			return ctxErr
		}
		if upErr != nil {
			_ = client.Close()
			return upErr
		}
		closeWrite(client)
		return nil
	}
}

func copyStream(dst io.Writer, src io.Reader) error {
	buf := make([]byte, maxReadChunk)
	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			if writeErr := writeAll(dst, buf[:n]); writeErr != nil {
				return writeErr
			}
		}
		if readErr != nil {
			return normalizeStreamError(readErr)
		}
	}
}

func normalizeStreamError(err error) error {
	if err == nil || err == io.EOF || errors.Is(err, net.ErrClosed) || errors.Is(err, io.ErrClosedPipe) {
		return nil
	}
	return err
}

func closeWrite(value any) {
	if closer, ok := value.(interface{ CloseWrite() error }); ok {
		_ = closer.CloseWrite()
	}
}
