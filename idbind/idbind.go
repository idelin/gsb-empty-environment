// Package idbind implements IKEv2 initiator identity binding for the
// responder's SA authorization step, per RFC 7296.
//
// An attacker holding a valid certificate may claim a different identity
// in IDi to inherit broader access policy. This package reconstructs the
// initiator's signed octets (RFC 7296 Section 2.15), binds the IDi type
// and value to the peer certificate's SANs or subject, and confines the
// CHILD_SA traffic selectors to the policy of the authenticated identity:
// a proposal may narrow the policy but never exceed it.
package idbind

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"fmt"
)

// PRF is an IKEv2 pseudo-random function (RFC 7296 Section 2.13).
type PRF func(key, data []byte) []byte

// PRFHMACSHA1 is prf+ base PRF HMAC-SHA1 (single block; IKE AUTH inputs
// are always shorter than one HMAC block output chain requirement here
// because MACedIDForI is a single prf() invocation, not prf+).
func PRFHMACSHA1(key, data []byte) []byte {
	mac := hmac.New(sha1.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

// PRFHMACSHA256 is the HMAC-SHA256 PRF.
func PRFHMACSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

// PRFHMACSHA512 is the HMAC-SHA512 PRF.
func PRFHMACSHA512(key, data []byte) []byte {
	mac := hmac.New(sha512.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

// IDType is an IKEv2 Identification payload type (RFC 7296 Section 3.5).
type IDType uint8

const (
	ID_IPV4_ADDR   IDType = 1
	ID_FQDN        IDType = 2
	ID_RFC822_ADDR IDType = 3
	ID_IPV6_ADDR   IDType = 5
	ID_DER_ASN1_DN IDType = 9
	ID_DER_ASN1_GN IDType = 10
	ID_KEY_ID      IDType = 11
)

func (t IDType) String() string {
	switch t {
	case ID_IPV4_ADDR:
		return "ID_IPV4_ADDR"
	case ID_FQDN:
		return "ID_FQDN"
	case ID_RFC822_ADDR:
		return "ID_RFC822_ADDR"
	case ID_IPV6_ADDR:
		return "ID_IPV6_ADDR"
	case ID_DER_ASN1_DN:
		return "ID_DER_ASN1_DN"
	case ID_DER_ASN1_GN:
		return "ID_DER_ASN1_GN"
	case ID_KEY_ID:
		return "ID_KEY_ID"
	default:
		return fmt.Sprintf("ID_TYPE_%d", uint8(t))
	}
}

// IDPayload is a parsed IKEv2 Identification payload.
type IDPayload struct {
	Type IDType
	// Data is the Identification Data field only (after the 4-byte
	// ID-specific header).
	Data []byte
}

// genericHeaderLen is the IKEv2 generic payload header length.
const genericHeaderLen = 4

// idHeaderLen is the Identification payload header length: 4-byte generic
// header plus ID type (1) and reserved (3).
const idHeaderLen = 8

// ParseIDPayload parses a full Identification payload, including the
// 4-byte generic IKE payload header.
func ParseIDPayload(payload []byte) (*IDPayload, error) {
	if len(payload) < idHeaderLen {
		return nil, fmt.Errorf("idbind: ID payload too short: %d bytes", len(payload))
	}
	declared := int(binary.BigEndian.Uint16(payload[2:4]))
	if declared != len(payload) {
		return nil, fmt.Errorf("idbind: ID payload length mismatch: header says %d, got %d", declared, len(payload))
	}
	id := &IDPayload{
		Type: IDType(payload[4]),
		Data: make([]byte, len(payload)-idHeaderLen),
	}
	copy(id.Data, payload[idHeaderLen:])
	if len(id.Data) == 0 {
		return nil, errors.New("idbind: ID payload has empty identification data")
	}
	return id, nil
}

// Marshal encodes the payload including the generic IKE payload header.
// nextPayload is the payload type of the following payload (0 if last).
func (p *IDPayload) Marshal(nextPayload uint8) []byte {
	out := make([]byte, idHeaderLen+len(p.Data))
	out[0] = nextPayload
	binary.BigEndian.PutUint16(out[2:4], uint16(len(out)))
	out[4] = byte(p.Type)
	copy(out[idHeaderLen:], p.Data)
	return out
}

// Body returns the payload body: ID type, reserved bytes, and
// Identification Data, excluding the 4-byte generic header. This is IDi'
// as defined by RFC 7296 Section 2.15.
func (p *IDPayload) Body() []byte {
	body := make([]byte, 4+len(p.Data))
	body[0] = byte(p.Type)
	copy(body[4:], p.Data)
	return body
}

// BuildInitiatorSignedOctets reconstructs the octets signed by the
// initiator in the AUTH payload, per RFC 7296 Section 2.15:
//
//	InitiatorSignedOctets = RealMessage1 | NonceRData | MACedIDForI
//	MACedIDForI = prf(SK_pi, IDi')
//
// realMessage1 must be the initiator's first IKE_SA_INIT message exactly
// as received on the wire (including the IKE header). nonceR is the
// responder's nonce (without the payload header). idiBody is the IDi
// payload body as returned by IDPayload.Body. skPi is SK_pi from the IKE
// SA keymat, and prf is the negotiated PRF.
func BuildInitiatorSignedOctets(realMessage1, nonceR, idiBody, skPi []byte, prf PRF) []byte {
	macedID := prf(skPi, idiBody)
	out := make([]byte, 0, len(realMessage1)+len(nonceR)+len(macedID))
	out = append(out, realMessage1...)
	out = append(out, nonceR...)
	out = append(out, macedID...)
	return out
}

// BuildResponderSignedOctets reconstructs the octets the responder signs
// for its own AUTH payload, per RFC 7296 Section 2.15:
//
//	ResponderSignedOctets = RealMessage2 | NonceIData | MACedIDForR
func BuildResponderSignedOctets(realMessage2, nonceI, idrBody, skPr []byte, prf PRF) []byte {
	macedID := prf(skPr, idrBody)
	out := make([]byte, 0, len(realMessage2)+len(nonceI)+len(macedID))
	out = append(out, realMessage2...)
	out = append(out, nonceI...)
	out = append(out, macedID...)
	return out
}
