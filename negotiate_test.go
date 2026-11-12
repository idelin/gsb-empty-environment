package netsec

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// scriptConn is a fake transport that replays a fixed byte script to the
// reader and records everything written to it.
type scriptConn struct {
	in  *bytes.Buffer
	out bytes.Buffer
}

func newScriptConn(script []byte) *scriptConn {
	return &scriptConn{in: bytes.NewBuffer(script)}
}

func (c *scriptConn) Read(p []byte) (int, error) {
	if c.in.Len() == 0 {
		return 0, io.EOF
	}
	return c.in.Read(p)
}

func (c *scriptConn) Write(p []byte) (int, error) { return c.out.Write(p) }

// fakeServer is a fake transport that answers each client message by
// invoking respond, so replies can be bound to the live handshake instance.
type fakeServer struct {
	raw       []byte
	toClient  bytes.Buffer
	respond   func(wireMessage) []wireMessage
	responded bool
}

func (f *fakeServer) Write(p []byte) (int, error) {
	f.raw = append(f.raw, p...)
	if len(f.raw) < 4 {
		return len(p), nil
	}
	n := int(f.raw[0])<<24 | int(f.raw[1])<<16 | int(f.raw[2])<<8 | int(f.raw[3])
	if len(f.raw) < 4+n {
		return len(p), nil
	}
	var msg wireMessage
	if err := json.Unmarshal(f.raw[4:4+n], &msg); err != nil {
		return len(p), err
	}
	for _, reply := range f.respond(msg) {
		payload, err := json.Marshal(reply)
		if err != nil {
			return len(p), err
		}
		if err := writeFrame(&f.toClient, payload); err != nil {
			return len(p), err
		}
	}
	f.responded = true
	return len(p), nil
}

func (f *fakeServer) Read(p []byte) (int, error) {
	if f.toClient.Len() == 0 {
		return 0, io.EOF
	}
	return f.toClient.Read(p)
}

// recordingConn wraps a transport and records every byte read.
type recordingConn struct {
	net.Conn
	recorded bytes.Buffer
}

func (c *recordingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.recorded.Write(p[:n])
	return n, err
}

