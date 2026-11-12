package capnet

import (
	"bytes"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type dialCounter struct {
	mu    sync.Mutex
	calls []dialCall
}

type dialCall struct {
	network string
	address string
}

func (d *dialCounter) dial(network, address string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, dialCall{network: network, address: address})
	return nil
}

func (d *dialCounter) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.calls)
}

func testHost(t *testing.T) (*Host, *dialCounter) {
	t.Helper()
	dc := &dialCounter{}
	return NewHost(dc.dial), dc
}

func TestForgedHandleFailsTokenCheck(t *testing.T) {
	host, dc := testHost(t)
	trial := host.NewTrial()
	good, err := trial.Mint("10.0.0.9:443", Script{Inbox: [][]byte{[]byte("hi")}})
	if err != nil {
		t.Fatal(err)
	}

	forged := good
	forged.tok[0] ^= 0xff
	if _, err := host.resolve(forged); !errors.Is(err, ErrBadHandle) {
		t.Fatalf("token-forged handle: want ErrBadHandle, got %v", err)
	}

	forgedGen := good
	forgedGen.gen = good.gen + 99
	if _, err := host.resolve(forgedGen); !errors.Is(err, ErrBadHandle) {
		t.Fatalf("gen-forged handle: want ErrBadHandle, got %v", err)
	}

	forgedID := good
	forgedID.id = good.id + 99
	if _, err := host.resolve(forgedID); !errors.Is(err, ErrBadHandle) {
		t.Fatalf("id-forged handle: want ErrBadHandle, got %v", err)
	}

	if _, err := host.resolve(Handle{}); !errors.Is(err, ErrBadHandle) {
		t.Fatalf("zero handle: want ErrBadHandle, got %v", err)
	}
	if dc.count() != 0 {
		t.Fatalf("real dialer invoked %d times during forgery attempts", dc.count())
	}
}

func TestForeignHostHandleRejected(t *testing.T) {
	hostA, _ := testHost(t)
	hostB, dcB := testHost(t)
	hA, err := hostA.NewTrial().Mint("a:1", Script{})
	if err != nil {
		t.Fatal(err)
	}
	rB := NewRouter(hostB)
	if _, err := rB.Read(hA, make([]byte, 4)); !errors.Is(err, ErrBadHandle) {
		t.Fatalf("foreign host handle: want ErrBadHandle, got %v", err)
	}
	if dcB.count() != 0 {
		t.Fatal("foreign handle caused a real dial")
	}
}

