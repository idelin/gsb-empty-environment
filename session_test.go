package fsm

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	stInit   State = "INIT"
	stEst    State = "ESTABLISHED"
	stClosed State = "CLOSED"
)

// newTestMachine compiles the table used across session tests.
// dataRuns counts DATA action executions (for dedup assertions).
func newTestMachine(t *testing.T, dataRuns *int) *Machine {
	t.Helper()
	m, err := Compile(stInit,
		[]State{stInit, stEst, stClosed},
		[]Row{
			{stInit, EventHello, stEst, func(c *Context) (interface{}, error) {
				return "WELCOME", nil
			}},
			{stInit, EventTimeout, stClosed, nil},
			{stEst, EventPing, stEst, func(c *Context) (interface{}, error) {
				return "PONG", nil
			}},
			{stEst, EventData, stEst, func(c *Context) (interface{}, error) {
				*dataRuns++
				p := string(c.Frame.Payload)
				if p == "fail" {
					return nil, errors.New("simulated action failure")
				}
				return "ECHO:" + strings.ToUpper(p), nil
			}},
			{stEst, EventBye, stClosed, nil},
			{stEst, EventReenter, stEst, func(c *Context) (interface{}, error) {
				return "ERR: already handshaked", nil
			}},
			{stEst, EventUnknown, stEst, func(c *Context) (interface{}, error) {
				return "ERR: unknown", nil
			}},
			{stEst, EventTimeout, stClosed, nil},
		})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return m
}

func handshake(t *testing.T, s *Session) {
	t.Helper()
	r := s.Handle(&Frame{Type: TypeHello})
	if r.Err != nil || s.State() != stEst {
		t.Fatalf("handshake: %+v state=%s", r, s.State())
	}
}

func TestHandshakeGatesBusinessFrames(t *testing.T) {
	var runs int
	s := NewSession(newTestMachine(t, &runs), 0, 0)
	for _, f := range []*Frame{
		{Type: TypePing},
		{Type: TypeData, Seq: 1, Payload: []byte("x")},
		{Type: TypeBye},
	} {
		r := s.Handle(f)
		if !IsNoTransition(r.Err) {
			t.Fatalf("type 0x%02x pre-handshake: err=%v", f.Type, r.Err)
		}
		if s.State() != stInit {
			t.Fatalf("state moved to %s", s.State())
		}
	}
	if runs != 0 {
		t.Fatalf("action ran %d times pre-handshake", runs)
	}
	handshake(t, s)
	r := s.Handle(&Frame{Type: TypePing})
	if r.Output != "PONG" {
		t.Fatalf("post-handshake PING: %+v", r)
	}
}

func TestAtomicCommitAndFailureKeepsState(t *testing.T) {
	var runs int
	s := NewSession(newTestMachine(t, &runs), 0, 0)
	handshake(t, s)

	r := s.Handle(&Frame{Type: TypeData, Seq: 1, Payload: []byte("ok")})
	if r.Err != nil || !r.Applied || r.Output != "ECHO:OK" {
		t.Fatalf("good DATA: %+v", r)
	}

	r = s.Handle(&Frame{Type: TypeData, Seq: 2, Payload: []byte("fail")})
	if r.Err == nil || r.Applied {
		t.Fatalf("failing DATA: %+v", r)
	}
	if s.State() != stEst {
		t.Fatalf("state changed on action failure: %s", s.State())
	}
	// The failed seq was never applied, so the dedup rule must not block
	// its retry.
	r = s.Handle(&Frame{Type: TypeData, Seq: 2, Payload: []byte("retry")})
	if r.Err != nil || r.Output != "ECHO:RETRY" {
		t.Fatalf("retry after failure: %+v", r)
	}
}

