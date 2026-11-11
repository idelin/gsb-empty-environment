package cdpmux

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// mockConn is an in-memory Transport. Tests push inbound frames with push and
// inspect outbound frames via the sent channel.
type mockConn struct {
	mu          sync.Mutex
	inbox       chan []byte
	sent        chan []byte
	closed      bool
	err         error
	closeSignal chan struct{}
	closeOnce   sync.Once
}

func newMockConn() *mockConn {
	return &mockConn{
		inbox:       make(chan []byte, 128),
		sent:        make(chan []byte, 128),
		closeSignal: make(chan struct{}),
	}
}

func (c *mockConn) Send(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("mockConn: send on closed connection")
	}
	c.sent <- append([]byte(nil), data...)
	return nil
}

func (c *mockConn) Recv() ([]byte, error) {
	// Drain queued frames first so tests that close after injecting messages
	// still get deterministic delivery.
	select {
	case frame := <-c.inbox:
		return frame, nil
	default:
	}
	select {
	case frame := <-c.inbox:
		return frame, nil
	case <-c.closeSignal:
		c.mu.Lock()
		err := c.err
		c.mu.Unlock()
		return nil, err
	}
}

func (c *mockConn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	if c.err == nil {
		c.err = errors.New("mockConn: connection reset")
	}
	c.mu.Unlock()
	c.closeOnce.Do(func() { close(c.closeSignal) })
	return nil
}

// fail simulates an abrupt transport failure and wakes any blocked Recv.
func (c *mockConn) fail(err error) {
	c.mu.Lock()
	c.closed = true
	c.err = err
	c.mu.Unlock()
	c.closeOnce.Do(func() { close(c.closeSignal) })
}

func (c *mockConn) push(msg Message) {
	frame, err := json.Marshal(msg)
	if err != nil {
		panic(err)
	}
	c.inbox <- frame
}

func waitSent(t *testing.T, c *mockConn, want int) []Message {
	t.Helper()
	out := make([]Message, 0, want)
	deadline := time.After(2 * time.Second)
	for len(out) < want {
		select {
		case frame := <-c.sent:
			var msg Message
			if err := json.Unmarshal(frame, &msg); err != nil {
				t.Fatalf("malformed outbound frame: %v", err)
			}
			out = append(out, msg)
		case <-deadline:
			t.Fatalf("timed out waiting for %d outbound frames, got %d", want, len(out))
		}
	}
	return out
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

// TestOutOfOrderResponses verifies responses are matched by command id rather
// than arrival order.
func TestOutOfOrderResponses(t *testing.T) {
	conn := newMockConn()
	mux := New(conn)
	defer mux.Close()

	ctx := context.Background()
	type callResult struct {
		out json.RawMessage
		err error
	}
	results := make([]callResult, 3)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out, err := mux.Send(ctx, fmt.Sprintf("Test.method%d", i), nil)
			results[i] = callResult{out, err}
		}(i)
	}

	sent := waitSent(t, conn, 3)
	idByMethod := map[string]int64{}
	seenIDs := map[int64]bool{}
	for _, msg := range sent {
		idByMethod[msg.Method] = msg.ID
		if seenIDs[msg.ID] {
			t.Fatalf("duplicate command id %d", msg.ID)
		}
		seenIDs[msg.ID] = true
	}

	// Reply out of order: 2, then 1, then 3.
	conn.push(Message{ID: idByMethod["Test.method1"], Result: mustMarshal(t, map[string]int{"v": 20})})
	conn.push(Message{ID: idByMethod["Test.method0"], Result: mustMarshal(t, map[string]int{"v": 10})})
	conn.push(Message{ID: idByMethod["Test.method2"], Result: mustMarshal(t, map[string]int{"v": 30})})

	wg.Wait()

	want := []int{10, 20, 30}
	for i, r := range results {
		if r.err != nil {
			t.Fatalf("call %d: unexpected error %v", i, r.err)
		}
		var got map[string]int
		if err := json.Unmarshal(r.out, &got); err != nil {
			t.Fatalf("call %d: unmarshal result: %v", i, err)
		}
		if got["v"] != want[i] {
			t.Fatalf("call %d: got %v, want v=%d", i, got, want[i])
		}
	}
}

