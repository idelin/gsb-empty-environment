package netsec

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type pipeAddr string

func (a pipeAddr) Network() string { return "pipe" }
func (a pipeAddr) String() string  { return string(a) }

type singleConnListener struct {
	conn      net.Conn
	accepted  atomic.Bool
	closed    chan struct{}
	closeOnce sync.Once
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	if l.accepted.CompareAndSwap(false, true) {
		return l.conn, nil
	}
	<-l.closed
	return nil, net.ErrClosed
}

func (l *singleConnListener) Close() error {
	l.closeOnce.Do(func() {
		close(l.closed)
	})
	return nil
}

func (l *singleConnListener) Addr() net.Addr {
	return pipeAddr("single-connection")
}

func newWireClient(t *testing.T, handler http.Handler) (net.Conn, *bufio.Reader) {
	t.Helper()
	client, serverSide := net.Pipe()
	srv := &http.Server{Handler: handler}
	listener := &singleConnListener{
		conn:   serverSide,
		closed: make(chan struct{}),
	}
	go func() {
		_ = srv.Serve(listener)
	}()
	t.Cleanup(func() {
		_ = client.Close()
		_ = srv.Close()
	})
	return client, bufio.NewReader(client)
}

func readStatus(t *testing.T, reader *bufio.Reader) (int, http.Header) {
	t.Helper()
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read status line: %v", err)
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		t.Fatalf("malformed status line %q", line)
	}
	code, err := strconv.Atoi(fields[1])
	if err != nil {
		t.Fatalf("parse status line %q: %v", line, err)
	}
	header, err := textproto.NewReader(reader).ReadMIMEHeader()
	if err != nil {
		t.Fatalf("read response headers: %v", err)
	}
	return code, http.Header(header)
}

func waitForSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func assertNoResponseYet(t *testing.T, conn net.Conn, reader *bufio.Reader) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Peek(1); err == nil {
		t.Fatal("server sent a response before authentication completed")
	} else if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
		t.Fatalf("unexpected read error while waiting for authentication: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
}

func TestWireAuthFailureSendsNoContinue(t *testing.T) {
	authStarted := make(chan struct{}, 1)
	release := make(chan struct{})

	handler := NewHandler(Config{
		MaxBodyBytes: 1024,
		Authenticate: func(*http.Request) (bool, int) {
			authStarted <- struct{}{}
			<-release
			return false, http.StatusUnauthorized
		},
		Next: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("next handler called after authentication failure")
		}),
	})

	conn, reader := newWireClient(t, handler)
	if _, err := fmt.Fprintf(conn, "POST / HTTP/1.1\r\nHost: example.test\r\nContent-Length: 8\r\nExpect: 100-continue\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	waitForSignal(t, authStarted, "authentication start")
	assertNoResponseYet(t, conn, reader)

	close(release)
	code, header := readStatus(t, reader)
	if code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", code, http.StatusUnauthorized)
	}
	if header.Get("Connection") != "close" {
		t.Fatalf("Connection = %q, want close", header.Get("Connection"))
	}

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadByte(); err == nil {
		t.Fatal("connection remained open after authentication failure")
	} else if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		t.Fatal("connection was not closed after authentication failure")
	}
}

func TestWireContinueOnlyAfterAuthentication(t *testing.T) {
	const body = "data"

	authStarted := make(chan struct{}, 1)
	release := make(chan struct{})
	nextBody := make(chan string, 1)
	nextExpect := make(chan []string, 1)

	handler := NewHandler(Config{
		MaxBodyBytes: 1024,
		Authenticate: func(*http.Request) (bool, int) {
			authStarted <- struct{}{}
			<-release
			return true, 0
		},
		Next: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nextExpect <- r.Header.Values("Expect")
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read body: %v", err)
			}
			nextBody <- string(data)
			w.WriteHeader(http.StatusOK)
		}),
	})

	conn, reader := newWireClient(t, handler)
	if _, err := fmt.Fprintf(conn, "POST / HTTP/1.1\r\nHost: example.test\r\nContent-Length: %d\r\nExpect: 100-continue\r\n\r\n", len(body)); err != nil {
		t.Fatal(err)
	}
	waitForSignal(t, authStarted, "authentication start")
	assertNoResponseYet(t, conn, reader)

	close(release)
	code, _ := readStatus(t, reader)
	if code != http.StatusContinue {
		t.Fatalf("first status = %d, want 100", code)
	}
	if _, err := io.WriteString(conn, body); err != nil {
		t.Fatal(err)
	}

	code, _ = readStatus(t, reader)
	if code != http.StatusOK {
		t.Fatalf("final status = %d, want %d", code, http.StatusOK)
	}
	if got := <-nextBody; got != body {
		t.Fatalf("next handler read %q, want %q", got, body)
	}
	if got := <-nextExpect; len(got) != 0 {
		t.Fatalf("Expect was forwarded: %q", got)
	}
}

