package netsec

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultMaxEarlyData is the number of client bytes that may arrive before
	// the tunnel is established. Data beyond this bound aborts the request.
	DefaultMaxEarlyData int64 = 64 << 10

	// DefaultEarlyDataIdleTimeout is how long the gateway waits for the first
	// (and each subsequent) early byte before it stops pre-reading.
	DefaultEarlyDataIdleTimeout = 25 * time.Millisecond

	maxAuthorizationAttempts = 4
)

var (
	// ErrInvalidRequest reports a missing target or client connection.
	ErrInvalidRequest = errors.New("netsec: invalid request")

	// ErrInvalidConfig reports a Gateway without the callbacks it needs.
	ErrInvalidConfig = errors.New("netsec: invalid gateway configuration")

	// ErrUnauthorized reports that the target was not authorized.
	ErrUnauthorized = errors.New("netsec: target is not authorized")

	// ErrAuthorizationChanged reports that the policy kept changing while the
	// gateway tried to obtain a stable authorization decision.
	ErrAuthorizationChanged = errors.New("netsec: authorization did not stabilize")

	// ErrEarlyDataTooLarge reports that the client sent more early data than
	// MaxEarlyData allows.
	ErrEarlyDataTooLarge = errors.New("netsec: early tunnel data exceeds limit")

	// ErrResponseWrite reports that the success response could not be
	// completely sent to the client.
	ErrResponseWrite = errors.New("netsec: failed to send success response")

	// ErrDial reports that the upstream connection could not be established.
	ErrDial = errors.New("netsec: upstream dial failed")
)

// Request describes a CONNECT request and the policy revision under which it
// must be authorized. PolicyVersion is used when Gateway.Policy is nil.
type Request struct {
	Target        string
	PolicyVersion string
}

// Decision is the result returned by an AuthorizeFunc.
type Decision struct {
	Allowed bool
	Reason  string
}

// AuthorizeFunc decides whether req.Target may be used. It is called for
// every request; decisions are never cached across targets or policy
// revisions.
type AuthorizeFunc func(context.Context, Request) (Decision, error)

// DialFunc opens a connection to the authorized target. The Request passed to
// it carries the target and policy revision that were actually authorized.
type DialFunc func(context.Context, Request) (net.Conn, error)

// RespondFunc writes the successful CONNECT response. The gateway supplies a
// buffered writer, flushes it, and only then releases pre-read client data to
// the target.
type RespondFunc func(context.Context, io.Writer) error

// Gateway establishes CONNECT tunnels with explicit security barriers:
//
//	authorize -> dial -> send and flush success response -> forward data
//
// Client bytes are pre-read concurrently with authorization, but they are
// bounded by MaxEarlyData and are never written to the target before the
// success response has been completely sent. If the policy revision changes,
// the gateway obtains a fresh decision before proceeding through the next
// barrier.
type Gateway struct {
	Authorize AuthorizeFunc
	Dial      DialFunc
	Respond   RespondFunc

	// Policy returns the current policy revision. If nil, Request.PolicyVersion
	// is used. A change observed by the gateway forces reauthorization.
	Policy func() string

	// MaxEarlyData bounds the bytes buffered while the tunnel is being
	// established. Values <= 0 use DefaultMaxEarlyData.
	MaxEarlyData int64

	// EarlyDataIdleTimeout bounds how long pre-reading waits for more early
	// bytes. Values <= 0 use DefaultEarlyDataIdleTimeout.
	EarlyDataIdleTimeout time.Duration
}

