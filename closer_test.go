package netsec

import (
	"errors"
	"sync"
	"testing"
)

type eventCollector struct {
	mu     sync.Mutex
	events []Event
}

func (c *eventCollector) handler() func(Event) {
	return func(e Event) {
		c.mu.Lock()
		c.events = append(c.events, e)
		c.mu.Unlock()
	}
}

func (c *eventCollector) snapshot() []Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Event(nil), c.events...)
}

func (c *eventCollector) closeCount() int {
	n := 0
	for _, e := range c.snapshot() {
		if e.Kind == EventClose {
			n++
		}
	}
	return n
}

func (c *eventCollector) messages() [][]byte {
	var msgs [][]byte
	for _, e := range c.snapshot() {
		if e.Kind == EventMessage {
			msgs = append(msgs, e.Message)
		}
	}
	return msgs
}

func TestParseClosePayloadValidation(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
		code    uint16
		reason  string
		wantErr error
	}{
		{name: "empty means no status", payload: nil, code: StatusNoStatusRcvd},
		{name: "normal closure", payload: closePayload(1000, "bye"), code: 1000, reason: "bye"},
		{name: "going away no reason", payload: closePayload(1001, ""), code: 1001},
		{name: "application code", payload: closePayload(4000, "app"), code: 4000, reason: "app"},
		{name: "utf8 reason", payload: closePayload(1000, "再见"), code: 1000, reason: "再见"},
		{name: "truncated code", payload: []byte{0x03}, wantErr: errCloseStatusCode},
		{name: "code too small", payload: closePayload(999, ""), wantErr: errCloseStatusCode},
		{name: "code too large", payload: closePayload(5000, ""), wantErr: errCloseStatusCode},
		{name: "reserved 1004", payload: closePayload(1004, ""), wantErr: errCloseStatusCode},
		{name: "reserved 1005", payload: closePayload(1005, ""), wantErr: errCloseStatusCode},
		{name: "reserved 1006", payload: closePayload(1006, ""), wantErr: errCloseStatusCode},
		{name: "reserved 1015", payload: closePayload(1015, ""), wantErr: errCloseStatusCode},
		{name: "undefined 2000", payload: closePayload(2000, ""), wantErr: errCloseStatusCode},
		{name: "bad utf8 reason", payload: []byte{0x03, 0xe8, 0xff, 0xfe}, wantErr: errCloseReasonUTF8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, reason, err := parseClosePayload(tt.payload)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if code != tt.code || reason != tt.reason {
				t.Fatalf("got (%d, %q), want (%d, %q)", code, reason, tt.code, tt.reason)
			}
		})
	}
}

// TestHalfWrittenCloseFrame feeds a close frame split across two writes and
// verifies the handshake completes with exactly one close event.
func TestHalfWrittenCloseFrame(t *testing.T) {
	col := &eventCollector{}
	c := NewCloser(col.handler())

	wire := marshalFrame(opcodeClose, closePayload(StatusNormalClosure, "bye"))
	half := len(wire) / 2

	out, err := c.Feed(wire[:half])
	if err != nil {
		t.Fatalf("first half: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("first half produced %d outbound bytes", len(out))
	}
	if got := c.State(); got != StateOpen {
		t.Fatalf("state after first half = %v, want %v", got, StateOpen)
	}
	if n := len(col.snapshot()); n != 0 {
		t.Fatalf("first half emitted %d events", n)
	}

	out, err = c.Feed(wire[half:])
	if err != nil {
		t.Fatalf("second half: %v", err)
	}
	if got := c.State(); got != StateCloseReceived {
		t.Fatalf("state after close = %v, want %v", got, StateCloseReceived)
	}
	// The only permitted output is the echoed close response.
	resp, n, err := parseFrame(out)
	if err != nil || n != len(out) || resp.opcode != opcodeClose {
		t.Fatalf("outbound is not a single close frame: op=%x n=%d err=%v", resp.opcode, n, err)
	}
	if code, reason, _ := parseClosePayload(resp.payload); code != 1000 || reason != "bye" {
		t.Fatalf("close response = (%d, %q)", code, reason)
	}

	// Cached data delivered after the close frame must be dropped.
	out, err = c.Feed(marshalFrame(opcodeText, []byte("stale")))
	if err != nil {
		t.Fatalf("stale data: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("data after close produced %d outbound bytes", len(out))
	}
	if msgs := col.messages(); len(msgs) != 0 {
		t.Fatalf("stale data published as %d messages", len(msgs))
	}
	if col.closeCount() != 0 {
		t.Fatal("close event fired before write completion")
	}

	c.WriteDone()
	if got := c.State(); got != StateWriteDone {
		t.Fatalf("state after write done = %v, want %v", got, StateWriteDone)
	}
	if n := col.closeCount(); n != 1 {
		t.Fatalf("close events = %d, want 1", n)
	}
	// Termination after a completed handshake must not fire again.
	c.Terminate(nil)
	if n := col.closeCount(); n != 1 {
		t.Fatalf("close events after terminate = %d, want 1", n)
	}
}

// TestConcurrentCloseFiresOnce hammers the Closer with concurrent feeds,
// write completions and cancellations, expecting a single close event.
func TestConcurrentCloseFiresOnce(t *testing.T) {
	col := &eventCollector{}
	c := NewCloser(col.handler())

	wire := marshalFrame(opcodeClose, closePayload(StatusGoingAway, ""))
	boom := errors.New("boom")

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			switch i % 4 {
			case 0:
				c.Terminate(boom)
			case 1:
				c.Feed(wire) //nolint:errcheck
			case 2:
				c.WriteDone()
			case 3:
				c.Terminate(nil)
			}
		}(i)
	}
	wg.Wait()
	c.Terminate(nil) // ensure the machine reaches termination

	if n := col.closeCount(); n != 1 {
		t.Fatalf("close events = %d, want exactly 1", n)
	}
	if got := c.State(); got != StateTerminated {
		t.Fatalf("state = %v, want %v", got, StateTerminated)
	}
}