func frameBytes(t *testing.T, msg wireMessage) []byte {
	t.Helper()
	payload, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := writeFrame(&buf, payload); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func openStore(t *testing.T, dir string) *CapabilityStore {
	t.Helper()
	s, err := OpenCapabilityStore(filepath.Join(dir, "capability.json"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func newClient(t *testing.T, store *CapabilityStore, allowFallback bool) *Negotiator {
	t.Helper()
	n, err := NewNegotiator(Config{
		Protocols:           []Protocol{ProtocolV2, ProtocolLegacy},
		Legacy:              ProtocolLegacy,
		AllowLegacyFallback: allowFallback,
		Store:               store,
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func asHandshakeError(t *testing.T, err error) *HandshakeError {
	t.Helper()
	var he *HandshakeError
	if !errors.As(err, &he) {
		t.Fatalf("expected HandshakeError, got %v", err)
	}
	return he
}

func TestHandshakeConfirmsAndPersistsCapability(t *testing.T) {
	dir := t.TempDir()
	clientStore := openStore(t, dir)
	serverStore := openStore(t, t.TempDir())

	client := newClient(t, clientStore, false)
	server, err := NewNegotiator(Config{
		Protocols: []Protocol{ProtocolV2},
		Store:     serverStore,
	})
	if err != nil {
		t.Fatal(err)
	}

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	serverResult := make(chan Protocol, 1)
	go func() {
		p, err := server.ServerHandshake(serverConn)
		if err != nil {
			t.Errorf("server handshake: %v", err)
		}
		serverResult <- p
	}()

	selected, err := client.ClientHandshake(clientConn)
	if err != nil {
		t.Fatalf("client handshake: %v", err)
	}
	if selected != ProtocolV2 {
		t.Fatalf("client selected %q, want %q", selected, ProtocolV2)
	}
	if got := <-serverResult; got != ProtocolV2 {
		t.Fatalf("server selected %q, want %q", got, ProtocolV2)
	}
	if got := clientStore.Highest(); got != ProtocolV2 {
		t.Fatalf("stored capability %q, want %q", got, ProtocolV2)
	}

	reopened := openStore(t, dir)
	if got := reopened.Highest(); got != ProtocolV2 {
		t.Fatalf("capability after reopen %q, want %q", got, ProtocolV2)
	}
}

func TestReplayedServerHelloRejected(t *testing.T) {
	// Capture a genuine server hello from a completed handshake.
	server, err := NewNegotiator(Config{
		Protocols: []Protocol{ProtocolV2},
		Store:     openStore(t, t.TempDir()),
	})
	if err != nil {
		t.Fatal(err)
	}
	clientConn, serverConn := net.Pipe()
	recorder := &recordingConn{Conn: clientConn}
	done := make(chan struct{})
	go func() {
		_, _ = server.ServerHandshake(serverConn)
		close(done)
	}()
	first := newClient(t, openStore(t, t.TempDir()), false)
	if _, err := first.ClientHandshake(recorder); err != nil {
		t.Fatalf("first handshake: %v", err)
	}
	<-done
	clientConn.Close()
	serverConn.Close()

	// Replay the recorded server hello into a fresh handshake. The stale
	// instance ID must be detected as a replay, not accepted.
	replay := newScriptConn(recorder.recorded.Bytes())
	second := newClient(t, openStore(t, t.TempDir()), true)
	selected, err := second.ClientHandshake(replay)
	if err == nil {
		t.Fatalf("replayed hello accepted as %q", selected)
	}
	if he := asHandshakeError(t, err); he.Category != FailureReplay {
		t.Fatalf("category %q, want %q", he.Category, FailureReplay)
	}
}

func TestTruncationIsNotIncapability(t *testing.T) {
	// The peer is cut after three bytes of a server hello frame: an active
	// truncation, not a genuine no_common_protocol refusal.
	full := frameBytes(t, wireMessage{Kind: kindServerHello, Selected: string(ProtocolV2)})
	truncated := newScriptConn(full[:3])

	client := newClient(t, openStore(t, t.TempDir()), true)
	selected, err := client.ClientHandshake(truncated)
	if err == nil {
		t.Fatalf("truncated handshake fell back to %q", selected)
	}
	if selected == ProtocolLegacy {
		t.Fatal("truncation misclassified as incapability: legacy fallback used")
	}
	if he := asHandshakeError(t, err); he.Category != FailureTruncated {
		t.Fatalf("category %q, want %q", he.Category, FailureTruncated)
	}
}

func TestFallbackPolicy(t *testing.T) {
	serverFails := func() *fakeServer {
		return &fakeServer{respond: func(hello wireMessage) []wireMessage {
			return []wireMessage{{
				Kind:       kindFailure,
				InstanceID: hello.InstanceID,
				Category:   string(FailureNoCommonProtocol),
			}}
		}}
	}

	t.Run("denied without explicit opt-in", func(t *testing.T) {
		client := newClient(t, openStore(t, t.TempDir()), false)
		if _, err := client.ClientHandshake(serverFails()); err == nil {
			t.Fatal("fallback happened without AllowLegacyFallback")
		} else if he := asHandshakeError(t, err); he.Category != FailureNoCommonProtocol {
			t.Fatalf("category %q, want %q", he.Category, FailureNoCommonProtocol)
		}
	})

	t.Run("denied with confirmed newer capability", func(t *testing.T) {
		store := openStore(t, t.TempDir())
		if err := store.Confirm(ProtocolV2); err != nil {
			t.Fatal(err)
		}
		client := newClient(t, store, true)
		if _, err := client.ClientHandshake(serverFails()); err == nil {
			t.Fatal("fallback happened despite confirmed netsec/2 record")
		}
	})

	t.Run("permitted with opt-in and no capability record", func(t *testing.T) {
		client := newClient(t, openStore(t, t.TempDir()), true)
		selected, err := client.ClientHandshake(serverFails())
		if err != nil {
			t.Fatalf("handshake: %v", err)
		}
		if selected != ProtocolLegacy {
			t.Fatalf("selected %q, want legacy %q", selected, ProtocolLegacy)
		}
	})

	t.Run("unbound failure notice treated as replay", func(t *testing.T) {
		spoof := &fakeServer{respond: func(hello wireMessage) []wireMessage {
			return []wireMessage{{
				Kind:       kindFailure,
				InstanceID: "00000000000000000000000000000000",
				Category:   string(FailureNoCommonProtocol),
			}}
		}}
		client := newClient(t, openStore(t, t.TempDir()), true)
		if _, err := client.ClientHandshake(spoof); err == nil {
			t.Fatal("unbound failure notice triggered fallback")
		} else if he := asHandshakeError(t, err); he.Category != FailureReplay {
			t.Fatalf("category %q, want %q", he.Category, FailureReplay)
		}
	})
}

func TestStoreMonotonicAndDurable(t *testing.T) {
	dir := t.TempDir()
	store := openStore(t, dir)

	if err := store.Confirm(ProtocolV2); err != nil {
		t.Fatal(err)
	}
	if err := store.Confirm(ProtocolLegacy); err != nil {
		t.Fatal(err)
	}
	if got := store.Highest(); got != ProtocolV2 {
		t.Fatalf("highest %q, want %q after lower confirm", got, ProtocolV2)
	}

	// Simulate a crash that leaves a stale temp file behind: the committed
	// record must survive intact.
	if err := os.WriteFile(filepath.Join(dir, "capability.json.tmp"), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	reopened := openStore(t, dir)
	if got := reopened.Highest(); got != ProtocolV2 {
		t.Fatalf("highest after crash %q, want %q", got, ProtocolV2)
	}
	if !reopened.HasConfirmedAbove(ProtocolLegacy) {
		t.Fatal("confirmed netsec/2 not reported above legacy")
	}
}

func TestStoreRecoveryFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "capability.json")
	if err := os.WriteFile(path, []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenCapabilityStore(path); err == nil {
		t.Fatal("corrupt store opened without error")
	}

	// A negotiator cannot be built without a store: recovery failure fails
	// closed instead of silently forgetting confirmed capabilities.
	if _, err := NewNegotiator(Config{
		Protocols:           []Protocol{ProtocolV2, ProtocolLegacy},
		Legacy:              ProtocolLegacy,
		AllowLegacyFallback: true,
	}); err == nil {
		t.Fatal("negotiator built without capability store")
	}
}
