package tls13sm

import (
	"hash"
)

// transcript maintains the running handshake transcript hash and the ordered
// list of handshake messages. It implements the HelloRetryRequest transcript
// replacement from RFC 8446 section 4.4.1.
//
// After a HelloRetryRequest, the running hash is reset and seeded with a
// synthetic handshake message of type message_hash(254) whose body is
// Hash(ClientHello1). All subsequent handshake messages are then appended to
// this reset transcript.
type transcript struct {
	newHash func() hash.Hash
	hash    hash.Hash
	msgList [][]byte
	folded  bool
}

func newTranscript(newHash func() hash.Hash) *transcript {
	return &transcript{newHash: newHash, hash: newHash()}
}

// add appends a complete handshake message (including its four-byte header)
// to the transcript and updates the running hash.
func (t *transcript) add(message []byte) {
	t.hash.Write(message)
	stored := make([]byte, len(message))
	copy(stored, message)
	t.msgList = append(t.msgList, stored)
}

// hashOf returns the current Transcript-Hash value without disturbing the
// running hash.
func (t *transcript) hashOf() []byte {
	return t.hash.Sum(nil)
}

// Messages returns the ordered list of full handshake messages observed so
// far, after any HRR folding.
func (t *transcript) Messages() [][]byte { return t.msgList }

// HRRFolded reports whether the synthetic message_hash transcript replacement
// has already been performed.
func (t *transcript) HRRFolded() bool { return t.folded }

// foldHelloRetryRequest performs the mandatory transcript replacement for
// HelloRetryRequest. clientHello1 is the first ClientHello (full wire bytes)
// and serverHelloHRR is the HelloRetryRequest ServerHello (full wire bytes).
//
// Per RFC 8446 4.4.1 the new transcript starts with:
//
//	Handshake {
//	    msg_type = message_hash(254),
//	    length = Hash.length,
//	    body = Hash(ClientHello1)
//	}
//
// followed by the HelloRetryRequest and every later handshake message. The
// method must be called at most once for a connection; the state machines
// additionally reject any second HRR before reaching this point.
func (t *transcript) foldHelloRetryRequest(clientHello1, serverHelloHRR []byte) error {
	if t.folded {
		// Defensive guard: the state machine is expected to reject a second
		// HRR before this code is reachable.
		return errHRR("synthetic message_hash transcript already installed")
	}

	firstHash := newTranscript(t.newHash)
	firstHash.add(clientHello1)
	messageHash := firstHash.hashOf()

	synthetic := marshalHandshake(HandshakeMessageHash, messageHash)

	t.hash.Reset()
	t.hash.Write(synthetic)
	t.hash.Write(serverHelloHRR)

	t.msgList = nil
	t.msgList = append(t.msgList, synthetic, append([]byte(nil), serverHelloHRR...))
	t.folded = true
	return nil
}

// clone returns an independent copy, used when a state machine needs to
// compute a binder or verify_data over an intermediate transcript.
func (t *transcript) clone() *transcript {
	copyMessages := make([][]byte, len(t.msgList))
	for i, msg := range t.msgList {
		copyMessages[i] = append([]byte(nil), msg...)
	}
	ct := &transcript{
		newHash: t.newHash,
		hash:    t.newHash(),
		msgList: copyMessages,
		folded:  t.folded,
	}
	for _, msg := range ct.msgList {
		ct.hash.Write(msg)
	}
	return ct
}