func TestDeterministicSimulatorRoundTrip(t *testing.T) {
	host, dc := testHost(t)
	trial := host.NewTrial()
	script := Script{
		Inbox:        [][]byte{[]byte("hello\n"), []byte("world\n")},
		ExpectWrites: [][]byte{[]byte("ping"), []byte("pong")},
		AutoReply:    map[string][]byte{"ping": []byte("pong-back\n")},
	}
	h, err := trial.Mint("10.0.0.1:443", script)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRouter(host)

	line, err := r.ReadLine(h)
	if err != nil || line != "hello" {
		t.Fatalf("ReadLine = %q, %v", line, err)
	}
	if n, err := r.Write(h, []byte("ping")); err != nil || n != 4 {
		t.Fatalf("Write ping: %d, %v", n, err)
	}
	line, err = r.ReadLine(h)
	if err != nil || line != "world" {
		t.Fatalf("ReadLine = %q, %v", line, err)
	}
	line, err = r.ReadLine(h)
	if err != nil || line != "pong-back" {
		t.Fatalf("autoreply = %q, %v", line, err)
	}
	if n, err := r.Write(h, []byte("pong")); err != nil || n != 4 {
		t.Fatalf("Write pong: %d, %v", n, err)
	}
	if _, err := r.ReadLine(h); !errors.Is(err, ErrScriptExhausted) {
		t.Fatalf("exhausted script: want ErrScriptExhausted, got %v", err)
	}
	writes, err := trial.Writes(h)
	if err != nil || len(writes) != 2 || string(writes[1]) != "pong" {
		t.Fatalf("host writes = %v, %v", writes, err)
	}

	// A fresh endpoint with an expected sequence rejects an out-of-order write.
	hw, err := trial.Mint("10.0.0.2:443", Script{ExpectWrites: [][]byte{[]byte("only-this")}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewRouter(host).Write(hw, []byte("wrong")); !errors.Is(err, ErrUnexpectedWrite) {
		t.Fatalf("unexpected write: want ErrUnexpectedWrite, got %v", err)
	}
	if dc.count() != 0 {
		t.Fatal("real dialer invoked during simulated round trip")
	}
}

func TestFramesAndSyntheticAddresses(t *testing.T) {
	host, dc := testHost(t)
	trial := host.NewTrial()
	frame := []byte{0, 0, 0, 3, 'a', 'b', 'c'}
	h, err := trial.Mint("secret.internal:8443", Script{
		Inbox: [][]byte{frame},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := NewRouter(host)
	got, err := r.ReadFrame(h)
	if err != nil || string(got) != "abc" {
		t.Fatalf("ReadFrame = %q, %v", got, err)
	}
	if err := r.WriteFrame(h, []byte("xyz")); err != nil {
		t.Fatal(err)
	}
	addr, err := r.RemoteAddr(h)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(addr.String(), "secret.internal") {
		t.Fatalf("real address leaked through RemoteAddr: %q", addr)
	}
	if strings.Contains(h.String(), "secret.internal") || h.String() != "capnet:handle[redacted]" {
		t.Fatalf("handle string leaks identity: %q", h.String())
	}
	if dc.count() != 0 {
		t.Fatal("real dialer invoked")
	}
}

func TestDeadlineOnFakeClock(t *testing.T) {
	host, _ := testHost(t)
	trial := host.NewTrial()
	h, err := trial.Mint("x:1", Script{Inbox: [][]byte{[]byte("abc")}})
	if err != nil {
		t.Fatal(err)
	}
	r := NewRouter(host)
	deadline := trial.Now().Add(-time.Nanosecond)
	if err := r.SetReadDeadline(h, deadline); err != nil {
		t.Fatal(err)
	}

	before := trial.Now()
	if _, err := r.Read(h, make([]byte, 4)); !errors.Is(err, ErrDeadlineExceeded) {
		t.Fatalf("before advance want ErrDeadlineExceeded, got %v", err)
	}
	var ne net.Error
	if _, err := r.Read(h, make([]byte, 4)); !(errors.As(err, &ne) && ne.Timeout()) {
		t.Fatalf("deadline error must satisfy net.Error timeout: %v", err)
	}
	trial.Advance(time.Second)
	if !trial.Now().Equal(before.Add(time.Second)) {
		t.Fatal("fake clock did not advance deterministically")
	}
}

func TestTimersAreFakeAndCancelledOnEnd(t *testing.T) {
	host, _ := testHost(t)
	trial := host.NewTrial()
	h, err := trial.Mint("x:1", Script{})
	if err != nil {
		t.Fatal(err)
	}
	r := NewRouter(host)

	var fired atomic.Bool
	tm, err := r.AfterFunc(h, 10*time.Second, func() { fired.Store(true) })
	if err != nil {
		t.Fatal(err)
	}
	trial.Advance(5 * time.Second)
	if fired.Load() {
		t.Fatal("timer fired before simulated deadline")
	}
	if !tm.Stop() {
		t.Fatal("Stop should report an active timer")
	}
	trial.Advance(10 * time.Second)
	if fired.Load() {
		t.Fatal("stopped timer fired")
	}

	var late atomic.Bool
	if _, err := r.AfterFunc(h, time.Second, func() { late.Store(true) }); err != nil {
		t.Fatal(err)
	}
	trial.End()
	trial.Advance(time.Hour)
	if late.Load() {
		t.Fatal("timer survived trial end")
	}
}

func TestStaleHandlesRejectedAfterGenerationEnd(t *testing.T) {
	host, dc := testHost(t)
	trial := host.NewTrial()
	h, err := trial.Mint("x:1", Script{Inbox: [][]byte{[]byte("data")}})
	if err != nil {
		t.Fatal(err)
	}
	r := NewRouter(host)
	trial.End()

	if _, err := r.Read(h, make([]byte, 4)); !errors.Is(err, ErrStaleHandle) {
		t.Fatalf("read stale: want ErrStaleHandle, got %v", err)
	}
	if _, err := r.Write(h, []byte("z")); !errors.Is(err, ErrStaleHandle) {
		t.Fatalf("write stale: want ErrStaleHandle, got %v", err)
	}
	if _, err := r.Now(h); !errors.Is(err, ErrStaleHandle) {
		t.Fatalf("now stale: want ErrStaleHandle, got %v", err)
	}
	if _, err := r.ReadLine(h); !errors.Is(err, ErrStaleHandle) {
		t.Fatalf("parse stale: want ErrStaleHandle, got %v", err)
	}
	if _, err := trial.Mint("y:2", Script{}); !errors.Is(err, ErrTrialEnded) {
		t.Fatalf("mint on ended trial: want ErrTrialEnded, got %v", err)
	}

	trial2 := host.NewTrial()
	h2, err := trial2.Mint("y:2", Script{Inbox: [][]byte{[]byte("gen2")}})
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if n, err := r.Read(h2, buf); err != nil || string(buf[:n]) != "gen2" {
		t.Fatalf("new generation read = %q, %v", buf[:n], err)
	}
	if dc.count() != 0 {
		t.Fatal("real dialer invoked across generations")
	}
}

func TestEOFAndClose(t *testing.T) {
	host, _ := testHost(t)
	trial := host.NewTrial()
	h, err := trial.Mint("x:1", Script{Inbox: [][]byte{[]byte("ok")}, CloseAfterScript: true})
	if err != nil {
		t.Fatal(err)
	}
	r := NewRouter(host)
	buf := make([]byte, 2)
	if n, err := r.Read(h, buf); err != nil || n != 2 {
		t.Fatalf("read = %d, %v", n, err)
	}
	if _, err := r.Read(h, buf); !errors.Is(err, io.EOF) {
		t.Fatalf("want io.EOF, got %v", err)
	}
	if err := r.Close(h); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(h, buf); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed conn: want net.ErrClosed, got %v", err)
	}
}

func TestTokensAreRandomAndRedacted(t *testing.T) {
	host, _ := testHost(t)
	trial := host.NewTrial()
	h1, _ := trial.Mint("a:1", Script{})
	h2, _ := trial.Mint("a:1", Script{})
	if bytes.Equal(h1.tok[:], h2.tok[:]) {
		t.Fatal("two minted handles share a token")
	}
	if h1.String() != h2.String() {
		t.Fatal("redaction must be uniform")
	}
	if strings.Contains(h1.GoString(), "10.") || strings.Contains(h1.GoString(), "tok") {
		t.Fatalf("GoString leaks internals: %s", h1.GoString())
	}
}
