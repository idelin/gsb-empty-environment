package netsec

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testResponse = "HTTP/1.1 200 Connection Established\r\n\r\n"

func TestEarlyDataWaitsForSuccessfulResponse(t *testing.T) {
	client, clientPeer := net.Pipe()
	target, targetPeer := net.Pipe()
	defer clientPeer.Close()
	defer targetPeer.Close()

	authStarted := make(chan struct{})
	authRelease := make(chan struct{})
	var authOnce sync.Once
	authorizer := AuthorizerFunc(func(context.Context, Request) error {
		authOnce.Do(func() { close(authStarted) })
		<-authRelease
		return nil
	})

	dialed := make(chan string, 1)
	dialer := DialerFunc(func(_ context.Context, _ string, address string) (net.Conn, error) {
		dialed <- address
		return target, nil
	})

	responseStarted := make(chan struct{})
	responseWritten := make(chan struct{})
	responseRelease := make(chan struct{})
	responder := ResponderFuncs{Success: func(_ context.Context, w io.Writer) error {
		close(responseStarted)
		<-responseRelease
		if err := WriteResponse(w, 200, "Connection Established"); err != nil {
			return err
		}
		close(responseWritten)
		return nil
	}}

	ingress := NewIngress(
		WithAuthorizer(authorizer),
		WithDialer(dialer),
		WithResponder(responder),
		WithMaxEarlyData(32),
	)
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- ingress.Serve(context.Background(), client, Request{
			Target: NewTarget("authorized.example:443"),
			Policy: Policy{ID: "default"},
		})
	}()

	waitClosed(t, authStarted, "authorization did not start")

	early := []byte("GET /early HTTP/1.1\r\n\r\n")
	writeDone := make(chan error, 1)
	go func() {
		_, err := clientPeer.Write(early)
		writeDone <- err
	}()
	waitError(t, writeDone, "client could not write early data")

	assertNoTargetBytes(t, targetPeer, "target received early data before authorization")
	select {
	case address := <-dialed:
		t.Fatalf("dialed %q before authorization completed", address)
	default:
	}
	select {
	case <-responseStarted:
		t.Fatal("success response started before authorization completed")
	default:
	}

	close(authRelease)
	select {
	case address := <-dialed:
		if address != "authorized.example:443" {
			t.Fatalf("dialed %q, want authorized.example:443", address)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("authorized target was not dialed")
	}
	waitClosed(t, responseStarted, "success response did not start")
	assertNoTargetBytes(t, targetPeer, "target received early data before the success response completed")

	response := make(chan string, 1)
	go func() {
		buf := make([]byte, len(testResponse))
		_, err := io.ReadFull(clientPeer, buf)
		if err != nil {
			response <- "read error: " + err.Error()
			return
		}
		response <- string(buf)
	}()

	close(responseRelease)
	select {
	case got := <-response:
		if got != testResponse {
			t.Fatalf("client response = %q, want %q", got, testResponse)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client did not receive the success response")
	}
	waitClosed(t, responseWritten, "response writer did not finish")

	gotEarly := readN(t, targetPeer, len(early))
	if !bytes.Equal(gotEarly, early) {
		t.Fatalf("target early data = %q, want %q", gotEarly, early)
	}

	_ = targetPeer.Close()
	_ = clientPeer.Close()
	waitServe(t, serveDone)
}

func TestUnauthorizedTargetReceivesZeroBytes(t *testing.T) {
	client, clientPeer := net.Pipe()
	targetPeer, targetServer := net.Pipe()
	defer clientPeer.Close()
	defer targetPeer.Close()

	target := &writeCountingConn{Conn: targetServer}
	var dials atomic.Int32
	authStarted := make(chan struct{})
	authRelease := make(chan struct{})
	var once sync.Once
	ingress := NewIngress(
		WithAuthorizeFunc(func(context.Context, Request) error {
			once.Do(func() { close(authStarted) })
			<-authRelease
			return errors.New("policy denies destination")
		}),
		WithDialer(DialerFunc(func(context.Context, string, string) (net.Conn, error) {
			dials.Add(1)
			return target, nil
		})),
		WithMaxEarlyData(128),
	)

	result := make(chan error, 1)
	go func() {
		result <- ingress.Serve(context.Background(), client, Request{
			Target: NewTarget("denied.example:443"),
			Policy: Policy{ID: "deny"},
		})
	}()

	waitClosed(t, authStarted, "authorization did not start")
	writeDone := make(chan error, 1)
	go func() {
		_, err := clientPeer.Write([]byte("must-not-reach-target"))
		writeDone <- err
	}()
	waitError(t, writeDone, "client could not write early data")

	close(authRelease)
	response := make([]byte, 64)
	_ = clientPeer.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := clientPeer.Read(response)
	if err != nil {
		t.Fatalf("reading denial response: %v", err)
	}
	if !bytes.Contains(response[:n], []byte("403")) {
		t.Fatalf("denial response = %q, want 403", response[:n])
	}

	select {
	case err := <-result:
		if !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("Serve error = %v, want ErrUnauthorized", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not reject the unauthorized target")
	}

	if got := dials.Load(); got != 0 {
		t.Fatalf("unauthorized target was dialed %d times", got)
	}
	if got := target.bytes.Load(); got != 0 {
		t.Fatalf("unauthorized target received %d bytes", got)
	}
}

func TestCancellationStopsAdmissionWithZeroTargetBytes(t *testing.T) {
	client, clientPeer := net.Pipe()
	targetPeer, targetServer := net.Pipe()
	defer clientPeer.Close()
	defer targetPeer.Close()

	target := &writeCountingConn{Conn: targetServer}
	var dials atomic.Int32
	authStarted := make(chan struct{})
	authRelease := make(chan struct{})
	var once sync.Once
	ingress := NewIngress(
		WithAuthorizeFunc(func(context.Context, Request) error {
			once.Do(func() { close(authStarted) })
			<-authRelease
			return nil
		}),
		WithDialer(DialerFunc(func(context.Context, string, string) (net.Conn, error) {
			dials.Add(1)
			return target, nil
		})),
		WithMaxEarlyData(128),
	)

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- ingress.Serve(ctx, client, Request{
			Target: NewTarget("cancelled.example:443"),
			Policy: Policy{ID: "default"},
		})
	}()

	waitClosed(t, authStarted, "authorization did not start")
	writeDone := make(chan error, 1)
	go func() {
		_, err := clientPeer.Write([]byte("early-cancelled-data"))
		writeDone <- err
	}()
	waitError(t, writeDone, "client could not write early data")

	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Serve error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve ignored cancellation while authorization was paused")
	}
	close(authRelease)

	if got := dials.Load(); got != 0 {
		t.Fatalf("cancelled tunnel dialed the target %d times", got)
	}
	if got := target.bytes.Load(); got != 0 {
		t.Fatalf("cancelled tunnel sent %d bytes to the target", got)
	}
}

