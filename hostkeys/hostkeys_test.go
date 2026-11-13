package hostkeys_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	notifyType = "hostkeys-00@openssh.com"
	proveType  = "hostkeys-prove-00@openssh.com"
)

// serverBehavior controls how the in-process SSH server answers a rotation.
type serverBehavior struct {
	advertise [][]byte

	// signers maps key blob -> signer.
	signers map[string]ssh.Signer

	// noProve makes the server reject hostkeys-prove requests entirely.
	noProve bool

	// foreignSession signs with a random session id instead of the real
	// connection session id.
	foreignSession bool

	// tamperIndex >= 0 replaces the signature at that index with random bytes,
	// exercising partial-failure handling within one batch.
	tamperIndex int
}

type testServer struct {
	listener net.Listener
	config   *ssh.ServerConfig

	mu       sync.Mutex
	behavior *serverBehavior
	ready    chan struct{}
	conn     ssh.Conn
}

func newTestSigner(t *testing.T) (ssh.Signer, ssh.PublicKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("signer from key: %v", err)
	}
	return signer, signer.PublicKey()
}

func startServer(t *testing.T, initial ssh.Signer, behavior *serverBehavior) *testServer {
	t.Helper()

	config := &ssh.ServerConfig{NoClientAuth: true}
	config.AddHostKey(initial)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	srv := &testServer{
		listener: listener,
		config:   config,
		behavior: behavior,
		ready:    make(chan struct{}, 1),
	}
	go srv.serve()
	t.Cleanup(func() {
		listener.Close()
		srv.mu.Lock()
		if srv.conn != nil {
			srv.conn.Close()
		}
		srv.mu.Unlock()
	})
	return srv
}

func (s *testServer) serve() {
	for {
		tcpConn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handle(tcpConn)
	}
}

func (s *testServer) handle(tcpConn net.Conn) {
	conn, chans, reqs, err := ssh.NewServerConn(tcpConn, s.config)
	if err != nil {
		return
	}
	s.mu.Lock()
	s.conn = conn
	s.mu.Unlock()
	defer conn.Close()

	go s.requestLoop(reqs)
	go func() {
		for ch := range chans {
			ch.Reject(ssh.UnknownChannelType, "not used")
		}
	}()

	// Wait until the client's request pump is installed so the notification
	// cannot be delivered before the rotator's interceptor is running.
	select {
	case <-s.ready:
	case <-time.After(5 * time.Second):
		return
	}

	if behavior := s.currentBehavior(); behavior != nil && len(behavior.advertise) > 0 {
		_, _ = conn.SendRequest(notifyType, false, marshalStringList(behavior.advertise))
	}

	_ = conn.Wait()
}

func (s *testServer) requestLoop(reqs <-chan *ssh.Request) {
	for req := range reqs {
		switch req.Type {
		case proveType:
			s.answerProve(req)
		default:
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
	}
}

func (s *testServer) answerProve(req *ssh.Request) {
	behavior := s.currentBehavior()
	if behavior == nil || behavior.noProve {
		_ = req.Reply(false, nil)
		return
	}

	var requested [][]byte
	if err := ssh.Unmarshal(req.Payload, &requested); err != nil {
		_ = req.Reply(false, nil)
		return
	}

	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()

	var signatures [][]byte
	for i, blob := range requested {
		signer, ok := behavior.signers[string(blob)]
		if !ok {
			_ = req.Reply(false, nil)
			return
		}

		if behavior.tamperIndex == i {
			bogus := make([]byte, 64)
			_, _ = rand.Read(bogus)
			signatures = append(signatures, bogus)
			continue
		}

		sessionID := conn.SessionID()
		if behavior.foreignSession {
			sessionID = make([]byte, len(conn.SessionID()))
			_, _ = rand.Read(sessionID)
		}

		signed := make([]byte, 0, len(sessionID)+len(blob))
		signed = append(signed, sessionID...)
		signed = append(signed, blob...)
		sig, err := signer.Sign(rand.Reader, signed)
		if err != nil {
			_ = req.Reply(false, nil)
			return
		}
		signatures = append(signatures, ssh.Marshal(sig))
	}
	_ = req.Reply(true, marshalStringList(signatures))
}

func (s *testServer) currentBehavior() *serverBehavior {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.behavior
}

func marshalStringList(items [][]byte) []byte {
	out := new(bytes.Buffer)
	for _, item := range items {
		out.Write(ssh.String(item))
	}
	return out.Bytes()
}
