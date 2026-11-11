package rangedl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// pipeListener hands each accepted connection from net.Pipe, so tests need no
// TCP port (the sandbox forbids bind) while exercising the real net/http stack.
type pipeListener struct {
	conns chan net.Conn
	done  chan struct{}
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn, 64), done: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
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

func (l *pipeListener) Addr() net.Addr { return pipeAddr{} }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }

// mockServer is a configurable file server used to inject faults.
//
// The hook receives the one-based count of requests served and may return:
//   - "disconnect": close the TCP connection mid-response
//   - "status:NNN": respond with that HTTP status
//   - "badrange":   lie about the Content-Range header on ranged requests
//   - "":           serve the request normally
type mockServer struct {
	mu sync.Mutex

	versions     [][]byte
	current      int
	ignoreRange  bool
	honorIfRange bool

	count   int
	hook    func(n int, r *http.Request) string
	ranges  []string
	ifRange []string
}

func newMockServer(data []byte) *mockServer {
	return &mockServer{
		versions:     [][]byte{data},
		honorIfRange: true,
	}
}

func (m *mockServer) setVersion(data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.versions = append(m.versions, data)
	m.current = len(m.versions) - 1
}

func (m *mockServer) etag() string {
	return fmt.Sprintf(`"v%d"`, m.current)
}

// pipeServer runs the mock handler over in-memory net.Pipe connections.
type pipeServer struct {
	listener *pipeListener
	server   *http.Server
}

func startPipeServer(handler http.Handler) *pipeServer {
	ln := newPipeListener()
	srv := &http.Server{Handler: handler}
	go srv.Serve(ln)
	return &pipeServer{listener: ln, server: srv}
}

func (p *pipeServer) close() {
	p.server.Close()
}

// client returns an HTTP client whose every request gets a fresh pipe pair,
// mirroring real per-request TCP semantics including retries and disconnects.
func (p *pipeServer) client() *http.Client {
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				clientConn, serverConn := net.Pipe()
				select {
				case p.listener.conns <- serverConn:
					return clientConn, nil
				case <-ctx.Done():
					clientConn.Close()
					serverConn.Close()
					return nil, ctx.Err()
				}
			},
		},
	}
}