func TestWireOversizedContentLengthRejectedWithoutContinue(t *testing.T) {
	var authCalls atomic.Int32
	handler := NewHandler(Config{
		MaxBodyBytes: 8,
		Authenticate: func(*http.Request) (bool, int) {
			authCalls.Add(1)
			return true, 0
		},
		Next: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("next handler called for oversized Content-Length")
		}),
	})

	conn, reader := newWireClient(t, handler)
	if _, err := fmt.Fprintf(conn, "POST / HTTP/1.1\r\nHost: example.test\r\nContent-Length: 9\r\nExpect: 100-continue\r\n\r\n"); err != nil {
		t.Fatal(err)
	}

	code, header := readStatus(t, reader)
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("first status = %d, want %d (a 100 response must not precede it)", code, http.StatusRequestEntityTooLarge)
	}
	if header.Get("Connection") != "close" {
		t.Fatalf("Connection = %q, want close", header.Get("Connection"))
	}
	if authCalls.Load() != 0 {
		t.Fatal("authentication ran for oversized Content-Length")
	}
}

func TestWireMultipleExpectHeadersRejected(t *testing.T) {
	var authCalls atomic.Int32
	handler := NewHandler(Config{
		MaxBodyBytes: 1024,
		Authenticate: func(*http.Request) (bool, int) {
			authCalls.Add(1)
			return true, 0
		},
		Next: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("next handler called for duplicate Expect headers")
		}),
	})

	conn, reader := newWireClient(t, handler)
	if _, err := fmt.Fprintf(conn, "POST / HTTP/1.1\r\nHost: example.test\r\nContent-Length: 4\r\nExpect: 100-continue\r\nExpect: 100-continue\r\n\r\n"); err != nil {
		t.Fatal(err)
	}

	code, _ := readStatus(t, reader)
	if code != http.StatusBadRequest {
		t.Fatalf("first status = %d, want %d", code, http.StatusBadRequest)
	}
	if authCalls.Load() != 0 {
		t.Fatal("authentication ran for duplicate Expect headers")
	}
}

func TestWireHTTP10ExpectIgnored(t *testing.T) {
	const body = "data"

	nextBody := make(chan string, 1)
	nextExpect := make(chan []string, 1)
	handler := NewHandler(Config{
		MaxBodyBytes: 1024,
		Authenticate: func(*http.Request) (bool, int) {
			return true, 0
		},
		Next: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nextExpect <- r.Header.Values("Expect")
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read body: %v", err)
			}
			nextBody <- string(data)
			w.WriteHeader(http.StatusOK)
		}),
	})

	conn, reader := newWireClient(t, handler)
	if _, err := fmt.Fprintf(conn, "POST / HTTP/1.0\r\nHost: example.test\r\nContent-Length: %d\r\nExpect: 100-continue\r\n\r\n%s", len(body), body); err != nil {
		t.Fatal(err)
	}

	code, _ := readStatus(t, reader)
	if code != http.StatusOK {
		t.Fatalf("first status = %d, want %d (HTTP/1.0 must not send 100)", code, http.StatusOK)
	}
	if got := <-nextBody; got != body {
		t.Fatalf("next handler read %q, want %q", got, body)
	}
	if got := <-nextExpect; len(got) != 0 {
		t.Fatalf("Expect was forwarded for HTTP/1.0: %q", got)
	}
}

func TestWireEarlyBodyCountsAgainstBudget(t *testing.T) {
	nextResult := make(chan struct {
		body []byte
		err  error
	}, 1)

	handler := NewHandler(Config{
		MaxBodyBytes: 8,
		Authenticate: func(*http.Request) (bool, int) {
			return true, 0
		},
		Next: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			data, err := io.ReadAll(r.Body)
			nextResult <- struct {
				body []byte
				err  error
			}{body: data, err: err}
		}),
	})

	conn, reader := newWireClient(t, handler)
	const request = "POST / HTTP/1.1\r\n" +
		"Host: example.test\r\n" +
		"Transfer-Encoding: chunked\r\n" +
		"Expect: 100-continue\r\n" +
		"\r\n" +
		"5\r\n01234\r\n" +
		"5\r\n56789\r\n" +
		"0\r\n\r\n"
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatal(err)
	}

	code := 0
	for code < 200 {
		code, _ = readStatus(t, reader)
	}
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("final status = %d, want %d", code, http.StatusRequestEntityTooLarge)
	}

	got := <-nextResult
	if len(got.body) != 8 {
		t.Fatalf("next handler received %d body bytes, want the 8-byte budget", len(got.body))
	}
	if !errorsIsBodyTooLarge(got.err) {
		t.Fatalf("body read error = %v, want ErrBodyTooLarge", got.err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadByte(); err == nil {
		t.Fatal("connection remained open after a streamed body overflow")
	} else if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		t.Fatal("connection was not closed after a streamed body overflow")
	}
}

func errorsIsBodyTooLarge(err error) bool {
	return err != nil && errors.Is(err, ErrBodyTooLarge)
}
