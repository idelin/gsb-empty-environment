package tls13

import (
	"encoding"
	"errors"
	"hash"
)

// Transcript is the running TLS 1.3 handshake transcript hash.
type Transcript struct {
	suite CipherSuite
	h     hash.Hash
}

// NewTranscript returns an empty transcript using the cipher suite hash.
func NewTranscript(suite CipherSuite) *Transcript {
	return &Transcript{suite: suite, h: suite.NewHash()}
}

// Write appends raw handshake message bytes to the transcript.
func (t *Transcript) Write(p []byte) (int, error) {
	if t == nil || t.h == nil {
		return 0, errors.New("tls13: nil transcript")
	}
	return t.h.Write(p)
}

// WriteMessages appends each complete handshake message to the transcript.
func (t *Transcript) WriteMessages(messages ...[]byte) error {
	for _, message := range messages {
		if _, err := t.Write(message); err != nil {
			return err
		}
	}
	return nil
}

// Sum returns Transcript-Hash of all messages written so far.
func (t *Transcript) Sum() []byte {
	return t.h.Sum(nil)
}

// Hash is an alias for Sum.
func (t *Transcript) Hash() []byte { return t.Sum() }

// Clone returns an independent copy of the running hash state.
func (t *Transcript) Clone() (*Transcript, error) {
	if t == nil {
		return nil, errors.New("tls13: nil transcript")
	}
	cloned := NewTranscript(t.suite)
	marshaler, ok := t.h.(encoding.BinaryMarshaler)
	if !ok {
		return nil, errors.New("tls13: transcript hash does not support cloning")
	}
	data, err := marshaler.MarshalBinary()
	if err != nil {
		return nil, err
	}
	unmarshaler, ok := cloned.h.(encoding.BinaryUnmarshaler)
	if !ok {
		return nil, errors.New("tls13: transcript hash does not support cloning")
	}
	if err := unmarshaler.UnmarshalBinary(data); err != nil {
		return nil, err
	}
	return cloned, nil
}

// SynthesizeMessageHash builds:
//
//	message_hash = 0xFE || uint24(HashLen) || Hash(ClientHello1)
//
// The length field is the TLS uint24 encoding, e.g. 00 00 20 for SHA-256.
func SynthesizeMessageHash(suite CipherSuite, clientHello1 []byte) ([]byte, error) {
	digest := hashMessages(suite, clientHello1)
	if len(digest) > 0xffffff {
		return nil, errors.New("tls13: hash is too long")
	}
	out := make([]byte, 4+len(digest))
	out[0] = 0xfe
	out[1] = 0
	out[2] = 0
	out[3] = byte(len(digest))
	copy(out[4:], digest)
	return out, nil
}

// MessageHash is an alias for SynthesizeMessageHash.
func MessageHash(suite CipherSuite, clientHello1 []byte) ([]byte, error) {
	return SynthesizeMessageHash(suite, clientHello1)
}

// NewHRRTranscript applies RFC 8446's special transcript replacement. The
// returned hash contains message_hash(ClientHello1) followed by hrr. Append
// ClientHello2 and later messages to the returned Transcript.
func NewHRRTranscript(suite CipherSuite, clientHello1, hrr []byte) (*Transcript, error) {
	messageHash, err := SynthesizeMessageHash(suite, clientHello1)
	if err != nil {
		return nil, err
	}
	transcript := NewTranscript(suite)
	if err := transcript.WriteMessages(messageHash, hrr); err != nil {
		return nil, err
	}
	return transcript, nil
}

// ResetAsMessageHash discards the current transcript state and starts with
// message_hash(ClientHello1). This is useful when ClientHello1 was already
// streamed into the transcript before HRR was received.
func (t *Transcript) ResetAsMessageHash(clientHello1 []byte) error {
	if t == nil {
		return errors.New("tls13: nil transcript")
	}
	messageHash, err := SynthesizeMessageHash(t.suite, clientHello1)
	if err != nil {
		return err
	}
	t.h = t.suite.NewHash()
	return t.WriteMessages(messageHash)
}

func hashMessages(suite CipherSuite, messages ...[]byte) []byte {
	h := suite.NewHash()
	for _, message := range messages {
		h.Write(message)
	}
	return h.Sum(nil)
}
