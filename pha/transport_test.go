package pha

import (
	"errors"
	"sync"
)

// memConn is one end of a buffered in-memory connection pair. Unlike
// net.Pipe, writes never block on a concurrent reader and deadlines are not
// needed, which keeps the registry tests deterministic.
type memConn struct {
	in   chan []byte
	peer *memConn
	once sync.Once
}

var errClosed = errors.New("memConn: closed")

func newMemPair() (a, b *memConn) {
	a = &memConn{in: make(chan []byte, 64)}
	b = &memConn{in: make(chan []byte, 64)}
	a.peer, b.peer = b, a
	return a, b
}

func (m *memConn) send(frame []byte) error {
	cp := append([]byte(nil), frame...)
	select {
	case m.in <- cp:
		return nil
	default:
		return errors.New("memConn: buffer full")
	}
}

func (m *memConn) recv() ([]byte, error) {
	frame, ok := <-m.in
	if !ok {
		return nil, errClosed
	}
	return frame, nil
}

func (m *memConn) close() {
	m.once.Do(func() { close(m.in) })
}

// frameConn is an in-memory TLS 1.3 record-layer stand-in. Record kind 0x16
// carries a framed handshake message; 0x15 carries a two-byte TLS alert.
type frameConn struct {
	raw *memConn
}

func newFramePair() (*frameConn, *frameConn) {
	a, b := newMemPair()
	return &frameConn{raw: a}, &frameConn{raw: b}
}

func (f *frameConn) WriteHandshake(msg []byte) error {
	frame := append([]byte{0x16}, msg...)
	return f.raw.peer.send(frame)
}

func (f *frameConn) WriteAlert(level, description byte) error {
	return f.raw.peer.send([]byte{0x15, level, description})
}

func (f *frameConn) ReadHandshake() ([]byte, error) {
	for {
		frame, err := f.raw.recv()
		if err != nil {
			return nil, err
		}
		if frame[0] == 0x16 {
			return frame[1:], nil
		}
		return nil, &peerAlert{level: frame[1], description: frame[2]}
	}
}

func (f *frameConn) Close() error {
	f.raw.close()
	return nil
}

type peerAlert struct {
	level, description byte
}

func (e *peerAlert) Error() string {
	return "peer alert"
}

var testSignatureAlgorithms = []uint16{
	0x0804, // ecdsa_secp256r1_sha256
	0x0805, // ecdsa_secp384r1_sha384
}

var testLeaf = []byte("----- simulated leaf certificate DER -----")
