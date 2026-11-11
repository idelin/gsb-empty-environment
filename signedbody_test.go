package signedbody

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var testKey = []byte("test-hmac-key-0123456789abcdef")

// pipeListener is an in-memory net.Listener. The sandbox forbids binding
// TCP and unix sockets, so tests run the full net/http server and client
// stack — real HTTP/1.1 requests on the wire — over net.Pipe connections.
type pipeListener struct {
	conns chan net.Conn
	done  chan struct{}
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), done: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, fmt.Errorf("listener closed")
	}
}

func (l *pipeListener) Close() error {
	select {
	case <-l.done:
	default:
		close(l.done)
	}
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr("signedbody.pipe") }

func (l *pipeListener) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	server, client := net.Pipe()
	select {
	case l.conns <- server:
		return client, nil
	case <-l.done:
		return nil, fmt.Errorf("listener closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type pipeAddr string

func (a pipeAddr) Network() string { return "pipe" }
func (a pipeAddr) String() string  { return string(a) }

// testServer serves handler over real HTTP on an in-memory listener and
// returns a client wired to it.
type testServer struct {
	client *http.Client
	url    string
	srv    *http.Server
}

func startServer(t *testing.T, handler http.Handler) *testServer {
	t.Helper()
	l := newPipeListener()
	srv := &http.Server{Handler: handler}
	go srv.Serve(l)
	ts := &testServer{
		client: &http.Client{Transport: &http.Transport{DialContext: l.dial}},
		url:    "http://signedbody.pipe",
		srv:    srv,
	}
	t.Cleanup(func() {
		srv.Close()
		l.Close()
	})
	return ts
}

// recordingHandler echoes the body it receives and counts invocations.
type recordingHandler struct {
	calls atomic.Int32
	got   []byte
}

func (h *recordingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.calls.Add(1)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.got = body
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

func newServer(t *testing.T, budget int64, dir string, next http.Handler) *testServer {
	t.Helper()
	return startServer(t, Middleware(Config{
		Key:          testKey,
		MemoryBudget: budget,
		TempDir:      dir,
	}, next))
}

func signedRequest(t *testing.T, url string, framed []byte, chunks [][]byte) *http.Request {
	t.Helper()
	sig, total := Sign(testKey, chunks)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(framed))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(HeaderSignature, sig)
	req.Header.Set(HeaderTotalLength, strconv.FormatInt(total, 10))
	return req
}

func plaintext(chunks [][]byte) []byte {
	var out []byte
	for _, c := range chunks {
		out = append(out, c...)
	}
	return out
}

func dirEntries(t *testing.T, dir string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func waitDirEmpty(t *testing.T, dir string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(dirEntries(t, dir)) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	entries := dirEntries(t, dir)
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	t.Fatalf("temp dir not reclaimed, leftovers: %v", names)
}

func TestValidBodyServedFromMemory(t *testing.T) {
	dir := t.TempDir()
	handler := &recordingHandler{}
	srv := newServer(t, 1<<20, dir, handler)

	chunks := [][]byte{[]byte("hello, "), []byte("signed "), []byte("world")}
	req := signedRequest(t, srv.url, EncodeBody(chunks), chunks)

	resp, err := srv.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if want := plaintext(chunks); !bytes.Equal(handler.got, want) {
		t.Fatalf("handler got %q, want %q", handler.got, want)
	}
	if want := plaintext(chunks); !bytes.Equal(body, want) {
		t.Fatalf("response echoed %q, want %q", body, want)
	}
	if got := resp.Header.Get("Content-Length"); got != "19" {
		t.Fatalf("response Content-Length = %q, want 19", got)
	}
	waitDirEmpty(t, dir)
}

func TestEmptyBodyIsLegal(t *testing.T) {
	dir := t.TempDir()
	handler := &recordingHandler{}
	srv := newServer(t, 1<<20, dir, handler)

	req := signedRequest(t, srv.url, EncodeBody(nil), nil)
	req.ContentLength = 0

	resp, err := srv.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	// Drain the echoed body so the server handler can return and the
	// middleware can reclaim the spill file.
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if handler.calls.Load() != 1 {
		t.Fatalf("handler calls = %d, want 1", handler.calls.Load())
	}
	if len(handler.got) != 0 {
		t.Fatalf("handler got %d bytes, want empty body", len(handler.got))
	}
}

func TestLargeBodySpillsAndIsReclaimed(t *testing.T) {
	dir := t.TempDir()
	handler := &recordingHandler{}
	srv := newServer(t, 4096, dir, handler) // 4 KiB budget forces spilling

	var chunks [][]byte
	for i := 0; i < 64; i++ {
		chunk := make([]byte, 8192)
		if _, err := rand.Read(chunk); err != nil {
			t.Fatal(err)
		}
		chunks = append(chunks, chunk)
	}
	req := signedRequest(t, srv.url, EncodeBody(chunks), chunks)

	resp, err := srv.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	// Drain the echoed body so the server handler can return and the
	// middleware can reclaim the spill file.
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if want := plaintext(chunks); !bytes.Equal(handler.got, want) {
		t.Fatalf("handler got %d bytes, mismatch with %d plaintext bytes",
			len(handler.got), len(want))
	}
	waitDirEmpty(t, dir)
}

func TestBadFinalChunkRejectedAndReclaimed(t *testing.T) {
	dir := t.TempDir()
	handler := &recordingHandler{}
	srv := newServer(t, 1024, dir, handler) // tiny budget: bad body still spills

	chunks := [][]byte{
		bytes.Repeat([]byte("a"), 4096),
		bytes.Repeat([]byte("b"), 4096),
		bytes.Repeat([]byte("c"), 4096),
	}
	framed := EncodeBody(chunks)
	// Corrupt one payload byte inside the final chunk, keeping the length
	// and the (previously valid) signature intact.
	framed[len(framed)-1] ^= 0xff

	req := signedRequest(t, srv.url, framed, chunks)
	resp, err := srv.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if handler.calls.Load() != 0 {
		t.Fatalf("downstream called %d times with unverified body", handler.calls.Load())
	}
	waitDirEmpty(t, dir)
}

func TestWrongTotalLengthRejected(t *testing.T) {
	dir := t.TempDir()
	handler := &recordingHandler{}
	srv := newServer(t, 1<<20, dir, handler)

	chunks := [][]byte{[]byte("abc")}
	sig, _ := Sign(testKey, chunks)
	req, err := http.NewRequest(http.MethodPost, srv.url, bytes.NewReader(EncodeBody(chunks)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(HeaderSignature, sig)
	req.Header.Set(HeaderTotalLength, "4") // lie about the total

	resp, err := srv.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if handler.calls.Load() != 0 {
		t.Fatal("downstream called with unverified body")
	}
}

func TestConcurrentCancellationReclaimsFiles(t *testing.T) {
	dir := t.TempDir()
	handler := &recordingHandler{}
	srv := newServer(t, 2048, dir, handler)

	const clients = 16
	var wg sync.WaitGroup
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			pr, pw := io.Pipe()
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.url, pr)
			if err != nil {
				t.Error(err)
				return
			}
			// Sign a longer stream than we will actually send: the upload
			// is cancelled mid-flight, so verification can never complete.
			chunks := [][]byte{bytes.Repeat([]byte{byte(i)}, 16<<10)}
			sig, total := Sign(testKey, chunks)
			req.Header.Set(HeaderSignature, sig)
			req.Header.Set(HeaderTotalLength, strconv.FormatInt(total, 10))
			req.ContentLength = int64(4 + 16<<10)

			respDone := make(chan error, 1)
			go func() {
				resp, err := srv.client.Do(req)
				if err == nil {
					resp.Body.Close()
				}
				respDone <- err
			}()

			// Stream enough frames to exceed the memory budget and spill,
			// then cancel the request context mid-upload.
			frame := make([]byte, 4+1024)
			frame[3] = 4 // length = 1024 big-endian
			for j := 0; j < 8; j++ {
				if _, err := pw.Write(frame); err != nil {
					break
				}
			}
			cancel()
			pw.CloseWithError(fmt.Errorf("client %d went away", i))

			if err := <-respDone; err == nil {
				t.Errorf("client %d: expected request error after cancel", i)
			}
		}(i)
	}
	wg.Wait()

	if handler.calls.Load() != 0 {
		t.Fatalf("downstream called %d times for cancelled uploads", handler.calls.Load())
	}
	waitDirEmpty(t, dir)
}

func TestSpillFileIsRequestSpecific(t *testing.T) {
	dir := t.TempDir()
	created := make(chan string, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Error(err)
			return
		}
		for _, e := range entries {
			created <- filepath.Base(e.Name())
		}
		io.Copy(io.Discard, r.Body)
	})
	srv := newServer(t, 8, dir, handler)

	chunks := [][]byte{bytes.Repeat([]byte("x"), 4096)}
	req := signedRequest(t, srv.url, EncodeBody(chunks), chunks)
	resp, err := srv.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	select {
	case name := <-created:
		if !bytes.HasPrefix([]byte(name), []byte("signedbody-req-")) {
			t.Fatalf("spill file %q lacks request-specific prefix", name)
		}
	default:
		t.Fatal("no spill file observed while handler ran")
	}
	waitDirEmpty(t, dir)
}
