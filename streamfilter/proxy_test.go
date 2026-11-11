package streamfilter

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// duplexPipe is a controllable full-duplex in-memory stream with
// half-close support, used to drive a Proxy in tests.
type duplexPipe struct {
	r *io.PipeReader
	w *io.PipeWriter
}

func newDuplexPair() (*duplexPipe, *duplexPipe) {
	ar, bw := io.Pipe()
	br, aw := io.Pipe()
	return &duplexPipe{r: ar, w: aw}, &duplexPipe{r: br, w: bw}
}

func (d *duplexPipe) Read(p []byte) (int, error)  { return d.r.Read(p) }
func (d *duplexPipe) Write(p []byte) (int, error) { return d.w.Write(p) }
func (d *duplexPipe) CloseWrite() error           { return d.w.Close() }

func (d *duplexPipe) Close() error {
	d.r.Close()
	d.w.Close()
	return nil
}

func ep(d *duplexPipe) Endpoint { return Endpoint{In: d, Out: d} }

// watchBuf collects everything one side of the session receives.
type watchBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *watchBuf) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *watchBuf) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

type recEvent struct {
	dir  Direction
	data []byte
}

// memRecorder is a test persistence layer. If release is non-nil,
// Record blocks until the channel is closed, simulating backpressure.
type memRecorder struct {
	mu      sync.Mutex
	events  []recEvent
	release chan struct{}
}

func (m *memRecorder) Record(dir Direction, p []byte) error {
	if m.release != nil {
		<-m.release
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, recEvent{dir: dir, data: append([]byte(nil), p...)})
	return nil
}

func (m *memRecorder) transcript(dir Direction) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var sb strings.Builder
	for _, e := range m.events {
		if e.dir == dir {
			sb.Write(e.data)
		}
	}
	return sb.String()
}

func (m *memRecorder) all() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var sb strings.Builder
	for _, e := range m.events {
		sb.Write(e.data)
	}
	return sb.String()
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func writeAll(t *testing.T, w io.Writer, s string) {
	t.Helper()
	if _, err := io.WriteString(w, s); err != nil {
		t.Fatalf("write %q: %v", s, err)
	}
}

func runProxy(ctx context.Context, px *Proxy, local, remote *duplexPipe) <-chan error {
	errCh := make(chan error, 1)
	go func() { errCh <- px.Run(ctx, ep(local), ep(remote)) }()
	return errCh
}

func TestProxyMaskingOrderingAndHalfClose(t *testing.T) {
	user, proxyLocal := newDuplexPair()
	proxyRemote, server := newDuplexPair()
	rec := &memRecorder{}
	px := &Proxy{Filter: NewFilter(0), Recorder: rec, BufSize: 16}

	runErr := runProxy(context.Background(), px, proxyLocal, proxyRemote)

	var serverGot, userGot watchBuf
	serverEOF := make(chan struct{})
	go func() { io.Copy(&serverGot, server.r); close(serverEOF) }()
	go io.Copy(&userGot, user.r)

	// Normal traffic with echo on.
	writeAll(t, user.w, "sudo -s\r")
	waitFor(t, "server to receive command", func() bool {
		return strings.Contains(serverGot.String(), "sudo -s\r")
	})
	writeAll(t, server.w, "sudo -s\r\n[sudo] password for ops: ")
	waitFor(t, "user to see prompt", func() bool {
		return strings.Contains(userGot.String(), "password for ops: ")
	})

	// Echo off: the password must reach the server but never the
	// recorder; its late echo (arriving in two chunks) is masked too.
	px.Filter.SetEcho(false)
	writeAll(t, user.w, "hunter2\r")
	waitFor(t, "server to receive password", func() bool {
		return strings.Contains(serverGot.String(), "hunter2\r")
	})
	writeAll(t, server.w, "hunt")
	writeAll(t, server.w, "er2\r\n")
	waitFor(t, "user to see late echo", func() bool {
		return strings.Contains(userGot.String(), "hunter2\r\n")
	})

	// Echo restored: normal output and ordering resume.
	px.Filter.SetEcho(true)
	writeAll(t, server.w, "ops@prod:~# ")
	writeAll(t, user.w, "id\r")
	writeAll(t, server.w, "id\r\nuid=0(root) gid=0(root)\r\n")
	waitFor(t, "user to see id output", func() bool {
		return strings.Contains(userGot.String(), "uid=0(root)")
	})

	// Half-close: local input ends, remote output keeps flowing.
	if err := user.CloseWrite(); err != nil {
		t.Fatalf("user CloseWrite: %v", err)
	}
	select {
	case <-serverEOF:
	case <-time.After(5 * time.Second):
		t.Fatal("half-close not propagated to remote")
	}
	writeAll(t, server.w, "logout\r\n")
	waitFor(t, "user to see logout", func() bool {
		return strings.Contains(userGot.String(), "logout\r\n")
	})
	if err := server.CloseWrite(); err != nil {
		t.Fatalf("server CloseWrite: %v", err)
	}

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}

	if got, want := rec.transcript(DirInput), "sudo -s\rid\r"; got != want {
		t.Fatalf("recorded input: got %q, want %q", got, want)
	}
	wantOut := "sudo -s\r\n[sudo] password for ops: " +
		"\n" + // only the newline of the late echo survives
		"ops@prod:~# " +
		"id\r\nuid=0(root) gid=0(root)\r\n" +
		"logout\r\n"
	if got := rec.transcript(DirOutput); got != wantOut {
		t.Fatalf("recorded output: got %q, want %q", got, wantOut)
	}
	if strings.Contains(rec.all(), "hunter2") {
		t.Fatal("masked bytes reached the recorder")
	}
}

