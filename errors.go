package netsec

import "errors"

var (
	ErrInvalidIdentity      = errors.New("netsec: invalid identity")
	ErrInvalidPoint         = errors.New("netsec: invalid or infinite P-256 point")
	ErrInvalidBundle        = errors.New("netsec: invalid prekey bundle")
	ErrBundleExpired        = errors.New("netsec: prekey bundle expired")
	ErrInvalidSignature     = errors.New("netsec: invalid Ed25519 signature")
	ErrDirectoryProof       = errors.New("netsec: directory proof rejected")
	ErrIdentityMismatch     = errors.New("netsec: key does not match registered target device")
	ErrInvalidTranscript    = errors.New("netsec: invalid transcript")
	ErrTranscriptMismatch   = errors.New("netsec: transcript does not match established context")
	ErrInvalidMessage       = errors.New("netsec: invalid initial message")
	ErrSenderMismatch       = errors.New("netsec: sender does not match registered initiator")
	ErrRecipientMismatch    = errors.New("netsec: recipient does not match this device")
	ErrUnknownSignedPreKey  = errors.New("netsec: unknown signed prekey ID")
	ErrUnknownOneTimePreKey = errors.New("netsec: unknown one-time prekey ID")
	ErrOneTimePreKeyUsed    = errors.New("netsec: one-time prekey already consumed")
	ErrHandshakeConflict    = errors.New("netsec: handshake ID reused with different content")
	ErrDecrypt              = errors.New("netsec: first message authentication failed")
)
