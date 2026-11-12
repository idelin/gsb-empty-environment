package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"
)

func validGreeting(t *testing.T, role Role, mechanism Mechanism) []byte {
	t.Helper()
	g, err := NewGreeting(role, mechanism)
	if err != nil {
		t.Fatalf("NewGreeting: %v", err)
	}
	data, err := g.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	return data
}

func TestGreetingWireLayout(t *testing.T) {
	data := validGreeting(t, RoleServer, MechanismCurve)
	if len(data) != GreetingSize {
		t.Fatalf("greeting length = %d, want %d", len(data), GreetingSize)
	}
	if data[0] != 0xFF {
		t.Fatalf("signature[0] = 0x%02x, want 0xff", data[0])
	}
	if data[9] != 0x7F {
		t.Fatalf("signature[9] = 0x%02x, want 0x7f", data[9])
	}
	if data[10] != 3 || data[11] != 1 {
		t.Fatalf("version = %d.%d, want 3.1", data[10], data[11])
	}
	if string(data[12:17]) != "CURVE" {
		t.Fatalf("mechanism prefix = %q, want CURVE", data[12:17])
	}
	for i := 17; i < 32; i++ {
		if data[i] != 0 {
			t.Fatalf("mechanism padding[%d] = 0x%02x, want 0", i, data[i])
		}
	}
	if data[32] != 1 {
		t.Fatalf("as-server = %d, want 1", data[32])
	}
}