// TestErrorReasonNotPublishedAsMessage verifies transport errors ride on the
// close event only, and never appear as ordinary business messages.
func TestErrorReasonNotPublishedAsMessage(t *testing.T) {
	col := &eventCollector{}
	c := NewCloser(col.handler())

	if _, err := c.Feed(marshalFrame(opcodeText, []byte("hello"))); err != nil {
		t.Fatalf("data frame: %v", err)
	}
	if _, err := c.Feed(marshalFrame(opcodeClose, closePayload(1000, ""))); err != nil {
		t.Fatalf("close frame: %v", err)
	}
	// Cached data flushed by the peer after its close frame.
	if _, err := c.Feed(marshalFrame(opcodeText, []byte("boom"))); err != nil {
		t.Fatalf("stale frame: %v", err)
	}

	boom := errors.New("boom")
	c.Terminate(boom)

	events := col.snapshot()
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2 (message + close)", len(events))
	}
	if events[0].Kind != EventMessage || string(events[0].Message) != "hello" {
		t.Fatalf("first event = %+v, want message %q", events[0], "hello")
	}
	if events[1].Kind != EventClose {
		t.Fatalf("second event = %+v, want close", events[1])
	}
	if !errors.Is(events[1].Err, boom) {
		t.Fatalf("close err = %v, want %v", events[1].Err, boom)
	}
	for _, e := range events {
		if e.Kind == EventMessage && string(e.Message) == "boom" {
			t.Fatal("error reason leaked into a business message")
		}
	}
}

