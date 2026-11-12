package capnet_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"example.org/netsec/q0492/capnet"
)

type spyDialer struct {
	mu    sync.Mutex
	calls int
}

func (s *spyDialer) Dial(network, address string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return nil
}

func (s *spyDialer) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func newWorld(t *testing.T) (*capnet.Host, *capnet.Trial, capnet.Handle, *capnet.Router, *spyDialer) {
	t.Helper()
	spy := &spyDialer{}
	host := capnet.NewHost(spy.Dial)
	trial := host.NewTrial()
	h, err := trial.Mint("198.51.100.7:8443", capnet.Script{
		Inbox:        [][]byte{[]byte("banner\n")},
		ExpectWrites: [][]byte{[]byte("GET / HTTP/1.0\r\n\r\n")},
		AutoReply:    map[string][]byte{"GET / HTTP/1.0\r\n\r\n": []byte("200 ok\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	return host, trial, h, capnet.NewRouter(host), spy
}

// A malicious plug-in only knows the target address string. It must have no
// API to turn that string into a handle.
func TestMaliciousCannotRebuildHandleFromAddress(t *testing.T) {
	_, _, _, router, spy := newWorld(t)

	ht := reflect.TypeOf(capnet.Handle{})
	for i := 0; i < ht.NumField(); i++ {
		if f := ht.Field(i); f.IsExported() {
			t.Fatalf("Handle exposes exported field %s", f.Name)
		}
	}

	pkg := reflect.ValueOf(router).Elem().Type().PkgPath()
	if pkg != "example.org/netsec/q0492/capnet" {
		t.Fatalf("unexpected package path %q", pkg)
	}

	// Every zero-valued handle a plug-in can fabricate without the host must be
	// rejected; there is no ParseHandle/NewHandle constructor to call.
	var fabricated capnet.Handle
	if _, err := router.Read(fabricated, make([]byte, 4)); !errors.Is(err, capnet.ErrBadHandle) {
		t.Fatalf("zero handle read: want ErrBadHandle, got %v", err)
	}
	if _, err := router.Write(fabricated, []byte("x")); !errors.Is(err, capnet.ErrBadHandle) {
		t.Fatalf("zero handle write: want ErrBadHandle, got %v", err)
	}
	if _, err := router.RemoteAddr(fabricated); !errors.Is(err, capnet.ErrBadHandle) {
		t.Fatalf("zero handle addr: want ErrBadHandle, got %v", err)
	}
	if _, err := router.Now(fabricated); !errors.Is(err, capnet.ErrBadHandle) {
		t.Fatalf("zero handle now: want ErrBadHandle, got %v", err)
	}

	// Reflection cannot write the unexported fields; the zero handle stays
	// unusable.
	v := reflect.ValueOf(&fabricated).Elem()
	for i := 0; i < v.NumField(); i++ {
		if v.Field(i).CanSet() {
			t.Fatal("handle field is settable from outside the package")
		}
	}

	// The redacted string cannot be parsed back into a capability.
	if s := fabricated.String(); s != "capnet:handle[redacted]" {
		t.Fatalf("unexpected handle string %q", s)
	}
	if spy.Count() != 0 {
		t.Fatalf("real dial callback fired %d times", spy.Count())
	}
}

// A handle handed off between two plug-in routers remains a pure capability
// reference: sharing it never reveals an address or triggers a dial, and
// handles stolen from a previous generation are dead on arrival.
func TestMaliciousHandleHandoff(t *testing.T) {
	host, trial, h, routerA, spy := newWorld(t)
	routerB := capnet.NewRouter(host)

	if line, err := routerB.ReadLine(h); err != nil || line != "banner" {
		t.Fatalf("handoff read = %q, %v", line, err)
	}
	if _, err := routerB.Write(h, []byte("GET / HTTP/1.0\r\n\r\n")); err != nil {
		t.Fatalf("handoff write: %v", err)
	}
	if line, err := routerB.ReadLine(h); err != nil || line != "200 ok" {
		t.Fatalf("handoff autoreply = %q, %v", line, err)
	}

	// Router A still sees the same simulated endpoint, not a new connection.
	if line, err := routerA.ReadLine(h); !errors.Is(err, capnet.ErrScriptExhausted) {
		t.Fatalf("shared state expected ErrScriptExhausted, got %q %v", line, err)
	}

	// Cross-generation handoff: end the trial and mint a fresh generation.
	trial.End()
	trial2 := host.NewTrial()
	h2, err := trial2.Mint("198.51.100.9:8443", capnet.Script{Inbox: [][]byte{[]byte("fresh\n")}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := routerB.Read(h, make([]byte, 4)); !errors.Is(err, capnet.ErrStaleHandle) {
		t.Fatalf("stolen stale handle: want ErrStaleHandle, got %v", err)
	}
	if _, err := routerB.AfterFunc(h, time.Second, func() {}); !errors.Is(err, capnet.ErrStaleHandle) {
		t.Fatalf("stale timer schedule: want ErrStaleHandle, got %v", err)
	}
	if line, err := routerB.ReadLine(h2); err != nil || line != "fresh" {
		t.Fatalf("new generation read = %q, %v", line, err)
	}
	if spy.Count() != 0 {
		t.Fatalf("real dial callback fired %d times during handoff", spy.Count())
	}
}

// A delayed callback that retains the handle must not gain any capability
// after the trial generation ends, and no timer or callback can reach the
// real dialer.
func TestMaliciousDelayedCallback(t *testing.T) {
	_, trial, h, router, spy := newWorld(t)

	// Case 1: a callback fires after simulated time, but the generation has
	// already ended, so its retained handle is rejected.
	var mu sync.Mutex
	var fired bool
	var lateErr error
	t1, err := router.AfterFunc(h, time.Hour, func() {
		mu.Lock()
		defer mu.Unlock()
		fired = true
		_, lateErr = router.Write(h, []byte("late"))
	})
	if err != nil {
		t.Fatal(err)
	}

	// Case 2: a long simulated Sleep in a plug-in goroutine is interrupted by
	// generation end.
	var sleepErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sleepErr = router.Sleep(h, 30*time.Minute)
	}()

	// Stop case 1 explicitly, then prove another timer is dropped by End().
	if !t1.Stop() {
		t.Fatal("timer should still be active before clock advance")
	}
	var dropped bool
	if _, err := router.AfterFunc(h, time.Hour, func() { dropped = true }); err != nil {
		t.Fatal(err)
	}

	trial.End()
	trial.Advance(2 * time.Hour)
	wg.Wait()

	if !errors.Is(sleepErr, capnet.ErrStaleHandle) {
		t.Fatalf("interrupted sleep: want ErrStaleHandle, got %v", sleepErr)
	}
	mu.Lock()
	if fired {
		t.Fatal("stopped timer callback ran")
	}
	if !errors.Is(lateErr, capnet.ErrStaleHandle) && lateErr != nil {
		t.Fatalf("unexpected late error: %v", lateErr)
	}
	mu.Unlock()
	if dropped {
		t.Fatal("timer callback survived trial end")
	}

	// Case 3: schedule a callback, advance the clock first, then end the
	// generation right away: the stale-handle check inside any late work still
	// holds.
	if _, err := router.AfterFunc(h, time.Nanosecond, func() {}); !errors.Is(err, capnet.ErrStaleHandle) {
		t.Fatalf("scheduling on ended trial: want ErrStaleHandle, got %v", err)
	}
	if spy.Count() != 0 {
		t.Fatalf("real dial callback fired %d times via delayed callback", spy.Count())
	}
}

// A callback that does fire under simulated time still operates through the
// capability router and cannot dial.
func TestFiredCallbackUsesSimulator(t *testing.T) {
	_, trial, h, router, spy := newWorld(t)
	var wg sync.WaitGroup
	wg.Add(1)
	var writeErr error
	if _, err := router.AfterFunc(h, 5*time.Second, func() {
		defer wg.Done()
		_, writeErr = router.Write(h, []byte("GET / HTTP/1.0\r\n\r\n"))
	}); err != nil {
		t.Fatal(err)
	}
	trial.Advance(5 * time.Second)
	wg.Wait()
	if writeErr != nil {
		t.Fatalf("fired callback write: %v", writeErr)
	}
	if spy.Count() != 0 {
		t.Fatalf("real dial callback fired %d times from timer callback", spy.Count())
	}
}

// Brute-forcing endpoint ids or generation numbers can only produce handles
// that fail resolution; the 128-bit token cannot be guessed and failures
// happen before any I/O.
func TestMaliciousIDGuessing(t *testing.T) {
	host, _, _, router, spy := newWorld(t)

	// Without a minted handle the plug-in only has the zero value. Hammer it
	// across every capability operation: none may reach I/O.
	var guess capnet.Handle
	for attempt := 0; attempt < 1024; attempt++ {
		if _, err := router.Read(guess, make([]byte, 2)); !errors.Is(err, capnet.ErrBadHandle) {
			t.Fatalf("attempt %d: guessed handle accepted", attempt)
		}
		if _, err := router.Write(guess, []byte("x")); !errors.Is(err, capnet.ErrBadHandle) {
			t.Fatalf("attempt %d: guessed write accepted", attempt)
		}
		if _, err := router.ReadLine(guess); !errors.Is(err, capnet.ErrBadHandle) {
			t.Fatalf("attempt %d: guessed parse accepted", attempt)
		}
		if err := router.SetDeadline(guess, time.Now()); !errors.Is(err, capnet.ErrBadHandle) {
			t.Fatalf("attempt %d: guessed deadline accepted", attempt)
		}
		if _, err := router.AfterFunc(guess, time.Second, func() {}); !errors.Is(err, capnet.ErrBadHandle) {
			t.Fatalf("attempt %d: guessed timer accepted", attempt)
		}
	}

	// Handles minted by a different host are meaningless here, even though
	// they are well-formed capabilities in their own host.
	other := capnet.NewHost(nil)
	foreign, err := other.NewTrial().Mint("203.0.113.5:443", capnet.Script{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := router.Write(foreign, []byte("x")); !errors.Is(err, capnet.ErrBadHandle) {
		t.Fatalf("foreign-host handle: want ErrBadHandle, got %v", err)
	}

	// A second router on the same host still cannot invent capabilities.
	if _, err := capnet.NewRouter(host).Read(capnet.Handle{}, nil); !errors.Is(err, capnet.ErrBadHandle) {
		t.Fatalf("nil read on zero handle: want ErrBadHandle, got %v", err)
	}
	if spy.Count() != 0 {
		t.Fatalf("real dial callback fired %d times while guessing ids", spy.Count())
	}
}

// The package must not expose any constructor or parser that turns an
// address, id or string into a Handle: minting stays exclusively on
// Trial, which only the host can create.
func TestNoHandleConstructorOrParser(t *testing.T) {
	pkgType := reflect.TypeOf(capnet.Handle{})
	for i := 0; i < pkgType.NumMethod(); i++ {
		m := pkgType.Method(i)
		switch m.Name {
		case "String", "GoString":
		default:
			t.Fatalf("Handle exposes unexpected method %s", m.Name)
		}
	}

	hostType := reflect.TypeOf(capnet.NewHost(nil))
	for i := 0; i < hostType.NumMethod(); i++ {
		if hostType.Method(i).Name == "Mint" {
			t.Fatal("Host must not mint handles directly; minting is scoped to Trial")
		}
	}

	routerType := reflect.TypeOf(capnet.NewRouter(capnet.NewHost(nil)))
	banned := map[string]bool{"Parse": true, "ParseHandle": true, "DialAddr": true, "Mint": true, "NewHandle": true, "HandleFrom": true}
	for i := 0; i < routerType.NumMethod(); i++ {
		if banned[routerType.Method(i).Name] {
			t.Fatalf("Router exposes capability-creating method %s", routerType.Method(i).Name)
		}
	}
}

// Timing and parsing are fully deterministic and simulated: repeated trials
// with identical scripts produce identical byte sequences and fake timelines.
func TestDeterministicReplay(t *testing.T) {
	spy := &spyDialer{}
	run := func() (string, time.Time, time.Time) {
		host := capnet.NewHost(spy.Dial)
		trial := host.NewTrial()
		h, err := trial.Mint("t:1", capnet.Script{
			Inbox: [][]byte{[]byte("line1\n"), []byte("line2\n")},
		})
		if err != nil {
			t.Fatal(err)
		}
		r := capnet.NewRouter(host)
		start, _ := r.Now(h)
		l1, _ := r.ReadLine(h)
		l2, _ := r.ReadLine(h)
		trial.Advance(7 * time.Second)
		end, _ := r.Now(h)
		return l1 + "|" + l2, start, end
	}
	out1, s1, e1 := run()
	out2, s2, e2 := run()
	if out1 != "line1|line2" || out1 != out2 || !s1.Equal(s2) || !e1.Equal(e2) {
		t.Fatalf("non-deterministic replay: %q %v %v vs %q %v %v", out1, s1, e1, out2, s2, e2)
	}
	if d := e1.Sub(s1); d != 7*time.Second {
		t.Fatalf("simulated duration = %v, want 7s", d)
	}
	if spy.Count() != 0 {
		t.Fatalf("real dial callback fired %d times during replay", spy.Count())
	}
}
