// Package netsec implements TLS 1.2 extended_master_secret (RFC 7627)
// admission control for session resumption.
//
// The triple handshake (CVE-2014-1295) lets a man-in-the-middle splice two
// TLS 1.2 handshakes together by abusing session resumption, because the
// legacy master secret is derived from randoms only and is not bound to the
// full handshake transcript. The extended_master_secret extension fixes this
// by deriving the master secret from the transcript hash (the "session
// hash"). A server that supports the extension must enforce consistent use
// of it between the original session and every resumption:
//
//   - A session originally established with the extension MUST only be
//     resumed when the ClientHello also carries the extension, otherwise the
//     handshake is aborted with a fatal handshake_failure alert.
//   - A session originally established without the extension is never
//     resumed once the client offers the extension; the server falls back to
//     a full handshake and negotiates the extension.
//
// This package is deliberately transport-agnostic: the caller supplies the
// cryptographic PRF of the negotiated cipher suite (via [PRF]) and the
// session hash it computed from the handshake transcript. All admission
// decisions and key derivations are pure functions, which makes the full
// combination matrix and transcript-tampering checks unit testable.
package netsec

import (
	"crypto/hmac"
	"encoding/binary"
	"errors"
	"hash"
)

// ExtensionExtendedMasterSecret is the extension number assigned by
// RFC 7627 for the extended_master_secret extension. Its extension_data is
// defined to be empty.
const ExtensionExtendedMasterSecret uint16 = 0x0017

// VersionTLS12 is the only protocol version this package supports.
const VersionTLS12 uint16 = 0x0303

// AlertHandshakeFailure is the fatal alert the caller must send when
// [Decision.Action] is [ActionAbort] (RFC 7627 section 5.3).
const AlertHandshakeFailure byte = 40

// Labels used by the TLS 1.2 PRF, exactly as defined by RFC 5246 and
// RFC 7627, including the trailing space.
const (
	labelMasterSecret      = "master secret"
	labelExtendedMasterSec = "extended master secret"
	labelClientFinished    = "client finished"
	labelServerFinished    = "server finished"

	masterSecretLength     = 48
	sessionRecordMagic     = "EMSR"
	sessionRecordFixedSize = 4 + 2 + 1 + 1 + 48 // magic, version, usedEMS, reserved, masterSecret
)

// PRF is the pseudorandom function of the negotiated cipher suite, in the
// TLS 1.2 signature of RFC 5246 section 5: it expands secret from seed using
// label as part of the seed. Callers inject the implementation so this
// package stays independent of any specific cipher suite hash.
type PRF func(secret []byte, label string, seed []byte, outputLength int) ([]byte, error)

// TLS12PRF builds the standard TLS 1.2 PRF (RFC 5246 section 5) on top of
// the injected HMAC hash constructor (for example crypto/sha256.New or
// crypto/sha512.New, depending on the negotiated cipher suite).
func TLS12PRF(newHash func() hash.Hash) PRF {
	return func(secret []byte, label string, seed []byte, outputLength int) ([]byte, error) {
		if outputLength <= 0 {
			return nil, errors.New("netsec: PRF output length must be positive")
		}
		labeledSeed := make([]byte, 0, len(label)+len(seed))
		labeledSeed = append(labeledSeed, label...)
		labeledSeed = append(labeledSeed, seed...)

		half := (len(secret) + 1) / 2
		firstSecret := secret[:half]
		secondSecret := secret[len(secret)-half:]

		pHash := func(key []byte) []byte {
			var out []byte
			a := labeledSeed
			for len(out) < outputLength {
				mac := hmac.New(newHash, key)
				mac.Write(a)
				a = mac.Sum(nil)

				mac = hmac.New(newHash, key)
				mac.Write(a)
				mac.Write(labeledSeed)
				out = append(out, mac.Sum(nil)...)
			}
			return out[:outputLength]
		}

		first := pHash(firstSecret)
		second := pHash(secondSecret)
		out := make([]byte, outputLength)
		for i := range out {
			out[i] = first[i] ^ second[i]
		}
		return out, nil
	}
}

// Action tells the caller what kind of handshake to run after [Admit].
type Action byte

const (
	// ActionFull means no resumption is allowed; perform a complete
	// handshake. This covers "no session to resume" and the RFC 7627
	// mismatch case where the original session lacked the extension but
	// the new ClientHello offers it.
	ActionFull Action = iota + 1
	// ActionResume means an abbreviated handshake is permitted.
	ActionResume
	// ActionAbort means the policy violation is fatal: send a
	// handshake_failure alert and terminate the connection.
	ActionAbort
)