func TestResponseFailureDiscardsEarlyData(t *testing.T) {
	client, clientPeer := net.Pipe()
	targetPeer, targetServer := net.Pipe()
	defer clientPeer.Close()
	defer targetPeer.Close()

	counter := &writeCountingConn{Conn: targetServer}
	responseRelease := make(chan struct{})
	responseStarted := make(chan struct{})
	ingress := NewIngress(
		WithAuthorizeFunc(func(context.Context, Request) error { return nil }),
		WithDialer(DialerFunc(func(context.Context, string, string) (net.Conn, error) {
			return counter, nil
		})),
		WithResponseWriter(func(context.Context, io.Writer) error {
			close(responseStarted)
			<-responseRelease
			return errors.New("write failed")
		}),
		WithMaxEarlyData(128),
	)

	result := make(chan error, 1)
	go func() {
		result <- ingress.Serve(context.Background(), client, Request{
			Target: NewTarget("target.example:443"),
			Policy: Policy{ID: "default"},
		})
	}()

	waitClosed(t, responseStarted, "success response did not start")

	early := []byte("must-not-be-forwarded")
	writeDone := make(chan error, 1)
	go func() {
		_, err := clientPeer.Write(early)
		writeDone <- err
	}()
	waitError(t, writeDone, "client could not write early data")

	assertNoTargetBytes(t, targetPeer, "target received data while the response writer was paused")
	close(responseRelease)

	select {
	case err := <-result:
		if !errors.Is(err, ErrResponseFailed) {
			t.Fatalf("Serve error = %v, want ErrResponseFailed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not report the response write failure")
	}

	if got := counter.writes.Load(); got != 0 {
		t.Fatalf("target Write calls = %d, want 0", got)
	}
	if got := counter.bytes.Load(); got != 0 {
		t.Fatalf("target bytes = %d, want 0", got)
	}
}

func TestEarlyDataPrefetchIsBounded(t *testing.T) {
	client, clientPeer := net.Pipe()
	target, targetPeer := net.Pipe()
	defer clientPeer.Close()
	defer targetPeer.Close()

	counted := &countingConn{Conn: client, reads: make(chan int64, 8)}
	authStarted := make(chan struct{})
	authRelease := make(chan struct{})
	var once sync.Once
	dialed := make(chan struct{}, 1)
	ingress := NewIngress(
		WithAuthorizeFunc(func(context.Context, Request) error {
			once.Do(func() { close(authStarted) })
			<-authRelease
			return nil
		}),
		WithDialer(DialerFunc(func(context.Context, string, string) (net.Conn, error) {
			select {
			case dialed <- struct{}{}:
			default:
			}
			return target, nil
		})),
		WithResponseWriter(func(context.Context, io.Writer) error { return nil }),
		WithMaxEarlyData(8),
	)

	result := make(chan error, 1)
	go func() {
		result <- ingress.Serve(context.Background(), counted, Request{
			Target: NewTarget("target.example:443"),
			Policy: Policy{ID: "default"},
		})
	}()
	waitClosed(t, authStarted, "authorization did not start")

	payload := bytes.Repeat([]byte("x"), 1000)
	writeDone := make(chan error, 1)
	go func() {
		_, err := clientPeer.Write(payload)
		writeDone <- err
	}()

	waitAtLeast(t, counted.reads, 8, "ingress did not read the early-data cap")
	time.Sleep(100 * time.Millisecond)
	if got := counted.total.Load(); got != 8 {
		t.Fatalf("client bytes read before authorization = %d, want 8", got)
	}
	select {
	case <-dialed:
		t.Fatal("target was dialed before authorization")
	default:
	}

	close(authRelease)
	select {
	case <-dialed:
	case <-time.After(2 * time.Second):
		t.Fatal("target was not dialed after authorization")
	}

	got := readN(t, targetPeer, len(payload))
	if !bytes.Equal(got, payload) {
		t.Fatalf("target payload length = %d, want %d", len(got), len(payload))
	}
	waitError(t, writeDone, "client could not finish early data")

	_ = clientPeer.Close()
	_ = targetPeer.Close()
	waitServe(t, result)
}

func TestConfigChangeRequiresReauthorization(t *testing.T) {
	client, clientPeer := net.Pipe()
	target, targetPeer := net.Pipe()
	defer clientPeer.Close()
	defer targetPeer.Close()

	store := NewConfigStore(Config{
		Target: NewTarget("first.example:443"),
		Policy: Policy{ID: "policy-a"},
	})

	authCalls := make(chan Request, 4)
	firstRelease := make(chan struct{})
	var authCount atomic.Int32
	authorizer := AuthorizerFunc(func(_ context.Context, req Request) error {
		authCalls <- req
		if authCount.Add(1) == 1 {
			<-firstRelease
		}
		return nil
	})

	dialed := make(chan string, 2)
	dialer := DialerFunc(func(_ context.Context, _ string, address string) (net.Conn, error) {
		dialed <- address
		return target, nil
	})
	responseSent := make(chan struct{})
	ingress := NewIngress(
		WithAuthorizer(authorizer),
		WithDialer(dialer),
		WithResponseWriter(func(context.Context, io.Writer) error {
			close(responseSent)
			return nil
		}),
		WithConfigSource(store),
	)

	result := make(chan error, 1)
	go func() {
		result <- ingress.Serve(context.Background(), client, Request{})
	}()

	first := <-authCalls
	if first.Target.Address != "first.example:443" || first.Policy.ID != "policy-a" {
		t.Fatalf("first authorization = %+v", first)
	}
	if first.Revision != 1 {
		t.Fatalf("first revision = %d, want 1", first.Revision)
	}

	store.Update(Config{
		Target: NewTarget("second.example:443"),
		Policy: Policy{ID: "policy-b"},
	})
	close(firstRelease)

	second := <-authCalls
	if second.Target.Address != "second.example:443" || second.Policy.ID != "policy-b" {
		t.Fatalf("reauthorization request = %+v, want second target and policy-b", second)
	}
	if second.Revision != 2 {
		t.Fatalf("reauthorization revision = %d, want 2", second.Revision)
	}

	select {
	case address := <-dialed:
		if address != "second.example:443" {
			t.Fatalf("dialed %q, want second.example:443", address)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("target was not dialed after reauthorization")
	}

	waitClosed(t, responseSent, "success response was not written")
	_ = clientPeer.Close()
	_ = targetPeer.Close()
	waitServe(t, result)
}

func TestConfigChangeDuringDialDoesNotLeakToOldTarget(t *testing.T) {
	client, clientPeer := net.Pipe()
	oldTarget, oldPeer := net.Pipe()
	newTarget, newPeer := net.Pipe()
	defer clientPeer.Close()
	defer oldPeer.Close()
	defer newPeer.Close()

	oldCounter := &writeCountingConn{Conn: oldTarget}
	newCounter := &writeCountingConn{Conn: newTarget}
	store := NewConfigStore(Config{
		Target: NewTarget("old.example:443"),
		Policy: Policy{ID: "p1"},
	})

	dialStarted := make(chan string, 2)
	dialRelease := make(chan struct{})
	var dialCount atomic.Int32
	dialer := DialerFunc(func(_ context.Context, _ string, address string) (net.Conn, error) {
		dialStarted <- address
		if dialCount.Add(1) == 1 {
			<-dialRelease
			return oldCounter, nil
		}
		return newCounter, nil
	})

	authCalls := make(chan Request, 4)
	ingress := NewIngress(
		WithAuthorizer(AuthorizerFunc(func(_ context.Context, req Request) error {
			authCalls <- req
			return nil
		})),
		WithDialer(dialer),
		WithResponseWriter(func(context.Context, io.Writer) error { return nil }),
		WithConfigSource(store),
		WithMaxEarlyData(64),
	)

	result := make(chan error, 1)
	go func() {
		result <- ingress.Serve(context.Background(), client, Request{})
	}()

	firstAuth := <-authCalls
	if firstAuth.Target.Address != "old.example:443" {
		t.Fatalf("first auth target = %q", firstAuth.Target.Address)
	}
	if got := <-dialStarted; got != "old.example:443" {
		t.Fatalf("first dial = %q", got)
	}

	store.Update(Config{
		Target: NewTarget("new.example:443"),
		Policy: Policy{ID: "p2"},
	})
	close(dialRelease)

	reauthorized := <-authCalls
	if reauthorized.Target.Address != "new.example:443" || reauthorized.Policy.ID != "p2" {
		t.Fatalf("reauthorization = %+v", reauthorized)
	}
	if got := <-dialStarted; got != "new.example:443" {
		t.Fatalf("second dial = %q", got)
	}

	_ = clientPeer.Close()
	_ = oldPeer.Close()
	_ = newPeer.Close()
	waitServe(t, result)

	if got := oldCounter.bytes.Load(); got != 0 {
		t.Fatalf("old target received %d bytes after config change", got)
	}
}

func TestConfigChangeAfterResponseAbortsWithoutForwarding(t *testing.T) {
	client, clientPeer := net.Pipe()
	targetPeer, targetServer := net.Pipe()
	defer clientPeer.Close()
	defer targetPeer.Close()

	store := NewConfigStore(Config{
		Target: NewTarget("target.example:443"),
		Policy: Policy{ID: "p1"},
	})
	target := &writeCountingConn{Conn: targetServer}
	responseStarted := make(chan struct{})
	responseRelease := make(chan struct{})
	ingress := NewIngress(
		WithAuthorizeFunc(func(context.Context, Request) error { return nil }),
		WithDialer(DialerFunc(func(context.Context, string, string) (net.Conn, error) {
			return target, nil
		})),
		WithResponseWriter(func(context.Context, io.Writer) error {
			close(responseStarted)
			<-responseRelease
			store.Update(Config{
				Target: NewTarget("changed.example:443"),
				Policy: Policy{ID: "p2"},
			})
			return nil
		}),
		WithConfigSource(store),
	)

	result := make(chan error, 1)
	go func() {
		result <- ingress.Serve(context.Background(), client, Request{})
	}()
	waitClosed(t, responseStarted, "success response did not start")

	early := []byte("must-stay-buffered")
	writeDone := make(chan error, 1)
	go func() {
		_, err := clientPeer.Write(early)
		writeDone <- err
	}()
	waitError(t, writeDone, "client could not write early data")
	close(responseRelease)

	select {
	case err := <-result:
		if !errors.Is(err, ErrConfigChanged) {
			t.Fatalf("Serve error = %v, want ErrConfigChanged", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve forwarded data after a post-response config change")
	}

	if got := target.bytes.Load(); got != 0 {
		t.Fatalf("target received %d bytes after post-response config change", got)
	}
}

type writeCountingConn struct {
	net.Conn
	writes atomic.Int64
	bytes  atomic.Int64
}

func (c *writeCountingConn) Write(p []byte) (int, error) {
	c.writes.Add(1)
	c.bytes.Add(int64(len(p)))
	return c.Conn.Write(p)
}

type countingConn struct {
	net.Conn
	total atomic.Int64
	reads chan int64
}

func (c *countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		total := c.total.Add(int64(n))
		select {
		case c.reads <- total:
		default:
		}
	}
	return n, err
}

func waitClosed(t *testing.T, ch <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal(message)
	}
}

func waitError(t *testing.T, ch <-chan error, message string) {
	t.Helper()
	select {
	case err := <-ch:
		if err != nil {
			t.Fatalf("%s: %v", message, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal(message)
	}
}

func waitServe(t *testing.T, ch <-chan error) {
	t.Helper()
	select {
	case err := <-ch:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Serve error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return")
	}
}

func assertNoTargetBytes(t *testing.T, conn net.Conn, message string) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(120 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1)
	n, err := conn.Read(buf)
	_ = conn.SetReadDeadline(time.Time{})
	if n != 0 {
		t.Fatalf("%s: read %d bytes", message, n)
	}
	if err == nil {
		t.Fatalf("%s: read succeeded unexpectedly", message)
	}
	if !isTimeout(err) {
		t.Fatalf("%s: read error = %v, want timeout", message, err)
	}
}

func readN(t *testing.T, conn net.Conn, n int) []byte {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, n)
	_, err := io.ReadFull(conn, buf)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		t.Fatalf("reading %d bytes: %v", n, err)
	}
	return buf
}

func waitAtLeast(t *testing.T, ch <-chan int64, want int64, message string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case got := <-ch:
			if got >= want {
				return
			}
		case <-deadline:
			t.Fatal(message)
		}
	}
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
