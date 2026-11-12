package netsec

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"strings"
	"testing"
	"time"
)

// wireFrame is one response block observed on a raw HTTP/1.1 connection.
type wireFrame struct {
	status string
	header http.Header
	body   string
}

// readFrames parses every response block (103 hints plus the final response)
// from one raw connection so the test asserts the receiver's real sequence.
func readFrames(t *testing.T, conn net.Conn, expectedBodies ...int) []wireFrame {
	t.Helper()
	reader := bufio.NewReader(conn)
	var frames []wireFrame
	for i := 0; ; i++ {
		tp := textproto.NewReader(reader)
		statusLine, err := tp.ReadLine()
		if err != nil {
			var nerr net.Error
			if (errors.Is(err, io.EOF) || (errors.As(err, &nerr) && nerr.Timeout())) && i > 0 {
				return frames
			}
			t.Fatalf("read status line: %v", err)
		}
		mimeHeader, err := tp.ReadMIMEHeader()
		if err != nil {
			t.Fatalf("read headers: %v", err)
		}
		frame := wireFrame{status: statusLine, header: http.Header(mimeHeader)}
		if !strings.Contains(statusLine, " 103 ") {
			bodyLen := 0
			if i < len(expectedBodies) {
				bodyLen = expectedBodies[i]
			} else if cl := mimeHeader.Get("Content-Length"); cl != "" {
				for _, ch := range cl {
					bodyLen = bodyLen*10 + int(ch-'0')
				}
			}
			if bodyLen > 0 {
				buf := make([]byte, bodyLen)
				n, err := io.ReadFull(reader, buf)
				if err != nil && !(errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)) {
					t.Fatalf("read body: %v", err)
				}
				frame.body = string(buf[:n])
			}
			frames = append(frames, frame)
			return frames
		}
		frames = append(frames, frame)
	}
}

// pipeListener is an in-memory net.Listener so the integration test drives a
// real http.Server (full HTTP/1.1 framing) without binding a TCP socket.
type pipeListener struct {
	ch     chan net.Conn
	closed chan struct{}
}

func newPipeListener() *pipeListener {
	return &pipeListener{ch: make(chan net.Conn, 8), closed: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.ch:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return nil
}

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe:memory" }

func (l *pipeListener) Addr() net.Addr { return pipeAddr{} }

// netPipeClient tags the client end so tests can retrieve its server peer.
type netPipeClient struct {
	net.Conn
	server net.Conn
}

func (l *pipeListener) dial() net.Conn {
	client, server := net.Pipe()
	l.ch <- server
	return &netPipeClient{Conn: client, server: server}
}

type adapterServer struct {
	server   *http.Server
	listener *pipeListener
}

var cancelProbeDone = make(chan struct{}, 1)

func newAdapterServer(t *testing.T, p *Pipeline) *adapterServer {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) {
		stream, ok := StreamOf(w)
		if !ok {
			t.Errorf("StreamOf failed")
			return
		}
		h := w.Header()
		h.Set("Link", "</a-preload.css>; rel=preload")
		h.Set("Authorization", "Bearer must-never-leave")
		h.Set("Cookie", "sid=tenant-a")
		w.WriteHeader(http.StatusEarlyHints)

		stream2, _ := StreamOf(w)
		if stream2 != stream {
			t.Errorf("StreamOf unstable across calls")
		}

		w.Header().Set("Link", "</a-final.css>")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("X-Tenant", "a")
		_, _ = w.Write([]byte("body-a"))
	})
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", "</b-preload.js>; rel=preload")
		w.Header().Set("Proxy-Authorization", "Basic abc")
		w.WriteHeader(http.StatusEarlyHints)

		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("X-Tenant", "b")
		_, _ = w.Write([]byte("body-b"))
	})
	mux.HandleFunc("/auto", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Tenant", "auto")
		// No WriteHeader: the adapter must finalize with 200 on return.
	})
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		stream, _ := StreamOf(w)
		w.Header().Set("Link", "</slow1>; rel=preload")
		w.WriteHeader(http.StatusEarlyHints)
		// Wait for the test to simulate client abandonment, then prove every
		// post-cancel frame is rejected and reaches no receiver.
		<-r.Context().Done()
		deadline := time.Now().Add(time.Second)
		for p.Len() > 0 && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if err := stream.Hint(linkHeader("</after-cancel>")); !errors.Is(err, ErrStreamClosed) {
			t.Errorf("hint after cancel err = %v, want ErrStreamClosed", err)
		}
		if err := stream.Final(200, nil, nil); !errors.Is(err, ErrStreamClosed) {
			t.Errorf("final after cancel err = %v, want ErrStreamClosed", err)
		}
		cancelProbeDone <- struct{}{}
	})
	listener := newPipeListener()
	srv := &http.Server{Handler: p.Handler(mux)}
	go func() { _ = srv.Serve(listener) }()
	return &adapterServer{server: srv, listener: listener}
}

func (s *adapterServer) close() {
	_ = s.server.Close()
	_ = s.listener.Close()
}

func (s *adapterServer) dialRaw(t *testing.T) net.Conn {
	t.Helper()
	return s.listener.dial()
}

func sendRequest(conn net.Conn, path string) {
	_, _ = io.WriteString(conn, "GET "+path+" HTTP/1.1\r\nHost: tenant.example\r\nConnection: close\r\n\r\n")
}