// String returns a human-readable name for the action.
func (a Action) String() string {
	switch a {
	case ActionFull:
		return "full-handshake"
	case ActionResume:
		return "resume"
	case ActionAbort:
		return "abort"
	default:
		return "unknown-action"
	}
}

// Decision is the admission result for an incoming ClientHello.
type Decision struct {
	// Action selects full handshake, abbreviated handshake, or abort.
	Action Action
	// UseEMS reports whether the negotiated connection must use the
	// extended_master_secret computation. When Action is ActionResume it
	// mirrors the original session, since the extension presence matched.
	UseEMS bool
	// Alert carries the fatal alert description when Action is
	// ActionAbort; it is zero otherwise.
	Alert byte
}

// SessionRecord is the resumption state persisted at the end of a full
// handshake and looked up when a later ClientHello presents the session ID.
type SessionRecord struct {
	// Version must be VersionTLS12.
	Version uint16
	// UsedEMS records whether the original connection negotiated the
	// extended_master_secret extension.
	UsedEMS bool
	// MasterSecret is the 48-byte master secret of the original
	// connection, derived either classically or via the session hash.
	MasterSecret []byte
}

// Marshal serializes the record into a fixed-size, self-describing binary
// blob suitable for storing in a session cache or ticket. The format is:
//
//	"EMSR" magic (4 bytes)
//	protocol version, big endian (2 bytes)
//	UsedEMS flag (1 byte)
//	reserved (1 byte)
//	master secret (48 bytes)
func (r SessionRecord) Marshal() ([]byte, error) {
	if r.Version != VersionTLS12 {
		return nil, errors.New("netsec: session record is not TLS 1.2")
	}
	if len(r.MasterSecret) != masterSecretLength {
		return nil, errors.New("netsec: master secret must be 48 bytes")
	}
	buf := make([]byte, 0, sessionRecordFixedSize)
	buf = append(buf, sessionRecordMagic...)
	buf = binary.BigEndian.AppendUint16(buf, r.Version)
	if r.UsedEMS {
		buf = append(buf, 1)
	} else {
		buf = append(buf, 0)
	}
	buf = append(buf, 0)
	buf = append(buf, r.MasterSecret...)
	return buf, nil
}

// UnmarshalSessionRecord parses a record previously produced with
// [SessionRecord.Marshal].
func UnmarshalSessionRecord(data []byte) (SessionRecord, error) {
	if len(data) != sessionRecordFixedSize {
		return SessionRecord{}, errors.New("netsec: malformed session record length")
	}
	if string(data[:4]) != sessionRecordMagic {
		return SessionRecord{}, errors.New("netsec: malformed session record magic")
	}
	version := binary.BigEndian.Uint16(data[4:6])
	if version != VersionTLS12 {
		return SessionRecord{}, errors.New("netsec: session record is not TLS 1.2")
	}
	switch data[6] {
	case 0, 1:
	default:
		return SessionRecord{}, errors.New("netsec: malformed extended_master_secret flag")
	}
	secret := make([]byte, masterSecretLength)
	copy(secret, data[8:])
	return SessionRecord{
		Version:      version,
		UsedEMS:      data[6] == 1,
		MasterSecret: secret,
	}, nil
}

// Admit applies the RFC 7627 resumption consistency policy to an incoming
// ClientHello.
//
// record is the cached state for the session ID the client presented, or nil
// when the client did not present a usable session (a fresh full handshake is
// required). clientOffersEMS reports whether the ClientHello contains the
// extended_master_secret extension, as parsed by [ClientOffersEMS] or
// [HasExtension].
//
// The four resumption combinations resolve as:
//
//   - EMS session, EMS offered: abbreviated handshake with EMS.
//   - EMS session, EMS missing: fatal handshake_failure, abort.
//   - legacy session, EMS offered: no resumption; full handshake with EMS.
//   - legacy session, EMS missing: abbreviated handshake without EMS.
func Admit(record *SessionRecord, clientOffersEMS bool) (Decision, error) {
	if record == nil {
		return Decision{Action: ActionFull, UseEMS: clientOffersEMS}, nil
	}
	if record.Version != VersionTLS12 {
		return Decision{}, errors.New("netsec: cannot resume a non-TLS-1.2 session")
	}
	if len(record.MasterSecret) != masterSecretLength {
		return Decision{}, errors.New("netsec: cached master secret has invalid length")
	}

	switch {
	case record.UsedEMS && !clientOffersEMS:
		// RFC 7627 section 5.3: an EMS-protected session MUST NOT be
		// resumed without the extension; abort instead of silently
		// downgrading to a legacy abbreviated handshake.
		return Decision{
			Action: ActionAbort,
			UseEMS: true,
			Alert:  AlertHandshakeFailure,
		}, nil
	case !record.UsedEMS && clientOffersEMS:
		// The extension was added since the original session; the cached
		// master secret is not transcript-bound, so refuse resumption and
		// renegotiate from scratch with EMS enabled.
		return Decision{Action: ActionFull, UseEMS: true}, nil
	case record.UsedEMS:
		return Decision{Action: ActionResume, UseEMS: true}, nil
	default:
		return Decision{Action: ActionResume, UseEMS: false}, nil
	}
}

