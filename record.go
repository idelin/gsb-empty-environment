// Package cid routes RFC 9146 DTLS 1.2 records that carry a Connection ID.
package cid

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// ContentTypeTLS12CID is the DTLS record content type defined for
	// connection-ID records (tls12_cid).
	ContentTypeTLS12CID uint8 = 25

	// ReplayWindowSize is the number of sequence numbers covered by a replay
	// window. It is one bit per sequence number.
	ReplayWindowSize = 64

	// AmplificationFactor is the maximum number of bytes that may be sent to a
	// new peer address for each byte received from that address before the path
	// probe is answered.
	AmplificationFactor = 3
)

const dtlsRecordHeaderLen = 13

var (
	// ErrNotCID reports that the received record does not have content type 25.
	ErrNotCID = errors.New("cid: not a tls12_cid record")

	// ErrMalformedRecord reports an invalid or truncated DTLS record.
	ErrMalformedRecord = errors.New("cid: malformed DTLS record")

	// ErrCIDLengthMismatch reports a record CID whose length does not match the
	// length negotiated for the session.
	ErrCIDLengthMismatch = errors.New("cid: connection ID length mismatch")

	// ErrInvalidCIDLength is an alias for ErrCIDLengthMismatch.
	ErrInvalidCIDLength = ErrCIDLengthMismatch

	// ErrUnknownCID reports that no session is associated with the record CID.
	ErrUnknownCID = errors.New("cid: unknown connection ID")

	// ErrAuthentication reports that record authentication or AEAD opening
	// failed.
	ErrAuthentication = errors.New("cid: record authentication failed")

	// ErrReplay reports a record whose sequence number was already accepted.
	ErrReplay = errors.New("cid: replayed record")

	// ErrAmplificationLimit reports that a send would exceed the anti-
	// amplification limit for an unvalidated peer address.
	ErrAmplificationLimit = errors.New("cid: path amplification limit exceeded")

	// ErrNoLookup reports a Router without a session lookup callback.
	ErrNoLookup = errors.New("cid: no session lookup configured")

	// ErrNoVerifier reports a Router without a record verifier callback.
	ErrNoVerifier = errors.New("cid: no record verifier configured")

	// ErrNoSend reports a Router or send operation without a send callback.
	ErrNoSend = errors.New("cid: no send callback configured")

	// ErrNoProbe reports a Router without a path-probe builder.
	ErrNoProbe = errors.New("cid: no path probe builder configured")

	// ErrPathProbe reports a failure while building or sending a path probe.
	ErrPathProbe = errors.New("cid: path probe failed")

	// ErrPathProbePending reports an application send attempted before the path
	// probe required for an address change was sent.
	ErrPathProbePending = errors.New("cid: path probe pending")

	// ErrWrongPeerAddress reports a send to an address other than the session's
	// current peer address.
	ErrWrongPeerAddress = errors.New("cid: destination is not the current peer address")

	// ErrNoPendingPath reports a path confirmation for a session with no
	// unvalidated path state.
	ErrNoPendingPath = errors.New("cid: no pending path")

	// ErrNoSourceAddress reports a received record with no source address.
	ErrNoSourceAddress = errors.New("cid: record has no source address")
)

// Record is a parsed DTLS record. Fragment is the record fragment as it
// appeared on the wire; for a tls12_cid record it is normally ciphertext.
type Record struct {
	ContentType    uint8
	Version        uint16
	Epoch          uint16
	SequenceNumber uint64
	ConnectionID   []byte
	Fragment       []byte
	Raw            []byte
	HeaderLength   int
}

// CID returns the record's connection ID.
func (r Record) CID() []byte {
	return r.ConnectionID
}

// Sequence returns the record's DTLS sequence number.
func (r Record) Sequence() uint64 {
	return r.SequenceNumber
}

// ParseRecord parses one complete DTLS record. A tls12_cid record has a
// connection_id_length byte and connection ID between the sequence number and
// the record length, as defined by RFC 9146.
func ParseRecord(packet []byte) (Record, error) {
	if len(packet) < dtlsRecordHeaderLen {
		return Record{}, fmt.Errorf("%w: header is %d bytes, need at least %d", ErrMalformedRecord, len(packet), dtlsRecordHeaderLen)
	}

	record := Record{
		ContentType:    packet[0],
		Version:        binary.BigEndian.Uint16(packet[1:3]),
		Epoch:          binary.BigEndian.Uint16(packet[3:5]),
		SequenceNumber: readUint48(packet[5:11]),
		Raw:            packet,
	}

	fragmentOffset := dtlsRecordHeaderLen
	if record.ContentType == ContentTypeTLS12CID {
		if len(packet) < dtlsRecordHeaderLen+1 {
			return Record{}, fmt.Errorf("%w: missing connection ID length", ErrMalformedRecord)
		}
		cidLength := int(packet[11])
		cidStart := 12
		cidEnd := cidStart + cidLength
		if cidEnd > len(packet) || len(packet)-cidEnd < 2 {
			return Record{}, fmt.Errorf("%w: truncated connection ID", ErrMalformedRecord)
		}
		record.ConnectionID = append([]byte(nil), packet[cidStart:cidEnd]...)
		fragmentOffset = cidEnd + 2
	}

	fragmentLength := int(binary.BigEndian.Uint16(packet[fragmentOffset-2 : fragmentOffset]))
	if fragmentLength != len(packet)-fragmentOffset {
		return Record{}, fmt.Errorf("%w: fragment length %d, available %d", ErrMalformedRecord, fragmentLength, len(packet)-fragmentOffset)
	}

	record.Fragment = packet[fragmentOffset:]
	record.HeaderLength = fragmentOffset
	return record, nil
}

func readUint48(b []byte) uint64 {
	return uint64(b[0])<<40 |
		uint64(b[1])<<32 |
		uint64(b[2])<<24 |
		uint64(b[3])<<16 |
		uint64(b[4])<<8 |
		uint64(b[5])
}