func assertNoAuthHeaders(t *testing.T, frame wireFrame, where string) {
	t.Helper()
	for _, key := range []string{"Authorization", "Proxy-Authorization", "Cookie", "Set-Cookie"} {
		if frame.header.Get(key) != "" {
			t.Fatalf("%s carries forbidden %s: %v", where, key, frame.header)
		}
	}
}

func TestAdapterWireSequenceAndAuthScrub(t *testing.T) {
	p := NewPipeline()
	srv := newAdapterServer(t, p)
	defer srv.close()

	conn := srv.dialRaw(t)
	defer conn.Close()
	sendRequest(conn, "/a")
	frames := readFrames(t, conn, 0, 6)

	if len(frames) != 2 {
		t.Fatalf("frame count = %d, want 2 (103 + 200): %+v", len(frames), frames)
	}
	hint, final := frames[0], frames[1]
	if !strings.Contains(hint.status, " 103 ") {
		t.Fatalf("first frame status = %q, want 103", hint.status)
	}
	if got := hint.header.Get("Link"); got != "</a-preload.css>; rel=preload" {
		t.Fatalf("hint Link = %q", got)
	}
	assertNoAuthHeaders(t, hint, "103 hint")

	if !strings.Contains(final.status, " 200 ") {
		t.Fatalf("final status = %q, want 200", final.status)
	}
	if got := final.header.Get("X-Tenant"); got != "a" {
		t.Fatalf("final X-Tenant = %q", got)
	}
	if got := final.header.Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Fatalf("final Content-Type = %q", got)
	}
	if final.body != "body-a" {
		t.Fatalf("final body = %q, want body-a", final.body)
	}
	assertNoAuthHeaders(t, final, "final response")
	// Hint-only Link must be replaced, not accumulated, on the final frame.
	if got := final.header.Values("Link"); len(got) != 1 || got[0] != "</a-final.css>" {
		t.Fatalf("final Link = %v, want only the final-stage value", got)
	}
}

func TestAdapterInterleavedConnectionsIsolated(t *testing.T) {
	p := NewPipeline()
	srv := newAdapterServer(t, p)
	defer srv.close()

	connA := srv.dialRaw(t)
	connB := srv.dialRaw(t)
	defer connA.Close()
	defer connB.Close()

	// Interleave the two requests over separate connections, then drain each.
	sendRequest(connA, "/a")
	sendRequest(connB, "/b")
	framesA := readFrames(t, connA, 0, 6)
	framesB := readFrames(t, connB, 0, 6)

	if len(framesA) != 2 || len(framesB) != 2 {
		t.Fatalf("frame counts A=%d B=%d, want 2 each", len(framesA), len(framesB))
	}
	if got := framesA[0].header.Get("Link"); got != "</a-preload.css>; rel=preload" {
		t.Fatalf("A hint Link = %q", got)
	}
	if got := framesB[0].header.Get("Link"); got != "</b-preload.js>; rel=preload" {
		t.Fatalf("B hint Link = %q", got)
	}
	if framesA[1].header.Get("X-Tenant") != "a" || framesB[1].header.Get("X-Tenant") != "b" {
		t.Fatalf("tenant swap on wire: A=%v B=%v", framesA[1].header, framesB[1].header)
	}
	if framesA[1].body != "body-a" || framesB[1].body != "body-b" {
		t.Fatalf("body swap: A=%q B=%q", framesA[1].body, framesB[1].body)
	}
	for _, f := range append(framesA, framesB...) {
		assertNoAuthHeaders(t, f, "wire frame")
	}
	if p.Len() != 0 {
		t.Fatalf("pipeline retains %d streams after responses complete", p.Len())
	}
}

func TestAdapterAutoFinalize(t *testing.T) {
	p := NewPipeline()
	srv := newAdapterServer(t, p)
	defer srv.close()

	conn := srv.dialRaw(t)
	defer conn.Close()
	sendRequest(conn, "/auto")
	frames := readFrames(t, conn)
	if len(frames) != 1 || !strings.Contains(frames[0].status, " 200 ") {
		t.Fatalf("want one implicit 200 frame, got %+v", frames)
	}
	if frames[0].header.Get("X-Tenant") != "auto" {
		t.Fatalf("auto final header lost: %v", frames[0].header)
	}
}

func TestAdapterCancelOnClientDisconnect(t *testing.T) {
	select {
	case <-cancelProbeDone:
	default:
	}
	p := NewPipeline()
	srv := newAdapterServer(t, p)
	defer srv.close()

	conn := srv.dialRaw(t)
	sendRequest(conn, "/slow")

	// Consume the single 103 frame, then force the server-side connection
	// closed, which cancels the request context exactly like a disconnect.
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	frames := readFrames(t, conn)
	if len(frames) != 1 || !strings.Contains(frames[0].status, " 103 ") {
		t.Fatalf("receiver must see exactly one 103 frame, got %+v", frames)
	}
	if got := frames[0].header.Get("Link"); got != "</slow1>; rel=preload" {
		t.Fatalf("hint Link = %q", got)
	}
	pc := conn.(*netPipeClient)
	_ = pc.server.Close()

	select {
	case <-cancelProbeDone:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not observe request cancellation")
	}
	if p.Len() != 0 {
		t.Fatalf("canceled stream not unbound, Len = %d", p.Len())
	}
	_ = conn.Close()
}