func TestParseGreetingRejectsSignatureAndVersion(t *testing.T) {
	tests := []struct {
		name string
		mut  func([]byte)
		want error
	}{
		{
			name: "first signature octet",
			mut:  func(b []byte) { b[0] = 0 },
			want: ErrGreetingSignature,
		},
		{
			name: "tenth signature octet",
			mut:  func(b []byte) { b[9] = 0 },
			want: ErrGreetingSignature,
		},
		{
			name: "major version below three",
			mut:  func(b []byte) { b[10] = 2 },
			want: ErrUnsupportedVersion,
		},
		{
			name: "as-server is not a bit",
			mut:  func(b []byte) { b[32] = 2 },
			want: ErrMalformedAsServer,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := validGreeting(t, RoleServer, MechanismCurve)
			tt.mut(data)
			_, err := ParseGreeting(data)
			if !errors.Is(err, tt.want) {
				t.Fatalf("ParseGreeting error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestParseGreetingAcceptsZMTP3MinorVersions(t *testing.T) {
	for _, minor := range []byte{0, 1, 2, 255} {
		data := validGreeting(t, RoleClient, MechanismNull)
		data[11] = minor
		if _, err := ParseGreeting(data); err != nil {
			t.Fatalf("minor %d: %v", minor, err)
		}
	}
}

func TestReadGreetingRejectsOldVersionAfterPrefix(t *testing.T) {
	data := validGreeting(t, RoleClient, MechanismNull)
	data[10] = 2

	// Only the 11-octet prefix is sent. A pre-3.0 peer must be rejected
	// without waiting for the rest of the ZMTP 3.x greeting.
	_, err := ReadGreeting(bytes.NewReader(data[:11]))
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("ReadGreeting error = %v, want %v", err, ErrUnsupportedVersion)
	}
}

func TestMechanismTailMustBeZeroPadded(t *testing.T) {
	data := validGreeting(t, RoleServer, MechanismCurve)
	data[18] = 0x41

	_, err := ParseGreeting(data)
	if !errors.Is(err, ErrMechanismPadding) {
		t.Fatalf("ParseGreeting error = %v, want %v", err, ErrMechanismPadding)
	}

	field := [20]byte{'C', 'U', 'R', 'V', 'E', 0, 0, 1}
	if _, err := ParseMechanismField(field); !errors.Is(err, ErrMechanismPadding) {
		t.Fatalf("ParseMechanismField error = %v, want %v", err, ErrMechanismPadding)
	}
}

func TestMechanismDowngradeIsRejected(t *testing.T) {
	serverConn, attackerConn := net.Pipe()
	defer attackerConn.Close()

	type result struct {
		session *Session
		err     error
	}
	done := make(chan result, 1)
	go func() {
		session, err := ServerHandshake(serverConn, Config{
			Role:      RoleServer,
			Mechanism: MechanismCurve,
		})
		done <- result{session: session, err: err}
	}()

	// Drain the server's greeting so its write completes before we send the
	// deliberately downgraded greeting.
	if _, err := ReadGreeting(attackerConn); err != nil {
		t.Fatalf("read server greeting: %v", err)
	}
	nullGreeting := validGreeting(t, RoleClient, MechanismNull)
	if err := writeFull(attackerConn, nullGreeting); err != nil {
		t.Fatalf("write attacker greeting: %v", err)
	}

	select {
	case got := <-done:
		if got.session != nil {
			t.Fatal("downgrade handshake unexpectedly succeeded")
		}
		if !errors.Is(got.err, ErrMechanismMismatch) {
			t.Fatalf("error = %v, want ErrMechanismMismatch", got.err)
		}
		if !errors.Is(got.err, ErrMechanismDowngrade) {
			t.Fatalf("error = %v, want ErrMechanismDowngrade", got.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server handshake timed out")
	}
}

func TestOldVersionPrefixFailsClosedWithoutDrainingGreeting(t *testing.T) {
	serverConn, attackerConn := net.Pipe()
	defer attackerConn.Close()

	done := make(chan error, 1)
	go func() {
		_, err := ServerHandshake(serverConn, Config{Mechanism: MechanismCurve})
		done <- err
	}()

	// The attacker sends only the 11-octet old-version prefix and never
	// reads the server's 64-octet greeting.
	oldPrefix := validGreeting(t, RoleClient, MechanismNull)[:11]
	oldPrefix[10] = 2
	if err := writeFull(attackerConn, oldPrefix); err != nil {
		t.Fatalf("write old prefix: %v", err)
	}

	select {
	case err := <-done:
		if !errors.Is(err, ErrUnsupportedVersion) {
			t.Fatalf("error = %v, want %v", err, ErrUnsupportedVersion)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server waited for a full greeting from a pre-3.0 peer")
	}
}

func TestSameRoleIsRejected(t *testing.T) {
	tests := []struct {
		name      string
		localRole Role
		peerRole  Role
	}{
		{name: "server claims server", localRole: RoleServer, peerRole: RoleServer},
		{name: "client claims client", localRole: RoleClient, peerRole: RoleClient},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{Role: tt.localRole, Mechanism: MechanismCurve}
			peer, err := NewGreeting(tt.peerRole, MechanismCurve)
			if err != nil {
				t.Fatalf("NewGreeting: %v", err)
			}
			if err := cfg.ValidatePeer(peer); !errors.Is(err, ErrRoleMismatch) {
				t.Fatalf("ValidatePeer error = %v, want %v", err, ErrRoleMismatch)
			}
		})
	}
}

func TestValidatePeerRejectsOldVersion(t *testing.T) {
	cfg := Config{Role: RoleServer, Mechanism: MechanismCurve}
	peer := Greeting{Major: 2, Mechanism: MechanismCurve, AsServer: false}
	if err := cfg.ValidatePeer(peer); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("ValidatePeer error = %v, want %v", err, ErrUnsupportedVersion)
	}
}

func TestHandshakeSuccessBothRoles(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	clientResult := make(chan error, 1)
	serverResult := make(chan error, 1)

	go func() {
		_, err := ClientHandshake(clientConn, Config{Mechanism: MechanismCurve})
		clientResult <- err
	}()
	go func() {
		session, err := ServerHandshake(serverConn, Config{Mechanism: MechanismCurve})
		if err == nil {
			if session.Peer().AsServer != false || session.Peer().Mechanism != MechanismCurve {
				err = fmt.Errorf("unexpected peer greeting: %+v", session.Peer())
			}
		}
		serverResult <- err
	}()

	for _, result := range []chan error{clientResult, serverResult} {
		select {
		case err := <-result:
			if err != nil {
				t.Fatalf("handshake failed: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("handshake timed out")
		}
	}
}

func TestCurveMessageNonceMustStrictlyIncrease(t *testing.T) {
	box := bytes.Repeat([]byte{0xAA}, curveMessageBoxMinSize)
	var tracker NonceTracker

	for _, nonce := range []uint64{1, 2, 100} {
		if err := tracker.Check(nonce); err != nil {
			t.Fatalf("nonce %d rejected: %v", nonce, err)
		}
	}

	for _, nonce := range []uint64{100, 99, 1} {
		if err := tracker.Check(nonce); !errors.Is(err, ErrNonceReplay) {
			t.Fatalf("nonce %d error = %v, want %v", nonce, err, ErrNonceReplay)
		}
	}

	body, err := EncodeCurveMessage(101, box)
	if err != nil {
		t.Fatalf("EncodeCurveMessage: %v", err)
	}
	message, err := CheckCurveNonce(body, &tracker)
	if err != nil {
		t.Fatalf("CheckCurveNonce: %v", err)
	}
	if message.Nonce != 101 {
		t.Fatalf("nonce = %d, want 101", message.Nonce)
	}
}

func TestReplayedCurveFrameClosesSession(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	clientResult := make(chan *Session, 1)
	serverResult := make(chan *Session, 1)
	go func() {
		session, _ := ClientHandshake(clientConn, Config{Mechanism: MechanismCurve})
		clientResult <- session
	}()
	go func() {
		session, _ := ServerHandshake(serverConn, Config{Mechanism: MechanismCurve})
		serverResult <- session
	}()

	var client *Session
	var server *Session
	for i, result := range []chan *Session{clientResult, serverResult} {
		select {
		case session := <-result:
			if i == 0 {
				client = session
			} else {
				server = session
			}
		case <-time.After(3 * time.Second):
			t.Fatal("handshake timed out")
		}
	}
	if client == nil || server == nil {
		t.Fatal("handshake did not produce both sessions")
	}

	box := bytes.Repeat([]byte{0xBB}, curveMessageBoxMinSize)
	body, err := EncodeCurveMessage(7, box)
	if err != nil {
		t.Fatalf("EncodeCurveMessage: %v", err)
	}
	frame := Frame{Command: true, Body: body}

	writeDone := make(chan error, 1)
	go func() {
		if err := client.WriteFrame(frame); err != nil {
			writeDone <- err
			return
		}
		// This is the replay: the exact same nonce and command body.
		writeDone <- client.WriteFrame(frame)
	}()

	first, err := server.ReadCurveMessage()
	if err != nil {
		t.Fatalf("first CURVE message: %v", err)
	}
	if first.Nonce != 7 {
		t.Fatalf("first nonce = %d, want 7", first.Nonce)
	}

	_, err = server.ReadCurveMessage()
	if !errors.Is(err, ErrNonceReplay) {
		t.Fatalf("replay error = %v, want %v", err, ErrNonceReplay)
	}

	select {
	case <-writeDone:
	case <-time.After(3 * time.Second):
		t.Fatal("replay writer timed out")
	}
}

func TestFrameRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		frame Frame
	}{
		{
			name:  "short command",
			frame: Frame{Command: true, Body: []byte("hello")},
		},
		{
			name:  "multi part",
			frame: Frame{More: true, Command: false, Body: bytes.Repeat([]byte{1}, 255)},
		},
		{
			name:  "long frame",
			frame: Frame{Command: false, Body: bytes.Repeat([]byte{2}, 256)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteFrame(&buf, tt.frame); err != nil {
				t.Fatalf("WriteFrame: %v", err)
			}
			got, err := ReadFrame(&buf, 1<<20)
			if err != nil {
				t.Fatalf("ReadFrame: %v", err)
			}
			if got.More != tt.frame.More || got.Command != tt.frame.Command || !bytes.Equal(got.Body, tt.frame.Body) {
				t.Fatalf("round trip = %+v, want %+v", got, tt.frame)
			}
		})
	}
}

func TestCurveMessageUsesBigEndianNonce(t *testing.T) {
	body := make([]byte, curveMessageMinSize)
	body[0] = byte(curveMessageNameLength)
	copy(body[1:], curveMessageName)
	body[8] = 0x01
	body[9] = 0x23
	body[10] = 0x45
	body[11] = 0x67
	body[12] = 0x89
	body[13] = 0xAB
	body[14] = 0xCD
	body[15] = 0xEF

	message, err := ParseCurveMessage(body)
	if err != nil {
		t.Fatalf("ParseCurveMessage: %v", err)
	}
	if message.Nonce != 0x0123456789ABCDEF {
		t.Fatalf("nonce = 0x%016x", message.Nonce)
	}
	if len(message.Box) != curveMessageBoxMinSize {
		t.Fatalf("box length = %d, want %d", len(message.Box), curveMessageBoxMinSize)
	}
}

func TestMalformedCurveMessageIsRejected(t *testing.T) {
	if _, err := ParseCurveMessage([]byte("HELLO")); !errors.Is(err, ErrNotCurveMessage) {
		t.Fatalf("error = %v, want %v", err, ErrNotCurveMessage)
	}

	short := make([]byte, curveMessageMinSize-1)
	short[0] = byte(curveMessageNameLength)
	copy(short[1:], curveMessageName)
	if _, err := ParseCurveMessage(short); !errors.Is(err, ErrMalformedCurveMessage) {
		t.Fatalf("error = %v, want %v", err, ErrMalformedCurveMessage)
	}
}

func TestNonceExhaustion(t *testing.T) {
	var tracker NonceTracker
	if err := tracker.Check(^uint64(0) - 1); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if tracker.Exhausted() {
		t.Fatal("tracker exhausted too early")
	}
	if err := tracker.Check(^uint64(0)); err != nil {
		t.Fatalf("Check max: %v", err)
	}
	if !tracker.Exhausted() {
		t.Fatal("tracker should be exhausted")
	}
	if err := tracker.Check(^uint64(0)); !errors.Is(err, ErrNonceReplay) {
		t.Fatalf("error = %v, want %v", err, ErrNonceReplay)
	}
}
