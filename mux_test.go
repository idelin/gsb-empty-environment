package sshchannel

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

const testTargetHost = "127.0.0.1"

// echoServer returns the port number of a virtual in-memory target. Every
// "connection" to it is a halfPipe pair that echoes bytes until the client
// side half-closes or closes. The port itself only needs to be a stable value
// for the allow-list and pinned-target assertions.
var echoTargetPort uint16 = 8080

func echoServer(t *testing.T) (port uint16, closeFn func()) {
	t.Helper()
	return echoTargetPort, func() {}
}

type harness struct {
	client     *Client
	mux        *Mux
	dialer     *PausableDialer
	serverConn net.Conn
	serveErr   chan error
}

func newHarness(t *testing.T, policy Policy, dialer Dialer) *harness {
	t.Helper()
	serverConn, clientConn, err := dialPipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	mux := NewMux(Config{Session: serverConn, Policy: policy, Dialer: dialer})
	client := NewClient(clientConn)

	h := &harness{
		client:     client,
		mux:        mux,
		serverConn: serverConn,
		serveErr:   make(chan error, 1),
	}
	if pd, ok := dialer.(*PausableDialer); ok {
		h.dialer = pd
	}
	go func() { h.serveErr <- mux.Serve() }()

	t.Cleanup(func() {
		client.Close()
		mux.Close()
		serverConn.Close()
		clientConn.Close()
	})
	return h
}

func dialPipe() (net.Conn, net.Conn, error) {
	a, b := newHalfPipePair()
	return a, b, nil
}