// Serve authorizes and establishes a tunnel for req, then forwards data in
// both directions until either side closes or ctx is canceled. The client
// connection is always closed before Serve returns.
//
// No client payload is written to the target until authorization has
// succeeded, Dial has returned a connection, the Respond callback has returned
// successfully, and the response writer has been flushed.
func (g *Gateway) Serve(ctx context.Context, client net.Conn, req Request) error {
	if client == nil {
		return ErrInvalidRequest
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var conns closeSet
	conns.add(client)
	stopCancellation := context.AfterFunc(ctx, conns.closeAll)
	defer stopCancellation()
	defer conns.closeAll()

	if g == nil {
		return ErrInvalidConfig
	}
	req.Target = strings.TrimSpace(req.Target)
	if req.Target == "" {
		return ErrInvalidRequest
	}
	if g.Authorize == nil || g.Dial == nil || g.Respond == nil {
		return ErrInvalidConfig
	}

	early := startEarlyRead(ctx, client, g.maxEarlyData(), g.earlyDataIdleTimeout())

	grant, err := g.authorize(ctx, req)
	if err != nil {
		return err
	}

	var earlyResult preReadResult
	select {
	case earlyResult = <-early.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	if earlyResult.err != nil {
		return earlyResult.err
	}

	// Pre-reading may have taken long enough for the policy to change.
	grant, err = g.revalidate(ctx, req, grant)
	if err != nil {
		return err
	}

	target, err := g.Dial(ctx, Request{
		Target:        grant.target,
		PolicyVersion: grant.policy,
	})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrDial, err)
	}
	if target == nil {
		return fmt.Errorf("%w: dialer returned a nil connection", ErrDial)
	}
	conns.add(target)

	// Dialing can be slow; authorization must still match the current policy.
	grant, err = g.revalidate(ctx, req, grant)
	if err != nil {
		return err
	}

	if err := g.respond(ctx, client); err != nil {
		return fmt.Errorf("%w: %w", ErrResponseWrite, err)
	}

	// A success response is an irreversible commitment. Re-check authorization
	// once more before any buffered client data is released.
	grant, err = g.revalidate(ctx, req, grant)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	if len(earlyResult.data) > 0 {
		if err := writeAll(target, earlyResult.data); err != nil {
			return fmt.Errorf("netsec: deliver early data: %w", err)
		}
	}

	return g.forward(ctx, client, target)
}

// WriteConnectionEstablished writes the conventional successful CONNECT
// response. It is suitable for use as a RespondFunc body.
func WriteConnectionEstablished(w io.Writer) error {
	_, err := io.WriteString(w, "HTTP/1.1 200 Connection Established\r\n\r\n")
	return err
}

type grant struct {
	target string
	policy string
}

func (g *Gateway) authorize(ctx context.Context, req Request) (grant, error) {
	for attempt := 0; attempt < maxAuthorizationAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return grant{}, err
		}

		policy := g.policyVersion(req)
		decision, err := g.Authorize(ctx, Request{
			Target:        req.Target,
			PolicyVersion: policy,
		})
		if err != nil {
			return grant{}, fmt.Errorf("netsec: authorize: %w", err)
		}
		if !decision.Allowed {
			if decision.Reason != "" {
				return grant{}, fmt.Errorf("%w: %s", ErrUnauthorized, decision.Reason)
			}
			return grant{}, ErrUnauthorized
		}

		// The decision is valid only for the policy revision it was made
		// against. If the revision moved while Authorize was running, repeat.
		if g.policyVersion(req) == policy {
			return grant{target: req.Target, policy: policy}, nil
		}
	}

	return grant{}, ErrAuthorizationChanged
}

func (g *Gateway) revalidate(ctx context.Context, req Request, current grant) (grant, error) {
	if err := ctx.Err(); err != nil {
		return grant{}, err
	}
	if current.target == req.Target && current.policy == g.policyVersion(req) {
		return current, nil
	}
	return g.authorize(ctx, req)
}

func (g *Gateway) policyVersion(req Request) string {
	if g.Policy != nil {
		return g.Policy()
	}
	return req.PolicyVersion
}

func (g *Gateway) maxEarlyData() int64 {
	if g.MaxEarlyData > 0 {
		return g.MaxEarlyData
	}
	return DefaultMaxEarlyData
}

