package tls13zerortt

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
)

var errUnknownPSKMode = errors.New("tls13zerortt: unknown PSK mode")

// PSKMode selects the binder_key label defined by RFC 8446 Section 7.1.
//
// External PSKs (provisioned out of band, Section 4.2.11) use
// "ext binder"; resumption PSKs carried in NewSessionTicket use
// "res binder". The two labels derive distinct binder keys even from an
// identical PSK, so the mode recorded on the ticket must be used.
type PSKMode uint8

const (
	// ResumptionPSK derives binder_key / finished_key via the
	// "res binder" / "res master" labels.
	ResumptionPSK PSKMode = iota
	// ExternalPSK derives binder_key via the "ext binder" label.
	ExternalPSK
)

func (m PSKMode) binderLabel() string {
	switch m {
	case ExternalPSK:
		return "ext binder"
	case ResumptionPSK:
		return "res binder"
	default:
		// Fail closed: an unknown mode must never silently match a label.
		return ""
	}
}

// KeySchedule holds the early-secret derivation outputs for one PSK
// (RFC 8446 Section 7.1, Figure with the SHA-256 hash):
//
//	early_secret    = HKDF-Extract(0, PSK)
//	binder_key      =
//	    Derive-Secret(early_secret, "ext binder" | "res binder", "")
//	finished_key    =
//	    HKDF-Expand-Label(binder_key, "finished", "", Hash.length)
//
// The empty transcript hash is SHA-256(""), matching a ClientHello that is
// the first handshake message.
type KeySchedule struct {
	PSK         []byte
	Mode        PSKMode
	EarlySecret []byte
	BinderKey   []byte
	FinishedKey []byte
}

// NewKeySchedule derives the complete early-secret key chain for psk.
//
// The PSK slice is consumed by HMAC only and is not retained beyond the
// returned struct; callers may zero it after construction. BinderKey and
// FinishedKey are freshly allocated and owned by the caller.
func NewKeySchedule(psk []byte, mode PSKMode) (*KeySchedule, error) {
	label := mode.binderLabel()
	if label == "" {
		return nil, errUnknownPSKMode
	}

	// early_secret = HKDF-Extract(salt = 0(Hash.len), IKM = PSK).
	zeroSalt := make([]byte, HashLength)
	earlySecret := hkdfExtract(zeroSalt, psk)

	// binder_key = Derive-Secret(early_secret, label, ""), i.e. the
	// transcript context is the hash of an empty message.
	binderKey, err := deriveSecret(earlySecret, label, nil)
	if err != nil {
		return nil, err
	}

	// finished_key = HKDF-Expand-Label(binder_key, "finished", "",
	//                                  Hash.length). The context is empty
	// (not a hash) for the finished key per Section 4.4.4.
	finishedKey, err := hkdfExpandLabel(binderKey, "finished", nil, HashLength)
	if err != nil {
		return nil, err
	}

	return &KeySchedule{
		PSK:         append([]byte(nil), psk...),
		Mode:        mode,
		EarlySecret: earlySecret,
		BinderKey:   binderKey,
		FinishedKey: finishedKey,
	}, nil
}

// binderMAC computes FinishedKey HMAC over transcriptHash(truncatedCH):
//
//	verify_data = HMAC(finished_key,
//	                   Transcript-Hash(TruncatedClientHello))
//
// as applied to PSK binders in Section 4.2.11.2.
func (ks *KeySchedule) binderMAC(truncatedClientHello []byte) []byte {
	mac := hmac.New(sha256.New, ks.FinishedKey)
	hash := transcriptHash(truncatedClientHello)
	mac.Write(hash)
	return mac.Sum(nil)
}
