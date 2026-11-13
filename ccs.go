package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
)

// RecordTypeChangeCipherSpec is the TLS record-layer content type of a
// ChangeCipherSpec record.
const RecordTypeChangeCipherSpec = 20

// compatCCSBody is the only payload a compatibility CCS record may carry.
const compatCCSBody = 0x01

// ErrIllegalCCS is returned when a ChangeCipherSpec record appears at a
// position the handshake state machine does not allow.
var ErrIllegalCCS = errors.New("netsec: ChangeCipherSpec at illegal position")

// ErrMalformedCCS is returned when a ChangeCipherSpec record is not the
// exact six-byte compatibility record.
var ErrMalformedCCS = errors.New("netsec: malformed ChangeCipherSpec record")

// Role identifies one end of a TLS connection.
type Role int

const (
	Client Role = iota
	Server
)

func (r Role) peer() Role {
	if r == Client {
		return Server
	}
	return Client
}

// Direction says whether a message travels from the peer to the local
// stack (Inbound) or from the local stack to the peer (Outbound).
type Direction int

const (
	Inbound Direction = iota
	Outbound
)

// KeyPhase is the record-protection generation the connection is in. A
// compatibility CCS must never advance it; only handshake messages do.
type KeyPhase int

const (
	KeyPhaseInitial KeyPhase = iota
	KeyPhaseHandshake
	KeyPhaseApplication
)

// Handler tracks one connection's handshake so the TLS stack can ask it
// whether a compatibility ChangeCipherSpec record is legal right now.
// The stack reports every handshake message with Handshake and every CCS
// record with ChangeCipherSpec; only the former reach the transcript.
//
// Legal CCS positions:
//   - server: after its first handshake message (ServerHello or
//     HelloRetryRequest), before any later server message;
//   - client: after the server's first message, before the client's
//     second flight (any client message after ClientHello);
//   - either peer: exactly once more after that peer's Finished.
//
// Anywhere else the record is rejected with ErrIllegalCCS.
type Handler struct {
	role Role
	tx   *Transcript

	serverFirstSeen  bool
	serverWindowOpen bool
	clientWindowOpen bool

	finishedSeen [2]bool
	postFinCCS   [2]bool

	phase KeyPhase
}

// NewHandler returns a Handler for the local endpoint's role.
func NewHandler(role Role) *Handler {
	return &Handler{role: role, tx: NewTranscript()}
}

func (h *Handler) senderOf(dir Direction) Role {
	if dir == Outbound {
		return h.role
	}
	return h.role.peer()
}

// Handshake registers one handshake message: it is appended to the
// transcript and advances the CCS windows and the key phase.
func (h *Handler) Handshake(dir Direction, typ HandshakeType, body []byte) {
	sender := h.senderOf(dir)
	switch {
	case typ == ServerHello && sender == Server:
		// Also matches HelloRetryRequest, which rides in a ServerHello.
		if h.serverFirstSeen {
			h.serverWindowOpen = false
		} else {
			h.serverFirstSeen = true
			h.serverWindowOpen = true
			h.clientWindowOpen = true
			h.phase = KeyPhaseHandshake
		}
	case typ == Finished:
		if sender == Server {
			h.serverWindowOpen = false
		} else {
			h.clientWindowOpen = false
		}
		h.finishedSeen[sender] = true
		h.phase = KeyPhaseApplication
	default:
		if sender == Server && h.serverFirstSeen {
			h.serverWindowOpen = false
		}
		if sender == Client && typ != ClientHello {
			h.clientWindowOpen = false
		}
	}
	h.tx.Add(typ, body)
}

// ChangeCipherSpec registers one compatibility CCS record. The record is
// validated and its position checked; it is never added to the transcript
// and never changes the key phase. It returns ErrMalformedCCS for anything
// but the exact compatibility record and ErrIllegalCCS for a record at an
// illegal position.
func (h *Handler) ChangeCipherSpec(dir Direction, record []byte) error {
	if err := checkCompatRecord(record); err != nil {
		return err
	}
	sender := h.senderOf(dir)

	if h.finishedSeen[sender] {
		// One extra CCS is tolerated after the sender's Finished.
		if h.postFinCCS[sender] {
			return ErrIllegalCCS
		}
		h.postFinCCS[sender] = true
		return nil
	}

	switch sender {
	case Server:
		if !h.serverWindowOpen {
			return ErrIllegalCCS
		}
		h.serverWindowOpen = false
	case Client:
		if !h.clientWindowOpen {
			return ErrIllegalCCS
		}
		h.clientWindowOpen = false
	}
	return nil
}

// TranscriptHash returns the current handshake transcript hash, the value
// Finished verify_data binds to.
func (h *Handler) TranscriptHash() []byte {
	return h.tx.Sum()
}

// KeyPhase returns the current record-protection generation.
func (h *Handler) KeyPhase() KeyPhase {
	return h.phase
}

// VerifyData computes the Finished verify_data binding the given traffic
// secret to the current transcript. Because a CCS never enters the
// transcript, inserting one at a legal position cannot change this value.
func VerifyData(secret, transcriptHash []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("finished"))
	mac.Write(transcriptHash)
	return mac.Sum(nil)
}

// MarshalCompatCCS returns the wire form of the compatibility CCS record:
// content type 20, legacy version 0x0303, length 1, body 0x01.
func MarshalCompatCCS() []byte {
	return []byte{RecordTypeChangeCipherSpec, 0x03, 0x03, 0x00, 0x01, compatCCSBody}
}

func checkCompatRecord(record []byte) error {
	if len(record) != 6 ||
		record[0] != RecordTypeChangeCipherSpec ||
		record[1] != 0x03 ||
		record[3] != 0x00 || record[4] != 0x01 ||
		record[5] != compatCCSBody {
		return ErrMalformedCCS
	}
	return nil
}