// TestEventRoutingAndSlowSubscriber covers event routing by sessionId, a slow
// subscriber that drops events without blocking other sessions, and events
// arriving after detach.
func TestEventRoutingAndSlowSubscriber(t *testing.T) {
	conn := newMockConn()
	var dropMu sync.Mutex
	var unknownDrops []string
	mux := New(conn, WithEventBuffer(2), WithLogger(func(format string, args ...any) {
		if len(args) >= 2 && format == "cdpmux: dropping event %s for unknown session %s" {
			dropMu.Lock()
			unknownDrops = append(unknownDrops, args[1].(string))
			dropMu.Unlock()
		}
	}))
	defer mux.Close()

	sessA := mux.getOrCreateSession("sess-a")
	sessB := mux.getOrCreateSession("sess-b")

	// Interleaved events go to the right subscriber regardless of order.
	conn.push(Message{Method: "Page.load", SessionID: "sess-b",
		Params: mustMarshal(t, map[string]string{"who": "b1"})})
	conn.push(Message{Method: "Page.load", SessionID: "sess-a",
		Params: mustMarshal(t, map[string]string{"who": "a1"})})

	evA := <-sessA.Events()
	evB := <-sessB.Events()
	if evA.SessionID != "sess-a" || evB.SessionID != "sess-b" {
		t.Fatalf("events routed to wrong sessions: %+v %+v", evA, evB)
	}

	// sess-a stops reading: overflow its 2-slot buffer. The read loop must not
	// block and sess-b must keep receiving.
	for i := 0; i < 5; i++ {
		conn.push(Message{Method: "Page.tick", SessionID: "sess-b",
			Params: mustMarshal(t, map[string]int{"i": i})})
		conn.push(Message{Method: "Page.tick", SessionID: "sess-a",
			Params: mustMarshal(t, map[string]int{"i": i})})
		// Let the read loop process this pair before the next push so sess-b
		// never gets starved behind a full sess-a buffer.
		select {
		case ev := <-sessB.Events():
			if ev.SessionID != "sess-b" {
				t.Fatalf("sess-b received foreign event %+v", ev)
			}
		case <-time.After(time.Second):
			t.Fatalf("sess-b blocked by slow sess-a at event %d", i)
		}
	}

	waitFor(t, func() bool { return sessA.Dropped() >= 3 }, "sess-a drop counter")

	// Detach sess-a; a late event must be dropped instead of resurrecting it.
	mux.removeSession("sess-a", ErrSessionDetached)
	select {
	case <-sessA.Done():
	case <-time.After(time.Second):
		t.Fatal("sess-a Done not closed after detach")
	}
	if !errors.Is(sessA.Err(), ErrSessionDetached) {
		t.Fatalf("sess-a err = %v, want ErrSessionDetached", sessA.Err())
	}

	// An event for a session that never existed is dropped without blocking.
	conn.push(Message{Method: "Page.stranger", SessionID: "sess-unknown",
		Params: mustMarshal(t, map[string]string{"late": "true"})})
	conn.push(Message{Method: "Page.alive", SessionID: "sess-b",
		Params: mustMarshal(t, map[string]string{"late": "false"})})

	select {
	case ev := <-sessB.Events():
		if ev.Method != "Page.alive" {
			t.Fatalf("sess-b got unexpected event %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("sess-b did not receive post-detach event")
	}

	if _, ok := mux.Session("sess-a"); ok {
		t.Fatal("detached sess-a still registered")
	}
	if got := sessA.Dropped(); got != 3 {
		t.Fatalf("sess-a dropped = %d, want 3 overflow events", got)
	}

	// An event arriving strictly after detach is counted as dropped on the
	// ended session (logged against the unknown id) and never resurrects it.
	conn.push(Message{Method: "Page.ghost", SessionID: "sess-a",
		Params: mustMarshal(t, map[string]string{"late": "true"})})
	waitFor(t, func() bool {
		dropMu.Lock()
		defer dropMu.Unlock()
		for _, id := range unknownDrops {
			if id == "sess-a" {
				return true
			}
		}
		return false
	}, "late event drop for sess-a")
	if _, ok := mux.Session("sess-a"); ok {
		t.Fatal("late event re-registered detached sess-a")
	}

	// Sending on a detached session fails immediately.
	_, err := sessA.Send(context.Background(), "Page.navigate", nil)
	if !errors.Is(err, ErrSessionDetached) {
		t.Fatalf("send on detached session: %v", err)
	}
}

// TestDisconnectFailsAllCalls ensures a transport failure ends every waiting
// call with the same ErrConnClosed-wrapped error and tears down every session.
func TestDisconnectFailsAllCalls(t *testing.T) {
	conn := newMockConn()
	mux := New(conn)

	sess := mux.getOrCreateSession("sess-x")

	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			switch i {
			case 0:
				_, errs[i] = mux.Send(context.Background(), "Browser.version", nil)
			case 1:
				_, errs[i] = sess.Send(context.Background(), "Runtime.evaluate", nil)
			default:
				_, errs[i] = mux.Send(context.Background(), "Browser.close", nil)
			}
		}(i)
	}

	waitSent(t, conn, 3)

	dropErr := errors.New("tcp: broken pipe")
	conn.fail(dropErr)

	waitFor(t, func() bool {
		select {
		case <-mux.Done():
			return true
		default:
			return false
		}
	}, "mux shutdown")

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pending calls did not return after disconnect")
	}

	for i, err := range errs {
		if !errors.Is(err, ErrConnClosed) {
			t.Fatalf("call %d: err = %v, want ErrConnClosed", i, err)
		}
	}
	if errs[0] != errs[1] || errs[1] != errs[2] {
		t.Fatalf("waiters received different error instances: %v %v %v",
			errs[0], errs[1], errs[2])
	}
	if !errors.Is(mux.CloseErr(), dropErr) {
		t.Fatalf("CloseErr = %v, want it to wrap %v", mux.CloseErr(), dropErr)
	}

	select {
	case <-sess.Done():
	default:
		t.Fatal("session not shut down on disconnect")
	}

	_, err := mux.Send(context.Background(), "Browser.version", nil)
	if !errors.Is(err, ErrConnClosed) {
		t.Fatalf("send after close: %v", err)
	}

	// Closing again must be a no-op.
	if err := mux.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestAttachDetachFlow drives explicit attach/detach commands including the
