package netsec

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// pausableSender is a Sender test double that can block inside SendTo until
// the test releases it, letting tests interleave receive events, concurrent
// reservations and validation with in-flight sends. It can also be told to
// write only part of each datagram.
type pausableSender struct {
	paused   atomic.Bool
	entered  chan struct{} // receives a signal when a paused send starts
	resume   chan struct{} // closed to release all paused sends
	writeMax int           // if > 0, each call writes at most this many bytes
	calls    atomic.Int64
	written  atomic.Int64
}

func newPausableSender() *pausableSender {
	return &pausableSender{
		entered: make(chan struct{}, 64),
		resume:  make(chan struct{}),
	}
}

func (s *pausableSender) SendTo(b []byte, addr string) (int, error) {
	s.calls.Add(1)
	if s.paused.Load() {
		s.entered <- struct{}{}
		<-s.resume
	}
	n := len(b)
	if s.writeMax > 0 && n > s.writeMax {
		n = s.writeMax
	}
	s.written.Add(int64(n))
	return n, nil
}

func (s *pausableSender) release() {
	s.paused.Store(false)
	close(s.resume)
}

func TestReserveCapsAtTripleReceived(t *testing.T) {
	l := NewLimiter()
	p := l.Path("client")
	if g := p.Reserve(1); g != 0 {
		t.Fatalf("no receives yet, grant = %d, want 0", g)
	}
	p.AddReceived(100)
	if g := p.Reserve(400); g != 300 {
		t.Fatalf("grant = %d, want 300 (3x cap)", g)
	}
	if g := p.Reserve(1); g != 0 {
		t.Fatalf("budget exhausted, grant = %d, want 0", g)
	}
	p.Release(300)
	if st := p.Stats(); st.Reserved != 0 || st.Sent != 0 {
		t.Fatalf("after release: %+v", st)
	}
}

func TestSendExhaustedReturnsError(t *testing.T) {
	l := NewLimiter()
	p := l.Path("client")
	s := newPausableSender()
	n, err := p.Send(s, make([]byte, 10))
	if !errors.Is(err, ErrBudgetExhausted) || n != 0 {
		t.Fatalf("n=%d err=%v, want 0, ErrBudgetExhausted", n, err)
	}
	if s.calls.Load() != 0 {
		t.Fatal("sender must not run when no budget is granted")
	}
}

func TestPartialSendReturnsUnsentOnly(t *testing.T) {
	l := NewLimiter()
	p := l.Path("client")
	p.AddReceived(100) // budget 300
	p.AddReceived(100) // duplicate receive event: budget grows to 600

	s := newPausableSender()
	s.paused.Store(true)
	s.writeMax = 250 // partial write

	done := make(chan int, 1)
	go func() {
		n, err := p.Send(s, make([]byte, 1000))
		if err != nil {
			t.Errorf("Send: %v", err)
		}
		done <- n
	}()
	<-s.entered // send in flight holding a reservation of 600

	if g := p.Reserve(1); g != 0 {
		t.Fatalf("in-flight reservation must hold the budget, grant = %d", g)
	}

	s.release()
	if n := <-done; n != 250 {
		t.Fatalf("sent = %d, want 250", n)
	}
	st := p.Stats()
	if st.Sent != 250 || st.Reserved != 0 {
		t.Fatalf("stats = %+v, want Sent=250 Reserved=0", st)
	}
	// Only the 250 written bytes are charged; the 350 unsent bytes of the
	// reservation were returned.
	if g := p.Reserve(1000); g != 350 {
		t.Fatalf("remaining budget = %d, want 350", g)
	}
}

func TestFailedSendReturnsWholeReservation(t *testing.T) {
	l := NewLimiter()
	p := l.Path("client")
	p.AddReceived(100)

	// A failed send writes nothing at all.
	s := SenderFunc(func(b []byte, addr string) (int, error) {
		return 0, errors.New("write: connection refused")
	})
	n, err := p.Send(s, make([]byte, 300))
	if err == nil || n != 0 {
		t.Fatalf("n=%d err=%v, want 0 and error", n, err)
	}
	st := p.Stats()
	if st.Sent != 0 || st.Reserved != 0 {
		t.Fatalf("failed send must not consume budget: %+v", st)
	}
	if g := p.Reserve(1000); g != 300 {
		t.Fatalf("budget after failed send = %d, want 300", g)
	}
}