// TestHostInitiatedHandshake walks the full host-initiated close sequence.
func TestHostInitiatedHandshake(t *testing.T) {
	col := &eventCollector{}
	c := NewCloser(col.handler())

	out, err := c.WriteClose(StatusNormalClosure, "done")
	if err != nil {
		t.Fatalf("WriteClose: %v", err)
	}
	if got := c.State(); got != StateCloseSent {
		t.Fatalf("state = %v, want %v", got, StateCloseSent)
	}
	f, n, err := parseFrame(out)
	if err != nil || n != len(out) || f.opcode != opcodeClose {
		t.Fatalf("WriteClose output is not a close frame")
	}
	if code, reason, _ := parseClosePayload(f.payload); code != 1000 || reason != "done" {
		t.Fatalf("close frame = (%d, %q)", code, reason)
	}
	if _, err := c.WriteClose(1000, ""); err == nil {
		t.Fatal("second WriteClose succeeded")
	}

	c.WriteDone()
	if got := c.State(); got != StateWriteDone {
		t.Fatalf("state = %v, want %v", got, StateWriteDone)
	}
	if col.closeCount() != 0 {
		t.Fatal("close event fired before peer close")
	}

	// In-flight data sent by the peer before its close frame is delivered.
	peer := append(marshalFrame(opcodeText, []byte("last")),
		marshalFrame(opcodeClose, closePayload(StatusGoingAway, ""))...)
	if _, err := c.Feed(peer); err != nil {
		t.Fatalf("peer frames: %v", err)
	}
	if msgs := col.messages(); len(msgs) != 1 || string(msgs[0]) != "last" {
		t.Fatalf("messages = %q, want [last]", msgs)
	}
	if n := col.closeCount(); n != 1 {
		t.Fatalf("close events = %d, want 1", n)
	}
	ev := col.snapshot()[len(col.snapshot())-1]
	if ev.Code != StatusGoingAway || ev.Err != nil {
		t.Fatalf("close event = %+v", ev)
	}

	c.Terminate(nil)
	if got := c.State(); got != StateTerminated {
		t.Fatalf("state = %v, want %v", got, StateTerminated)
	}
	if n := col.closeCount(); n != 1 {
		t.Fatalf("close events after terminate = %d, want 1", n)
	}
	if _, err := c.Feed(marshalFrame(opcodeText, []byte("x"))); !errors.Is(err, ErrClosed) {
		t.Fatalf("Feed after terminate = %v, want ErrClosed", err)
	}
}

// TestInvalidCloseCodeFailsConnection expects a 1002 close and a single
// close event carrying the protocol error.
func TestInvalidCloseCodeFailsConnection(t *testing.T) {
	col := &eventCollector{}
	c := NewCloser(col.handler())

	out, err := c.Feed(marshalFrame(opcodeClose, closePayload(1006, "")))
	if !errors.Is(err, errCloseStatusCode) {
		t.Fatalf("err = %v, want %v", err, errCloseStatusCode)
	}
	f, n, perr := parseFrame(out)
	if perr != nil || n != len(out) || f.opcode != opcodeClose {
		t.Fatalf("failure output is not a close frame")
	}
	if code, _, _ := parseClosePayload(f.payload); code != StatusProtocolError {
		t.Fatalf("failure close code = %d, want %d", code, StatusProtocolError)
	}
	if n := col.closeCount(); n != 1 {
		t.Fatalf("close events = %d, want 1", n)
	}
	ev := col.snapshot()[0]
	if !errors.Is(ev.Err, errCloseStatusCode) {
		t.Fatalf("close event err = %v", ev.Err)
	}
	if got := c.State(); got != StateTerminated {
		t.Fatalf("state = %v, want %v", got, StateTerminated)
	}
}

// TestInvalidUTF8ReasonFailsConnection expects a 1007 close response.
func TestInvalidUTF8ReasonFailsConnection(t *testing.T) {
	col := &eventCollector{}
	c := NewCloser(col.handler())

	out, err := c.Feed(marshalFrame(opcodeClose, []byte{0x03, 0xe8, 0xff}))
	if !errors.Is(err, errCloseReasonUTF8) {
		t.Fatalf("err = %v, want %v", err, errCloseReasonUTF8)
	}
	f, n, perr := parseFrame(out)
	if perr != nil || n != len(out) || f.opcode != opcodeClose {
		t.Fatalf("failure output is not a close frame")
	}
	if code, _, _ := parseClosePayload(f.payload); code != StatusInvalidFramePayloadData {
		t.Fatalf("failure close code = %d, want %d", code, StatusInvalidFramePayloadData)
	}
	if n := col.closeCount(); n != 1 {
		t.Fatalf("close events = %d, want 1", n)
	}
}

// TestMaskedAndFragmentedDelivery exercises masked frames and byte-at-a-time
// delivery through the incremental parser.
func TestMaskedAndFragmentedDelivery(t *testing.T) {
	col := &eventCollector{}
	c := NewCloser(col.handler())

	// Masked text frame "hi" with key 0x01020304.
	masked := []byte{0x81, 0x82, 0x01, 0x02, 0x03, 0x04, 'h' ^ 0x01, 'i' ^ 0x02}
	for _, b := range masked {
		if _, err := c.Feed([]byte{b}); err != nil {
			t.Fatalf("byte feed: %v", err)
		}
	}
	if msgs := col.messages(); len(msgs) != 1 || string(msgs[0]) != "hi" {
		t.Fatalf("messages = %q, want [hi]", msgs)
	}
}
