package delta

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"io"
	"net"
)

// Wire protocol: every message is a 4-byte big-endian length prefix
// followed by a gob-encoded envelope.
//
// A sync round is symmetric and delta-based:
//
//	1. both peers send Delta  (ops not yet acked by the other side)
//	2. both peers merge and reply Ack (causal context after merge)
//	3. both peers record the ack as the other's confirmed watermark
//
// The first round (zero watermark) degrades gracefully to a full-state
// transfer; later rounds only carry the operations that changed since
// the last ack.

type msgKind uint8

const (
	msgDelta msgKind = iota + 1
	msgAck
)

// maxMessage bounds a single wire message (1 GiB) to bound buffers.
const maxMessage = 1 << 30

type envelope struct {
	From  ReplicaID
	Kind  msgKind
	Delta Delta
	Ack   Ack
}

func writeEnvelope(w io.Writer, env envelope) error {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(env); err != nil {
		return err
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(buf.Len()))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}

func readEnvelope(r io.Reader) (envelope, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return envelope{}, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > maxMessage {
		return envelope{}, fmt.Errorf("delta: message too large: %d bytes", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return envelope{}, err
	}
	var env envelope
	if err := gob.NewDecoder(bytes.NewReader(buf)).Decode(&env); err != nil {
		return envelope{}, err
	}
	return env, nil
}

// EncodedSize returns the on-the-wire payload size in bytes of a delta
// as sent by the sync protocol (gob body, excluding the 4-byte length
// prefix). Useful for measuring sync traffic.
func EncodedSize(d Delta) (int, error) {
	var buf bytes.Buffer
	err := gob.NewEncoder(&buf).Encode(envelope{Kind: msgDelta, Delta: d})
	return buf.Len(), err
}

// sendAsync writes env on a background goroutine so that synchronous
// transports (e.g. net.Pipe) cannot deadlock when both sides of a
// symmetric round write before reading.
func sendAsync(conn net.Conn, env envelope) <-chan error {
	ch := make(chan error, 1)
	go func() { ch <- writeEnvelope(conn, env) }()
	return ch
}

// SyncRound executes one round on an established connection.
func syncRound(n *Node, peer ReplicaID, conn net.Conn) error {
	w1 := sendAsync(conn, envelope{From: n.ID(), Kind: msgDelta, Delta: n.DeltaFor(peer)})
	env, err := readEnvelope(conn)
	if err != nil {
		return err
	}
	if err := <-w1; err != nil {
		return err
	}
	if env.Kind != msgDelta {
		return fmt.Errorf("delta: expected delta message, got kind %d", env.Kind)
	}
	ack := n.HandleDelta(env.From, env.Delta)

	w2 := sendAsync(conn, envelope{From: n.ID(), Kind: msgAck, Ack: ack})
	env, err = readEnvelope(conn)
	if err != nil {
		return err
	}
	if err := <-w2; err != nil {
		return err
	}
	if env.Kind != msgAck {
		return fmt.Errorf("delta: expected ack message, got kind %d", env.Kind)
	}
	n.HandleAck(env.From, env.Ack)
	return nil
}

// SyncConn performs rounds symmetric delta-sync rounds with the peer at
// the other end of conn. Both sides must call SyncConn with the remote
// replica ID and the same round count. Works over TCP and net.Pipe.
func SyncConn(n *Node, peer ReplicaID, conn net.Conn, rounds int) error {
	for i := 0; i < rounds; i++ {
		if err := syncRound(n, peer, conn); err != nil {
			return err
		}
	}
	return nil
}