func TestProxyBackpressureNeverLeaks(t *testing.T) {
	user, proxyLocal := newDuplexPair()
	proxyRemote, server := newDuplexPair()
	release := make(chan struct{})
	rec := &memRecorder{release: release}
	px := &Proxy{Filter: NewFilter(0), Recorder: rec}

	runErr := runProxy(context.Background(), px, proxyLocal, proxyRemote)

	var serverGot, userGot watchBuf
	go io.Copy(&serverGot, server.r)
	go io.Copy(&userGot, user.r)

	// One recorded chunk in each direction stalls both pumps inside
	// the blocked recorder.
	writeAll(t, user.w, "whoami\r")
	writeAll(t, server.w, "whoami\r\n")
	waitFor(t, "recorder to stall", func() bool {
		return strings.Contains(userGot.String(), "whoami\r\n") &&
			strings.Contains(serverGot.String(), "whoami\r")
	})

	// While the recorder is stalled, a password is typed with echo
	// off. It must stay in the pipe (backpressure) and never reach
	// the recorder once the jam clears.
	px.Filter.SetEcho(false)
	writeErr := make(chan error, 1)
	go func() {
		_, err := io.WriteString(user.w, "s3cr3t\r")
		writeErr <- err
	}()
	time.Sleep(50 * time.Millisecond)
	if strings.Contains(serverGot.String(), "s3cr3t") {
		t.Fatal("backpressure not applied: password forwarded while recorder stalled")
	}

	close(release)
	if err := <-writeErr; err != nil {
		t.Fatalf("write password: %v", err)
	}
	waitFor(t, "server to receive password", func() bool {
		return strings.Contains(serverGot.String(), "s3cr3t\r")
	})
	writeAll(t, server.w, "s3cr3t\r\n")
	waitFor(t, "user to see late echo", func() bool {
		return strings.Contains(userGot.String(), "s3cr3t\r\n")
	})

	px.Filter.SetEcho(true)
	if err := user.CloseWrite(); err != nil {
		t.Fatalf("user CloseWrite: %v", err)
	}
	if err := server.CloseWrite(); err != nil {
		t.Fatalf("server CloseWrite: %v", err)
	}
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}

	if got, want := rec.transcript(DirInput), "whoami\r"; got != want {
		t.Fatalf("recorded input: got %q, want %q", got, want)
	}
	if got, want := rec.transcript(DirOutput), "whoami\r\n\n"; got != want {
		t.Fatalf("recorded output: got %q, want %q", got, want)
	}
	if strings.Contains(rec.all(), "s3cr3t") {
		t.Fatal("masked bytes reached the recorder")
	}
}

func TestProxyCancelNeverLeaks(t *testing.T) {
	user, proxyLocal := newDuplexPair()
	proxyRemote, server := newDuplexPair()
	rec := &memRecorder{}
	px := &Proxy{Filter: NewFilter(0), Recorder: rec}
	px.Filter.SetEcho(false)

	ctx, cancel := context.WithCancel(context.Background())
	runErr := runProxy(ctx, px, proxyLocal, proxyRemote)

	var serverGot, userGot watchBuf
	go io.Copy(&serverGot, server.r)
	go io.Copy(&userGot, user.r)

	writeAll(t, user.w, "cancelme\r")
	waitFor(t, "server to receive password", func() bool {
		return strings.Contains(serverGot.String(), "cancelme\r")
	})
	writeAll(t, server.w, "cancelme\r\n")
	waitFor(t, "user to see late echo", func() bool {
		return strings.Contains(userGot.String(), "cancelme\r\n")
	})

	cancel()
	select {
	case err := <-runErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run: got %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}

	if got := rec.transcript(DirInput); got != "" {
		t.Fatalf("recorded input: got %q, want empty", got)
	}
	if strings.Contains(rec.all(), "cancelme") {
		t.Fatal("masked bytes reached the recorder")
	}
}

func TestProxyRecorderErrorAborts(t *testing.T) {
	user, proxyLocal := newDuplexPair()
	proxyRemote, server := newDuplexPair()
	errBoom := errors.New("disk full")
	rec := RecorderFunc(func(Direction, []byte) error { return errBoom })
	px := &Proxy{Filter: NewFilter(0), Recorder: rec}

	runErr := runProxy(context.Background(), px, proxyLocal, proxyRemote)

	var serverGot watchBuf
	go io.Copy(&serverGot, server.r)

	writeAll(t, user.w, "x")
	select {
	case err := <-runErr:
		if !errors.Is(err, errBoom) {
			t.Fatalf("Run: got %v, want %v", err, errBoom)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not abort on recorder error")
	}
}