func TestReplayFollowsDedupRule(t *testing.T) {
	var runs int
	s := NewSession(newTestMachine(t, &runs), 0, 0)
	handshake(t, s)

	r := s.Handle(&Frame{Type: TypeData, Seq: 5, Payload: []byte("a")})
	if !r.Applied || r.Output != "ECHO:A" || r.Deduplicated {
		t.Fatalf("first: %+v", r)
	}
	// Replay of the same seq: cached output, no re-execution, no transition.
	r = s.Handle(&Frame{Type: TypeData, Seq: 5, Payload: []byte("a")})
	if !r.Deduplicated || r.Applied || r.Output != "ECHO:A" {
		t.Fatalf("replay same seq: %+v", r)
	}
	// Replay of an older seq is also a duplicate.
	r = s.Handle(&Frame{Type: TypeData, Seq: 3, Payload: []byte("old")})
	if !r.Deduplicated {
		t.Fatalf("replay older seq: %+v", r)
	}
	if runs != 1 {
		t.Fatalf("action ran %d times, want 1", runs)
	}
	if s.State() != stEst {
		t.Fatalf("state moved on replay: %s", s.State())
	}
	// A newer seq applies normally.
	r = s.Handle(&Frame{Type: TypeData, Seq: 6, Payload: []byte("b")})
	if r.Deduplicated || !r.Applied || runs != 2 {
		t.Fatalf("new seq: %+v runs=%d", r, runs)
	}
}

func TestUnknownTypeIsIndependentEvent(t *testing.T) {
	var runs int
	s := NewSession(newTestMachine(t, &runs), 0, 0)
	handshake(t, s)
	r := s.Handle(&Frame{Type: 0x7f, Payload: []byte("?")})
	if r.Err != nil || r.Output != "ERR: unknown" || s.State() != stEst {
		t.Fatalf("unknown type: %+v state=%s", r, s.State())
	}
}

func TestHandshakeReentryIsIndependentEvent(t *testing.T) {
	var runs int
	s := NewSession(newTestMachine(t, &runs), 0, 0)
	handshake(t, s)
	r := s.Handle(&Frame{Type: TypeHello})
	if r.Err != nil || r.Output != "ERR: already handshaked" || s.State() != stEst {
		t.Fatalf("re-entry: %+v state=%s", r, s.State())
	}
}

func TestTimeoutIsIndependentEvent(t *testing.T) {
	var runs int
	s := NewSession(newTestMachine(t, &runs), 0, time.Minute)
	now := time.Now()
	s.SetClock(func() time.Time { return now })

	if r := s.Tick(); r.Applied || r.Err != nil {
		t.Fatalf("premature timeout: %+v", r)
	}
	now = now.Add(2 * time.Minute)
	r := s.Tick()
	if r.Err != nil || !r.Applied || s.State() != stClosed {
		t.Fatalf("timeout in INIT: %+v state=%s", r, s.State())
	}
}

func TestReentrantHandleRejected(t *testing.T) {
	var s *Session
	m, err := Compile(stInit, []State{stInit, stEst}, []Row{
		{stInit, EventHello, stEst, func(c *Context) (interface{}, error) {
			inner := s.Handle(&Frame{Type: TypePing})
			if inner.Err != ErrReentrantCall {
				t.Errorf("re-entrant Handle: err=%v", inner.Err)
			}
			return "WELCOME", nil
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	s = NewSession(m, 0, 0)
	r := s.Handle(&Frame{Type: TypeHello})
	if r.Err != nil || s.State() != stEst {
		t.Fatalf("outer Handle: %+v state=%s", r, s.State())
	}
}

func TestSessionNextOverChunkedBytes(t *testing.T) {
	var runs int
	s := NewSession(newTestMachine(t, &runs), 0, 0)
	stream := append(Encode(Frame{Type: TypeHello}),
		Encode(Frame{Type: TypeData, Seq: 1, Payload: []byte("hi")})...)
	var outs []interface{}
	for i := 0; i < len(stream); i++ {
		s.Feed(stream[i : i+1])
		r, err := s.Next()
		if err == ErrNeedMoreData {
			continue
		}
		if err != nil {
			t.Fatalf("byte %d: %v", i, err)
		}
		if r.Err != nil {
			t.Fatalf("byte %d: %v", i, r.Err)
		}
		outs = append(outs, r.Output)
	}
	if len(outs) != 2 || outs[0] != "WELCOME" || outs[1] != "ECHO:HI" {
		t.Fatalf("outputs: %v", outs)
	}
	if s.State() != stEst {
		t.Fatalf("state=%s", s.State())
	}
}
