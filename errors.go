package netsec

import "errors"

var (
	// ErrInvalidManifest reports a structurally invalid manifest.
	ErrInvalidManifest = errors.New("netsec: invalid attachment manifest")
	// ErrInvalidSignature reports that the sender signature did not verify.
	ErrInvalidSignature = errors.New("netsec: invalid manifest signature")
	// ErrUntrustedSender reports that no trusted key is available for the sender.
	ErrUntrustedSender = errors.New("netsec: untrusted sender device")
	// ErrRecipientNotFound reports that the manifest has no envelope for the
	// local recipient device.
	ErrRecipientNotFound = errors.New("netsec: recipient device not present")
	// ErrDuplicateRecipient reports that a recipient device identifier occurs
	// more than once.
	ErrDuplicateRecipient = errors.New("netsec: duplicate recipient device")
	// ErrMetadataMismatch reports that metadata differs from the expected or
	// signed metadata.
	ErrMetadataMismatch = errors.New("netsec: metadata mismatch")
	// ErrChunkDigestMismatch reports that a ciphertext chunk does not match its
	// signed SHA-256 digest.
	ErrChunkDigestMismatch = errors.New("netsec: chunk digest mismatch")
	// ErrChunkAuthentication reports an AEAD authentication failure.
	ErrChunkAuthentication = errors.New("netsec: chunk authentication failed")
	// ErrDuplicateChunk reports a repeated chunk index.
	ErrDuplicateChunk = errors.New("netsec: duplicate chunk index")
	// ErrMissingChunks reports that not every chunk was supplied.
	ErrMissingChunks = errors.New("netsec: missing attachment chunks")
	// ErrLengthMismatch reports a plaintext length that differs from the signed
	// total length or from the canonical chunk layout.
	ErrLengthMismatch = errors.New("netsec: attachment length mismatch")
	// ErrChunkOutOfRange reports a chunk index outside the manifest.
	ErrChunkOutOfRange = errors.New("netsec: chunk index out of range")
	// ErrReaderFinished reports an operation after successful finalization.
	ErrReaderFinished = errors.New("netsec: attachment reader already finished")
)