// browser's detachedFromTarget notification.
func TestAttachDetachFlow(t *testing.T) {
	conn := newMockConn()
	mux := New(conn)
	defer mux.Close()

	type attachOut struct {
		sess *Session
		err  error
	}
	attached := make(chan attachOut, 1)
	go func() {
		s, err := mux.Attach(context.Background(), "page-1", true)
		attached <- attachOut{s, err}
	}()

	cmd := waitSent(t, conn, 1)[0]
	if cmd.Method != "Target.attachToTarget" {
		t.Fatalf("unexpected method %q", cmd.Method)
	}
	conn.push(Message{ID: cmd.ID, Result: mustMarshal(t, map[string]string{
		"sessionId": "sess-1",
	})})

	var res attachOut
	select {
	case res = <-attached:
	case <-time.After(2 * time.Second):
		t.Fatal("Attach did not return")
	}
	if res.err != nil {
		t.Fatalf("Attach: %v", res.err)
	}
	sess := res.sess
	if sess.ID() != "sess-1" {
		t.Fatalf("session id = %q", sess.ID())
	}

	// Session-scoped command carries the sessionId.
	go func() {
		_, _ = sess.Send(context.Background(), "Page.navigate",
			map[string]string{"url": "https://example.com"})
	}()
	nav := waitSent(t, conn, 1)[0]
	if nav.SessionID != "sess-1" || nav.Method != "Page.navigate" {
		t.Fatalf("session command frame wrong: %+v", nav)
	}
	conn.push(Message{ID: nav.ID, Result: mustMarshal(t, map[string]any{})})

	// Explicit detach sends the command; the browser confirmation ends the
	// session, and a late event for the dead session is dropped.
	go func() { _ = mux.Detach(context.Background(), "sess-1") }()
	det := waitSent(t, conn, 1)[0]
	if det.Method != "Target.detachFromTarget" {
		t.Fatalf("detach frame wrong: %+v", det)
	}
	var detParams map[string]string
	if err := json.Unmarshal(det.Params, &detParams); err != nil || detParams["sessionId"] != "sess-1" {
		t.Fatalf("detach params wrong: %s err=%v", det.Params, err)
	}
	conn.push(Message{ID: det.ID, Result: mustMarshal(t, map[string]any{})})
	conn.push(Message{Method: "Target.detachedFromTarget",
		Params: mustMarshal(t, map[string]string{"sessionId": "sess-1"})})

	waitFor(t, func() bool {
		_, ok := mux.Session("sess-1")
		return !ok
	}, "sess-1 removal")

	select {
	case <-sess.Done():
	case <-time.After(time.Second):
		t.Fatal("sess Done not closed after detach event")
	}

	conn.push(Message{Method: "Page.ghost", SessionID: "sess-1",
		Params: mustMarshal(t, map[string]string{"late": "true"})})

	// The lifecycle event and session-less events still reach browser
	// subscribers without blocking the read loop.
	select {
	case ev := <-mux.BrowserEvents():
		if ev.Method != "Target.detachedFromTarget" {
			t.Fatalf("browser event = %q", ev.Method)
		}
	case <-time.After(time.Second):
		t.Fatal("browser subscriber did not get detach event")
	}
}

