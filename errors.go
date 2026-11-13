package netsec

import "errors"

var (
	// ErrInvalidPacket indicates that an OpenPGP packet is malformed or
	// unsupported.
	ErrInvalidPacket = errors.New("netsec: invalid OpenPGP packet")

	// ErrUnsupportedAlgorithm indicates that the packet uses a public-key,
	// hash, or curve algorithm that this package cannot verify.
	ErrUnsupportedAlgorithm = errors.New("netsec: unsupported algorithm")

	// ErrBadSignature indicates that a signature did not verify.
	ErrBadSignature = errors.New("netsec: signature verification failed")

	// ErrNotDesignatedRevoker indicates that the signing key is neither the
	// target key itself nor a designated revoker of the target key.
	ErrNotDesignatedRevoker = errors.New("netsec: signer is not a designated revoker")

	// ErrWrongSignatureType indicates that the signature is not a key or
	// subkey revocation signature.
	ErrWrongSignatureType = errors.New("netsec: wrong signature type")

	// ErrKeyNotFound indicates that the requested key is absent from the
	// keyring.
	ErrKeyNotFound = errors.New("netsec: key not found")
)