func (g *Gateway) earlyDataIdleTimeout() time.Duration {
	if g.EarlyDataIdleTimeout > 0 {
		return g.EarlyDataIdleTimeout
	}
	return DefaultEarlyDataIdleTimeout
}

type preReadResult struct {
	data []byte
	err  error
}

type earlyRead struct {
	done <-chan preReadResult
}

func startEarlyRead(ctx context.Context, client net.Conn, limit int64, idle time.Duration) earlyRead {
	done := make(chan preReadResult, 1)
	go func() {
		data, err := readEarlyData(ctx, client, limit, idle)
		done <- preReadResult{data: data, err: err}
	}()
	return earlyRead{done: done}
}

// readEarlyData captures bytes that are already available on the client
// connection. It stops after a short idle period, the client closing, context
// cancellation, or the configured bound. At most limit+1 bytes are allocated
// so an oversized early payload can be detected without unbounded buffering.
func readEarlyData(ctx context.Context, client net.Conn, limit int64, idle time.Duration) ([]byte, error) {
	if limit <= 0 {
		limit = DefaultMaxEarlyData
	}
	if idle <= 0 {
		idle = DefaultEarlyDataIdleTimeout
	}

	defer func() {
		_ = client.SetReadDeadline(time.Time{})
	}()

	capacity := limit + 1
	if capacity > 32<<10 {
		capacity = 32 << 10
	}
	data := make([]byte, 0, int(capacity))
	chunk := make([]byte, 16<<10)

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		remaining := limit + 1 - int64(len(data))
		if remaining <= 0 {
			return nil, ErrEarlyDataTooLarge
		}
		size := int64(len(chunk))
		if remaining < size {
			size = remaining
		}

		if err := client.SetReadDeadline(time.Now().Add(idle)); err != nil {
			return nil, fmt.Errorf("netsec: set early-data deadline: %w", err)
		}

		n, err := client.Read(chunk[:int(size)])
		if n > 0 {
			data = append(data, chunk[:n]...)
			if int64(len(data)) > limit {
				return nil, ErrEarlyDataTooLarge
			}
		}
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			if isTimeout(err) {
				return data, nil
			}
			if errors.Is(err, io.EOF) {
				return nil, fmt.Errorf("netsec: client closed before tunnel was established: %w", io.EOF)
			}
			return nil, fmt.Errorf("netsec: read early data: %w", err)
		}
	}
}

func isTimeout(err error) bool {
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func (g *Gateway) respond(ctx context.Context, client net.Conn) error {
	w := bufio.NewWriterSize(client, 4<<10)
	if err := g.Respond(ctx, w); err != nil {
		return err
	}
	return w.Flush()
}

func (g *Gateway) forward(ctx context.Context, client, target net.Conn) error {
	results := make(chan error, 2)
	go func() { results <- copyTunnel(target, client) }()
	go func() { results <- copyTunnel(client, target) }()

	first := <-results
	_ = client.Close()
	_ = target.Close()
	second := <-results

	if err := ctx.Err(); err != nil {
		return err
	}
	for _, err := range []error{first, second} {
		if err != nil && !isBenignClose(err) {
			return fmt.Errorf("netsec: forward: %w", err)
		}
	}
	return nil
}

func copyTunnel(dst, src net.Conn) error {
	_, err := io.Copy(dst, src)
	return err
}

func isBenignClose(err error) bool {
	return errors.Is(err, net.ErrClosed) || errors.Is(err, os.ErrClosed)
}

func writeAll(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if n > 0 {
			p = p[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

type closeSet struct {
	mu     sync.Mutex
	closed bool
	conns  []io.Closer
}

func (s *closeSet) add(c io.Closer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		_ = c.Close()
		return
	}
	s.conns = append(s.conns, c)
}

func (s *closeSet) closeAll() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	conns := s.conns
	s.conns = nil
	s.mu.Unlock()

	for _, c := range conns {
		_ = c.Close()
	}
}