// DeriveMasterSecret computes the 48-byte TLS 1.2 master secret at the end of
// a full handshake. preMasterSecret and sessionHash are caller-supplied
// bytes; sessionHash is the hash over every handshake message up to (but not
// including) the ClientKeyExchange, computed with the cipher suite hash.
//
// When useEMS is true (the extension was negotiated), the seed is the
// session hash alone and the "extended master secret" label is used, per
// RFC 7627 section 3. Otherwise the classical RFC 5246 seed
// (clientRandom || serverRandom) and "master secret" label are used.
//
// On an abbreviated handshake no derivation happens: the cached master
// secret from the original connection is reused unchanged.
func DeriveMasterSecret(prf PRF, useEMS bool, preMasterSecret, clientRandom, serverRandom, sessionHash []byte) ([]byte, error) {
	if prf == nil {
		return nil, errors.New("netsec: PRF is required")
	}
	if len(preMasterSecret) == 0 {
		return nil, errors.New("netsec: pre-master secret is required")
	}

	var seed []byte
	var label string
	if useEMS {
		if len(sessionHash) == 0 {
			return nil, errors.New("netsec: session hash is required for extended master secret")
		}
		seed = sessionHash
		label = labelExtendedMasterSec
	} else {
		if len(clientRandom) == 0 || len(serverRandom) == 0 {
			return nil, errors.New("netsec: client and server randoms are required for legacy master secret")
		}
		seed = make([]byte, 0, len(clientRandom)+len(serverRandom))
		seed = append(seed, clientRandom...)
		seed = append(seed, serverRandom...)
		label = labelMasterSecret
	}

	return prf(preMasterSecret, label, seed, masterSecretLength)
}

// VerifyData computes a TLS 1.2 Finished message verify_data block. It is
// provided so callers can prove that EMS-derived secrets are bound to the
// handshake transcript: any change to sessionHash changes the master secret
// and therefore the verify_data.
//
// fromClient selects the "client finished" / "server finished" label and
// finishedHash is the running handshake hash at the point the Finished
// message is produced.
func VerifyData(prf PRF, masterSecret, finishedHash []byte, fromClient bool, length int) ([]byte, error) {
	if prf == nil {
		return nil, errors.New("netsec: PRF is required")
	}
	if len(masterSecret) != masterSecretLength {
		return nil, errors.New("netsec: master secret must be 48 bytes")
	}
	if len(finishedHash) == 0 {
		return nil, errors.New("netsec: finished hash is required")
	}
	if length <= 0 {
		return nil, errors.New("netsec: verify_data length must be positive")
	}
	label := labelServerFinished
	if fromClient {
		label = labelClientFinished
	}
	return prf(masterSecret, label, finishedHash, length)
}

// Extension is one entry of a TLS hello extension list.
type Extension struct {
	Type uint16
	Data []byte
}

// HasExtension reports whether a parsed ClientHello extension list contains
// the extended_master_secret extension. Per RFC 7627 its extension_data MUST
// be empty; a non-empty payload is treated as a malformed hello.
func HasExtension(extensions []Extension) (bool, error) {
	for _, ext := range extensions {
		if ext.Type != ExtensionExtendedMasterSecret {
			continue
		}
		if len(ext.Data) != 0 {
			return false, errors.New("netsec: extended_master_secret extension_data must be empty")
		}
		return true, nil
	}
	return false, nil
}

// ClientOffersEMS scans the raw extension block of a ClientHello (the bytes
// following the two-byte extensions length field) and reports whether the
// extended_master_secret extension is present and well-formed.
func ClientOffersEMS(extensionBlock []byte) (bool, error) {
	rest := extensionBlock
	for len(rest) > 0 {
		if len(rest) < 4 {
			return false, errors.New("netsec: truncated extension header")
		}
		extType := binary.BigEndian.Uint16(rest[0:2])
		extLen := int(binary.BigEndian.Uint16(rest[2:4]))
		rest = rest[4:]
		if extLen > len(rest) {
			return false, errors.New("netsec: truncated extension data")
		}
		rest = rest[extLen:]

		if extType == ExtensionExtendedMasterSecret {
			if extLen != 0 {
				return false, errors.New("netsec: extended_master_secret extension_data must be empty")
			}
			return true, nil
		}
	}
	return false, nil
}