// TestResponseError verifies CDP error objects surface as *ResponseError.
func TestResponseError(t *testing.T) {
	conn := newMockConn()
	mux := New(conn)
	defer mux.Close()

	errCh := make(chan error, 1)
	go func() {
		_, err := mux.Send(context.Background(), "No.suchMethod", nil)
		errCh <- err
	}()
	cmd := waitSent(t, conn, 1)[0]
	conn.push(Message{ID: cmd.ID, Error: &ResponseError{
		Code:    -32601,
		Message: "'No.suchMethod' wasn't found",
	}})

	select {
	case err := <-errCh:
		var re *ResponseError
		if !errors.As(err, &re) {
			t.Fatalf("err = %v, want *ResponseError", err)
		}
		if re.Code != -32601 {
			t.Fatalf("error code = %d", re.Code)
		}
	case <-time.After(time.Second):
		t.Fatal("Send did not return")
	}
}

// TestCanceledCall verifies ctx cancellation unblocks a caller and that a
// response arriving afterwards does not corrupt later commands.
func TestCanceledCall(t *testing.T) {
	conn := newMockConn()
	mux := New(conn)
	defer mux.Close()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := mux.Send(ctx, "Slow.method", nil)
		errCh <- err
	}()
	cmd := waitSent(t, conn, 1)[0]

	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled call did not return")
	}

	// Late response for the dead command is ignored.
	conn.push(Message{ID: cmd.ID, Result: mustMarshal(t, map[string]string{"late": "yes"})})

	// The next command still gets id+1 and a matching response.
	go func() {
		_, _ = mux.Send(context.Background(), "Next.method", nil)
	}()
	next := waitSent(t, conn, 1)[0]
	if next.ID != cmd.ID+1 {
		t.Fatalf("next id = %d, want %d", next.ID, cmd.ID+1)
	}
	conn.push(Message{ID: next.ID, Result: mustMarshal(t, map[string]string{"ok": "yes"})})
}

// TestAutoAttachEvent verifies an attachedToTarget event pre-registers a
// session before any command response, and its events route correctly.
func TestAutoAttachEvent(t *testing.T) {
	conn := newMockConn()
	mux := New(conn)
	defer mux.Close()

	conn.push(Message{Method: "Target.attachedToTarget",
		Params: mustMarshal(t, map[string]string{"sessionId": "auto-sess"})})

	waitFor(t, func() bool {
		_, ok := mux.Session("auto-sess")
		return ok
	}, "auto-attach registration")

	sess, _ := mux.Session("auto-sess")
	conn.push(Message{Method: "Runtime.consoleAPICalled", SessionID: "auto-sess",
		Params: mustMarshal(t, map[string]string{"type": "log"})})

	select {
	case ev := <-sess.Events():
		if ev.Method != "Runtime.consoleAPICalled" {
			t.Fatalf("event = %q", ev.Method)
		}
	case <-time.After(time.Second):
		t.Fatal("auto-attached session did not receive event")
	}
}