func (m *mockServer) handler(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.count++
	n := m.count
	data := append([]byte(nil), m.versions[m.current]...)
	etag := m.etag()
	ignore := m.ignoreRange
	honorIf := m.honorIfRange
	hook := m.hook
	m.ranges = append(m.ranges, r.Header.Get("Range"))
	m.ifRange = append(m.ifRange, r.Header.Get("If-Range"))
	m.mu.Unlock()

	h := w.Header()
	h.Set("ETag", etag)
	h.Set("Last-Modified", "Wed, 11 Nov 2026 00:00:00 GMT")
	h.Set("Accept-Ranges", "bytes")

	if hook != nil {
		effect := hook(n, r)
		switch {
		case effect == "disconnect":
			if hj, ok := w.(http.Hijacker); ok {
				if conn, brw, err := hj.Hijack(); err == nil {
					brw.Flush()
					conn.Close()
					return
				}
			}
		case effect == "badrange" && r.Header.Get("Range") != "":
			h.Set("Content-Range", "bytes 999999-1000000/"+strconv.Itoa(len(data)))
			w.WriteHeader(http.StatusPartialContent)
			io.Copy(w, strings.NewReader("garbage"))
			return
		case strings.HasPrefix(effect, "status:"):
			code, _ := strconv.Atoi(strings.TrimPrefix(effect, "status:"))
			w.WriteHeader(code)
			return
		}
	}

	rangeHdr := r.Header.Get("Range")
	ifRangeHdr := r.Header.Get("If-Range")
	if rangeHdr != "" && honorIf && ifRangeHdr != "" && ifRangeHdr != etag {
		// RFC 7233: a stale If-Range precondition fails instead of serving
		// another version under the requested range.
		w.WriteHeader(http.StatusPreconditionFailed)
		return
	}
	if rangeHdr == "" || ignore {
		// No Range, Range ignored, or stale If-Range per RFC 7233: full entity.
		h.Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(http.StatusOK)
		w.Write(data)
		return
	}

	start, end, ok := parseRequestRange(rangeHdr, int64(len(data)))
	if !ok {
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
	h.Set("Content-Length", strconv.FormatInt(end-start+1, 10))
	w.WriteHeader(http.StatusPartialContent)
	w.Write(data[start : end+1])
}

func parseRequestRange(hdr string, size int64) (start, end int64, ok bool) {
	const p = "bytes="
	if !strings.HasPrefix(hdr, p) {
		return 0, 0, false
	}
	parts := strings.SplitN(strings.TrimPrefix(hdr, p), "-", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	start, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	end, err = strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	if start < 0 || end < start || start >= size {
		return 0, 0, false
	}
	if end >= size {
		end = size - 1
	}
	return start, end, true
}

func testDownloader(client *http.Client) *Downloader {
	return NewDownloader(Config{
		Concurrency:      3,
		ChunkSize:        64,
		MaxChunkAttempts: 6,
		MaxRestarts:      3,
		RetryDelay:       5 * time.Millisecond,
		HTTPClient:       client,
	})
}

func sha256Hex(t *testing.T, data []byte) string {
	t.Helper()
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func runMock(t *testing.T, m *mockServer) (*pipeServer, *http.Client) {
	t.Helper()
	srv := startPipeServer(http.HandlerFunc(m.handler))
	t.Cleanup(srv.close)
	return srv, srv.client()
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

const pipeURL = "http://pipe.local/file"

func TestHappyPathConcurrentRanges(t *testing.T) {
	data := make([]byte, 500)
	for i := range data {
		data[i] = byte(i * 7)
	}
	m := newMockServer(data)
	_, client := runMock(t, m)

	dest := filepath.Join(t.TempDir(), "out.bin")
	res, err := testDownloader(client).Download(context.Background(), pipeURL, dest)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if got := readFile(t, dest); string(got) != string(data) {
		t.Fatalf("content mismatch (got %d bytes, want %d)", len(got), len(data))
	}
	if res.SHA256 != sha256Hex(t, data) {
		t.Fatalf("sha256 mismatch")
	}
	if res.Size != int64(len(data)) || res.ETag != m.etag() {
		t.Fatalf("unexpected result: %+v", res)
	}

	m.mu.Lock()
	usedRange := false
	for _, rh := range m.ranges {
		if strings.HasPrefix(rh, "bytes=") {
			usedRange = true
		}
	}
	m.mu.Unlock()
	if !usedRange {
		t.Fatalf("expected ranged requests, got %v", m.ranges)
	}
}

func TestResumeAfterDisconnects(t *testing.T) {
	data := make([]byte, 400)
	for i := range data {
		data[i] = byte(i)
	}
	m := newMockServer(data)
	// Kill the first two connections (probe + first chunk), then serve.
	m.hook = func(n int, _ *http.Request) string {
		if n <= 2 {
			return "disconnect"
		}
		return ""
	}
	_, client := runMock(t, m)

	dest := filepath.Join(t.TempDir(), "out.bin")
	if _, err := testDownloader(client).Download(context.Background(), pipeURL, dest); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if got := readFile(t, dest); string(got) != string(data) {
		t.Fatalf("content mismatch after disconnects")
	}

	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatalf("partial file not removed: %v", err)
	}
	if _, err := os.Stat(dest + ".part.json"); !os.IsNotExist(err) {
		t.Fatalf("state file not removed: %v", err)
	}
}

func TestResumeSkipsCompletedChunks(t *testing.T) {
	data := make([]byte, 300)
	for i := range data {
		data[i] = byte(i * 3)
	}
	m := newMockServer(data)
	m.hook = func(n int, _ *http.Request) string {
		if n >= 5 {
			return "disconnect"
		}
		return ""
	}
	_, client := runMock(t, m)

	dest := filepath.Join(t.TempDir(), "out.bin")
	dl := testDownloader(client)

	if _, err := dl.Download(context.Background(), pipeURL, dest); err == nil {
		t.Fatalf("expected first run to fail after injected disconnect")
	}

	st, err := loadState(dest + ".part.json")
	if err != nil {
		t.Fatalf("state should survive failure: %v", err)
	}
	done := 0
	for _, c := range st.Chunks {
		if c.Done {
			done++
		}
	}
	if done == 0 {
		t.Fatalf("expected at least one completed chunk on disk")
	}

	m.mu.Lock()
	before := len(m.ranges)
	m.hook = nil
	m.mu.Unlock()

	if _, err := dl.Download(context.Background(), pipeURL, dest); err != nil {
		t.Fatalf("resume Download: %v", err)
	}
	if got := readFile(t, dest); string(got) != string(data) {
		t.Fatalf("content mismatch after resume")
	}
	m.mu.Lock()
	after := len(m.ranges)
	m.mu.Unlock()
	if after <= before {
		t.Fatalf("expected additional requests on resume: before=%d after=%d", before, after)
	}
}

func makeVersion2() []byte {
	v2 := make([]byte, 250)
	for i := range v2 {
		v2[i] = byte(255 - i)
	}
	return v2
}

func TestVersionChangeRestartsFromScratch(t *testing.T) {
	v1 := make([]byte, 350)
	for i := range v1 {
		v1[i] = byte(i)
	}
	m := newMockServer(v1)
	swapped := false
	m.hook = func(n int, _ *http.Request) string {
		if n == 5 && !swapped {
			m.setVersion(makeVersion2())
			swapped = true
			return "disconnect"
		}
		return ""
	}
	_, client := runMock(t, m)

	dest := filepath.Join(t.TempDir(), "out.bin")
	dl := testDownloader(client)
	res, err := dl.Download(context.Background(), pipeURL, dest)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	want := makeVersion2()
	if got := readFile(t, dest); string(got) != string(want) {
		t.Fatalf("expected restarted download of the new version")
	}
	if res.ETag != m.etag() || !res.Restarted {
		t.Fatalf("expected restart result, got %+v", res)
	}
	if res.SHA256 != sha256Hex(t, want) {
		t.Fatalf("checksum of restarted file is wrong")
	}
}

func TestVersionChangeViaPreconditionFailed(t *testing.T) {
	v1 := make([]byte, 320)
	for i := range v1 {
		v1[i] = byte(i)
	}
	m := newMockServer(v1)
	swapped := false
	m.hook = func(n int, r *http.Request) string {
		// After some progress, swap the version; subsequent If-Range
		// requests then fail with 412 Precondition Failed.
		if n == 4 && !swapped {
			m.setVersion(makeVersion2())
			swapped = true
		}
		return ""
	}
	_, client := runMock(t, m)

	dest := filepath.Join(t.TempDir(), "out.bin")
	res, err := testDownloader(client).Download(context.Background(), pipeURL, dest)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	want := makeVersion2()
	if got := readFile(t, dest); string(got) != string(want) {
		t.Fatalf("expected content of the new version after 412 restart")
	}
	if !res.Restarted || res.ETag != m.etag() {
		t.Fatalf("expected restart on 412, got %+v", res)
	}

	m.mu.Lock()
	sawPrecondition := false
	for _, ir := range m.ifRange {
		if ir != "" {
			sawPrecondition = true
		}
	}
	m.mu.Unlock()
	if !sawPrecondition {
		t.Fatalf("expected If-Range headers on ranged requests")
	}
}

func TestStaleStateDiscarded(t *testing.T) {
	v1 := make([]byte, 200)
	m := newMockServer(v1)
	_, client := runMock(t, m)

	dest := filepath.Join(t.TempDir(), "out.bin")
	stale := &downloadState{
		URL:          pipeURL,
		ETag:         `"obsolete"`,
		TotalSize:    int64(len(v1)),
		AcceptRanges: true,
		Chunks:       planChunks(int64(len(v1)), 64),
	}
	if err := NewDownloader(Config{}).saveState(dest+".part.json", stale); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	res, err := testDownloader(client).Download(context.Background(), pipeURL, dest)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !res.Restarted {
		t.Fatalf("expected Restarted=true")
	}
	if got := readFile(t, dest); string(got) != string(v1) {
		t.Fatalf("content mismatch")
	}
}

func TestBadContentRangeIsFatalButRecoverable(t *testing.T) {
	data := make([]byte, 256)
	m := newMockServer(data)
	m.hook = func(n int, _ *http.Request) string {
		if n == 2 {
			return "badrange"
		}
		return ""
	}
	_, client := runMock(t, m)
	dest := filepath.Join(t.TempDir(), "out.bin")
	_, err := testDownloader(client).Download(context.Background(), pipeURL, dest)
	if err == nil || !strings.Contains(err.Error(), "Content-Range") {
		t.Fatalf("bad Content-Range must be fatal, got %v", err)
	}

	// A well-behaved server afterwards can still complete a fresh download.
	m2 := newMockServer(data)
	_, client2 := runMock(t, m2)
	dest2 := filepath.Join(t.TempDir(), "out2.bin")
	if _, err := testDownloader(client2).Download(context.Background(), pipeURL, dest2); err != nil {
		t.Fatalf("fresh download after badrange: %v", err)
	}
	if got := readFile(t, dest2); string(got) != string(data) {
		t.Fatalf("content mismatch")
	}
}

func TestPersistentBadContentRange(t *testing.T) {
	data := make([]byte, 256)
	m := newMockServer(data)
	m.hook = func(int, *http.Request) string { return "badrange" }
	_, client := runMock(t, m)
	dest := filepath.Join(t.TempDir(), "out.bin")
	_, err := testDownloader(client).Download(context.Background(), pipeURL, dest)
	if err == nil || !strings.Contains(err.Error(), "Content-Range") {
		t.Fatalf("persistent badrange must fail with ErrContentRange, got %v", err)
	}
}

func TestDegradedWhenRangeIgnored(t *testing.T) {
	data := make([]byte, 333)
	for i := range data {
		data[i] = byte(i + 1)
	}
	m := newMockServer(data)
	m.ignoreRange = true
	m.hook = func(n int, _ *http.Request) string {
		// Also drop one full-download connection to exercise retry.
		if n == 2 {
			return "disconnect"
		}
		return ""
	}
	_, client := runMock(t, m)

	dest := filepath.Join(t.TempDir(), "out.bin")
	res, err := testDownloader(client).Download(context.Background(), pipeURL, dest)
	if err != nil {
		t.Fatalf("degraded Download: %v", err)
	}
	if got := readFile(t, dest); string(got) != string(data) {
		t.Fatalf("content mismatch in degraded mode")
	}
	if res.SHA256 != sha256Hex(t, data) || res.Size != int64(len(data)) {
		t.Fatalf("degraded verify failed: %+v", res)
	}
}

func TestChecksumAndSizeMismatch(t *testing.T) {
	data := make([]byte, 128)
	m := newMockServer(data)
	_, client := runMock(t, m)

	dest := filepath.Join(t.TempDir(), "out.bin")
	dl := NewDownloader(Config{
		ChunkSize:      32,
		RetryDelay:     time.Millisecond,
		ExpectedSHA256: hex.EncodeToString(make([]byte, sha256.Size)),
		ExpectedSize:   999,
		HTTPClient:     client,
	})
	_, err := dl.Download(context.Background(), pipeURL, dest)
	if err == nil {
		t.Fatalf("expected verification error")
	}
	if !strings.Contains(err.Error(), "checksum") && !strings.Contains(err.Error(), "size") {
		t.Fatalf("expected checksum/size error, got %v", err)
	}
	if _, statErr := os.Stat(dest + ".part"); !os.IsNotExist(statErr) {
		t.Fatalf("partial file should be removed after failed verification")
	}
}

func TestServer500Retried(t *testing.T) {
	data := make([]byte, 192)
	m := newMockServer(data)
	m.hook = func(n int, _ *http.Request) string {
		if n <= 2 {
			return "status:500"
		}
		return ""
	}
	_, client := runMock(t, m)
	dest := filepath.Join(t.TempDir(), "out.bin")
	if _, err := testDownloader(client).Download(context.Background(), pipeURL, dest); err != nil {
		t.Fatalf("500 should be retried and recover: %v", err)
	}
	if got := readFile(t, dest); string(got) != string(data) {
		t.Fatalf("content mismatch after 500 retries")
	}
}