// waitNewCall waits for a dial call beyond the current count.
func waitNewCall(t *testing.T, dialer *PausableDialer, after int) *DialCall {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		calls := dialer.Calls()
		if len(calls) > after {
			call := calls[after]
			select {
			case <-call.Ready():
				return call
			default:
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("expected additional dial call")
	return nil
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting: %s", what)
}

func waitClosed(t *testing.T, ch Channel) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		buf := make([]byte, 8)
		for {
			if _, err := ch.Read(buf); err != nil {
				close(done)
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("channel %d did not close", ch.ID())
	}
}

func TestRevocationRecheckedBeforeAdmission(t *testing.T) {
	port, stop := echoServer(t)
	defer stop()
	policy := NewRulePolicy([]Rule{{HostName: testTargetHost, PortNum: port}})
	dialer := &PausableDialer{}
	h := newHarness(t, policy, dialer)

	open1, call := openAndCall(t, h, dialer, port)

	// Single-grant revocation while the dial is in flight.
	grants := policy.Grants()
	if len(grants) != 1 {
		t.Fatalf("expected 1 grant, got %d", len(grants))
	}
	grants[0].Revoke()

	upstream := dialTarget(t, port)
	call.Resolve(upstream, nil)
	call.Release()
	res1 := waitOpenResult(t, open1)
	if res1.err == nil {
		t.Fatal("revoked grant must reject the open")
	}
	assertConnClosed(t, upstream)

	// A generation bump racing the dial has the same effect.
	open2, call2 := openAndCall(t, h, dialer, port)
	policy.SetRules([]Rule{{HostName: testTargetHost, PortNum: port}})
	upstream2 := dialTarget(t, port)
	call2.Resolve(upstream2, nil)
	call2.Release()
	res2 := waitOpenResult(t, open2)
	if res2.err == nil {
		t.Fatal("stale-generation grant must reject the open")
	}
	assertConnClosed(t, upstream2)

	// Grants under the new generation still admit normally.
	open3, call3 := openAndCall(t, h, dialer, port)
	call3.Resolve(dialTarget(t, port), nil)
	call3.Release()
	res3 := waitOpenResult(t, open3)
	if res3.err != nil {
		t.Fatal(res3.err)
	}
	assertEcho(t, res3.ch)
}

func TestStaleDialCallbackCannotTakeOverRecycledID(t *testing.T) {
	port, stop := echoServer(t)
	defer stop()
	policy := NewRulePolicy([]Rule{{HostName: testTargetHost, PortNum: port}})
	dialer := &PausableDialer{}
	h := newHarness(t, policy, dialer)

	dialer.SetIgnoreDetachCancellation(true)
	open1, staleCall := openAndCall(t, h, dialer, port)

	h.client.mu.Lock()
	var reusedID uint32
	for id := range h.client.channels {
		reusedID = id
	}
	h.client.mu.Unlock()
	pending := h.client.lookup(reusedID)

	// Client gives up before the dial resolves: the host cancels the dial
	// and removes the channel, making the id reusable.
	if err := pending.Close(); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "host registry releases recycled id", func() bool {
		h.mux.mu.Lock()
		_, occupied := h.mux.channels[reusedID]
		h.mux.mu.Unlock()
		return !occupied
	})

	dialer.SetIgnoreDetachCancellation(false)
	open2, newCall := openAndCall(t, h, dialer, port)

	// The old dial resolves late with a connection. It must be discarded.
	staleConn := dialTarget(t, port)
	staleCall.Resolve(staleConn, nil)
	staleCall.Release()

	// The new channel's own dial resolves and admits normally.
	goodConn := dialTarget(t, port)
	newCall.Resolve(goodConn, nil)
	newCall.Release()

	second := waitOpenResult(t, open2)
	if second.err != nil {
		t.Fatal(second.err)
	}
	if second.ch.ID() != reusedID {
		t.Fatalf("expected id reuse, got %d want %d", second.ch.ID(), reusedID)
	}
	assertEcho(t, second.ch)
	assertConnClosed(t, staleConn)

	if len(dialer.Calls()) != 2 {
		t.Fatalf("expected 2 dial calls against the same id, got %d", len(dialer.Calls()))
	}
	if dialer.Calls()[0].Target.Host() != dialer.Calls()[1].Target.Host() ||
		dialer.Calls()[0].Target.Port() != dialer.Calls()[1].Target.Port() {
		t.Fatal("reused channel must keep the pinned target")
	}

	// The abandoned open must surface an error rather than hijack the id.
	select {
	case r := <-open1:
		if r.err == nil {
			t.Fatal("abandoned open must not succeed")
		}
	case <-time.After(time.Second):
		t.Fatal("abandoned open goroutine leaked")
	}
}

func TestMuxCloseCancelsInFlightDial(t *testing.T) {
	port, stop := echoServer(t)
	defer stop()
	policy := NewRulePolicy([]Rule{{HostName: testTargetHost, PortNum: port}})
	dialer := &PausableDialer{}
	h := newHarness(t, policy, dialer)

	open1, call := openAndCall(t, h, dialer, port)

	// Closing the host must cancel the dial context even though the call is
	// still suspended and never released.
	if err := h.mux.Close(); err != nil {
		t.Fatalf("mux close: %v", err)
	}
	waitUntil(t, "in-flight dial context cancellation", func() bool {
		return errors.Is(call.Context().Err(), context.Canceled)
	})

	select {
	case r := <-open1:
		if r.err == nil {
			t.Fatal("open must fail when the session closes")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("open did not unblock after mux close")
	}
}

func TestIndependentGrantsAndRecyclingOrder(t *testing.T) {
	port, stop := echoServer(t)
	defer stop()
	policy := NewRulePolicy([]Rule{{HostName: testTargetHost, PortNum: port}})
	dialer := &PausableDialer{}
	h := newHarness(t, policy, dialer)

	const n = 4
	opens := make([]<-chan openResult, n)
	calls := make([]*DialCall, n)
	for i := 0; i < n; i++ {
		opens[i], calls[i] = openAndCall(t, h, dialer, port)
	}

	grants := policy.Grants()
	serials := map[uint64]bool{}
	for _, grant := range grants {
		if serials[grant.Serial] {
			t.Fatalf("duplicate grant serial %d", grant.Serial)
		}
		serials[grant.Serial] = true
	}

	// All dials fail: every independent grant/channel must fail on its own,
	// and all ids must return to the reusable pool.
	for _, call := range calls {
		call.Resolve(nil, errors.New("dial boom"))
		call.Release()
	}
	for i, open := range opens {
		r := waitOpenResult(t, open)
		if r.err == nil {
			t.Fatalf("open %d should fail with its dial", i)
		}
	}

	// Recycling order: the registry is empty before any dial callback can
	// re-enter, so a new open reuses an id immediately.
	waitUntil(t, "all channels recycled", func() bool {
		h.mux.mu.Lock()
		n := len(h.mux.channels)
		h.mux.mu.Unlock()
		return n == 0
	})

	reopen, nextCall := openAndCall(t, h, dialer, port)
	nextCall.Resolve(dialTarget(t, port), nil)
	nextCall.Release()
	reopened := waitOpenResult(t, reopen)
	if reopened.err != nil {
		t.Fatalf("reopen after failures: %v", reopened.err)
	}
	if reopened.ch.ID() >= n {
		t.Fatalf("expected recycled id, got %d (pool had %d)", reopened.ch.ID(), n)
	}
	assertEcho(t, reopened.ch)
}

func assertReadEOF(t *testing.T, ch Channel) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 8)
		_, err := ch.Read(buf)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("expected EOF, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive expected EOF")
	}
}

func assertConnClosed(t *testing.T, conn net.Conn) {
	t.Helper()
	if hp, ok := conn.(*halfPipe); ok {
		waitUntil(t, "connection marked closed", hp.isClosed)
		return
	}
	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 1)
		_, err := conn.Read(buf)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected closed connection, got successful read")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("connection was not closed")
	}
}

func assertEcho(t *testing.T, ch Channel) {
	t.Helper()
	msg := []byte("hello multiplexed world")
	if _, err := ch.Write(msg); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(ch, got); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(got) != string(msg) {
		t.Fatalf("echo mismatch: got %q want %q", got, msg)
	}
}

// openAsync issues an Open in the background, since Open blocks until the
// host admits or rejects the channel and the pausable dialer keeps that
// decision suspended until the test releases it.
func openAsync(t *testing.T, h *harness, port uint16) <-chan openResult {
	t.Helper()
	result := make(chan openResult, 1)
	go func() {
		ch, err := h.client.Open(testTargetHost, port)
		result <- openResult{ch: ch, err: err}
	}()
	return result
}