func TestConcurrentWorkersNeverOverdraw(t *testing.T) {
	l := NewLimiter()
	p := l.Path("client")
	p.AddReceived(100) // budget 300

	s := newPausableSender()
	stop := make(chan struct{})
	var monitor sync.WaitGroup
	monitor.Add(1)
	go func() {
		defer monitor.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			st := p.Stats()
			if st.Sent+st.Reserved > AmplificationFactor*st.Received {
				t.Errorf("overdraw: %+v", st)
			}
		}
	}()

	const workers = 8
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if _, err := p.Send(s, make([]byte, 40)); errors.Is(err, ErrBudgetExhausted) {
					return
				}
			}
		}()
	}
	wg.Wait()
	close(stop)
	monitor.Wait()

	st := p.Stats()
	if st.Sent != 300 || st.Reserved != 0 {
		t.Fatalf("stats = %+v, want Sent=300 Reserved=0", st)
	}
	if w := s.written.Load(); w != 300 {
		t.Fatalf("wire bytes = %d, want 300", w)
	}
}

func TestConcurrentReserveExactTotal(t *testing.T) {
	l := NewLimiter()
	p := l.Path("client")
	p.AddReceived(100) // budget 300

	var granted atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				granted.Add(int64(p.Reserve(7)))
			}
		}()
	}
	wg.Wait()
	if g := granted.Load(); g != 300 {
		t.Fatalf("total granted = %d, want exactly 300", g)
	}
}

func TestAddressChangeOpensIndependentPath(t *testing.T) {
	l := NewLimiter()
	a := l.Path("10.0.0.1:1111")
	a.AddReceived(10)
	if g := a.Reserve(100); g != 30 {
		t.Fatalf("grant = %d, want 30", g)
	}
	if g := a.Reserve(1); g != 0 {
		t.Fatalf("path a exhausted, grant = %d", g)
	}

	// Same client from a new address: independent budget, unaffected by a.
	b := l.Path("10.0.0.1:2222")
	if g := b.Reserve(1); g != 0 {
		t.Fatalf("new path has no receives, grant = %d", g)
	}
	b.AddReceived(10)
	if g := b.Reserve(100); g != 30 {
		t.Fatalf("grant = %d, want 30", g)
	}

	if l.Path("10.0.0.1:1111") != a {
		t.Fatal("same address must resolve to the same shared path")
	}
}

func TestValidationLiftsLimitMidFlight(t *testing.T) {
	l := NewLimiter()
	p := l.Path("client")
	p.AddReceived(10) // budget 30

	s := newPausableSender()
	s.paused.Store(true)

	done := make(chan error, 1)
	go func() {
		_, err := p.Send(s, make([]byte, 30))
		done <- err
	}()
	<-s.entered // in-flight send holds the full pre-validation budget
	if g := p.Reserve(1); g != 0 {
		t.Fatalf("budget exhausted, grant = %d", g)
	}

	// Address validation completes while the send is still in flight.
	p.Validate()
	if !p.Validated() {
		t.Fatal("path should report validated")
	}
	if g := p.Reserve(1000); g != 1000 {
		t.Fatalf("validated path must admit without limit, grant = %d", g)
	}
	p.Release(1000)

	s.release()
	if err := <-done; err != nil {
		t.Fatalf("Send: %v", err)
	}
	st := p.Stats()
	if st.Sent != 30 || st.Reserved != 0 || !st.Validated {
		t.Fatalf("stats = %+v, want Sent=30 Reserved=0 Validated=true", st)
	}
}

func TestReceiveDuringFlightExtendsBudget(t *testing.T) {
	l := NewLimiter()
	p := l.Path("client")
	p.AddReceived(100) // budget 300

	s := newPausableSender()
	s.paused.Store(true)

	done := make(chan struct{})
	go func() {
		_, _ = p.Send(s, make([]byte, 300))
		close(done)
	}()
	<-s.entered
	if g := p.Reserve(1); g != 0 {
		t.Fatalf("budget exhausted, grant = %d", g)
	}

	// A new datagram arrives while the response is still being sent.
	p.AddReceived(50)
	if g := p.Reserve(1000); g != 150 {
		t.Fatalf("budget after receive = %d, want 150", g)
	}
	p.Release(150)

	s.release()
	<-done
	if st := p.Stats(); st.Sent != 300 || st.Reserved != 0 {
		t.Fatalf("stats = %+v", st)
	}
}