type openResult struct {
	ch  Channel
	err error
}

// openAndCall starts an Open and waits for the dial call that belongs to it,
// avoiding confusion with calls from earlier opens sharing the same dialer.
func openAndCall(t *testing.T, h *harness, dialer *PausableDialer, port uint16) (<-chan openResult, *DialCall) {
	t.Helper()
	before := len(dialer.Calls())
	result := openAsync(t, h, port)
	call := waitNewCall(t, dialer, before)
	return result, call
}

func waitOpenResult(t *testing.T, result <-chan openResult) openResult {
	t.Helper()
	select {
	case r := <-result:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("open never resolved")
		return openResult{}
	}
}

func dialTarget(t *testing.T, port uint16) net.Conn {
	t.Helper()
	clientSide, serverSide := newHalfPipePair()
	go func() {
		if _, err := io.Copy(serverSide, serverSide); err == nil {
			// Client half-closed its writes: echo remaining data then signal
			// EOF back so the host relay sends a Half frame.
			_ = serverSide.CloseWrite()
		}
	}()
	return clientSide
}

func TestOpenDeniedSynchronously(t *testing.T) {
	policy := NewRulePolicy([]Rule{{HostName: testTargetHost, PortNum: 1}})
	h := newHarness(t, policy, &PausableDialer{})

	if _, err := h.client.Open(testTargetHost, 22); err == nil {
		t.Fatal("expected denial for unlisted target")
	}
	if len(h.dialer.Calls()) != 0 {
		t.Fatalf("denied open must not trigger a dial, got %d calls", len(h.dialer.Calls()))
	}
}

func TestDialFailureRecyclesID(t *testing.T) {
	port, stop := echoServer(t)
	defer stop()
	policy := NewRulePolicy([]Rule{{HostName: testTargetHost, PortNum: port}})
	dialer := &PausableDialer{}
	h := newHarness(t, policy, dialer)

	open1, call := openAndCall(t, h, dialer, port)

	if got := call.Target.Port(); got != port {
		t.Fatalf("dial target not pinned: port=%d want %d", got, port)
	}
	call.Resolve(nil, errors.New("dial: connection refused"))
	call.Release()

	firstRes := waitOpenResult(t, open1)
	if firstRes.err == nil {
		t.Fatal("expected open failure after failed dial")
	}
	failedID := uint32(0) // first allocation always starts at 0
	if firstRes.ch != nil {
		failedID = firstRes.ch.ID()
	}

	// The id must be reusable immediately; a fresh open gets the same id and
	// a successful dial this time.
	open2 := openAsync(t, h, port)
	nextCall := waitNewCall(t, dialer, 1)
	nextCall.Resolve(dialTarget(t, port), nil)
	nextCall.Release()

	second := waitOpenResult(t, open2)
	if second.err != nil {
		t.Fatalf("second open: %v", second.err)
	}
	if second.ch.ID() != failedID {
		t.Fatalf("id not recycled: got %d want %d", second.ch.ID(), failedID)
	}

	assertEcho(t, second.ch)
}

func TestRelayAndHalfCloseBothDirections(t *testing.T) {
	port, stop := echoServer(t)
	defer stop()
	policy := NewRulePolicy([]Rule{{HostName: testTargetHost, PortNum: port}})
	dialer := &PausableDialer{}
	h := newHarness(t, policy, dialer)

	open1, call := openAndCall(t, h, dialer, port)
	call.Resolve(dialTarget(t, port), nil)
	call.Release()
	res1 := waitOpenResult(t, open1)
	if res1.err != nil {
		t.Fatal(res1.err)
	}
	ch := res1.ch
	assertEcho(t, ch)

	// Target-side half-close: the host relay reads EOF and must surface EOF
	// to the client reader without tearing the session down.
	open2, call2 := openAndCall(t, h, dialer, port)
	upstream2 := dialTarget(t, port)
	call2.Resolve(upstream2, nil)
	call2.Release()
	res2 := waitOpenResult(t, open2)
	if res2.err != nil {
		t.Fatal(res2.err)
	}
	ch2 := res2.ch

	if err := closeWrite(upstream2); err != nil {
		t.Fatal(err)
	}
	assertReadEOF(t, ch2)

	// Client-side half-close reaches the target's read side as EOF while the
	// target can still send data back.
	open3, call3 := openAndCall(t, h, dialer, port)
	upstream3 := dialTarget(t, port)
	call3.Resolve(upstream3, nil)
	call3.Release()
	res3 := waitOpenResult(t, open3)
	if res3.err != nil {
		t.Fatal(res3.err)
	}
	ch3 := res3.ch

	if err := ch3.CloseWrite(); err != nil {
		t.Fatalf("CloseWrite: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 4)
		_, err := upstream3.Read(buf)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("target expected EOF after client half-close, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client half-close did not reach target as EOF")
	}
}
