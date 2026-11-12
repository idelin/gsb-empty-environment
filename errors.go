package netsec

import "errors"

var (
	// ErrDuplicateMember is returned when the outer tar contains more than
	// one entry with the same member name.
	ErrDuplicateMember = errors.New("gem: duplicate archive member")

	// ErrUnknownMember is returned when the outer tar contains a member that
	// is not part of the documented gem layout.
	ErrUnknownMember = errors.New("gem: unknown archive member")

	// ErrMalformedArchive is returned when the gem tar or one of its gzip
	// members cannot be parsed.
	ErrMalformedArchive = errors.New("gem: malformed archive")

	// ErrMissingMember is returned when a required gem member is absent.
	ErrMissingMember = errors.New("gem: required member missing")

	// ErrMalformedChecksums is returned when checksums.yaml cannot be parsed
	// or does not describe the expected members.
	ErrMalformedChecksums = errors.New("gem: malformed checksums.yaml")

	// ErrChecksumMismatch is returned when a SHA512 digest recorded in
	// checksums.yaml does not match the actual member bytes.
	ErrChecksumMismatch = errors.New("gem: checksum mismatch")

	// ErrPartialSignature is returned when only some of the signed members
	// carry a .sig counterpart. Stripping any single signature always fails.
	ErrPartialSignature = errors.New("gem: partial signature set")

	// ErrUnsigned is returned under HighSecurity when the gem carries no
	// signatures at all.
	ErrUnsigned = errors.New("gem: gem is not signed")

	// ErrMalformedSignature is returned when a .sig member is not a parseable
	// detached PKCS#7 SignedData structure.
	ErrMalformedSignature = errors.New("gem: malformed PKCS#7 signature")

	// ErrUnsupportedAlgorithm is returned when a signature or digest
	// algorithm other than RSA-SHA256 is used.
	ErrUnsupportedAlgorithm = errors.New("gem: unsupported signature algorithm")

	// ErrInvalidSignature is returned when a PKCS#7 signature does not
	// verify against its member content.
	ErrInvalidSignature = errors.New("gem: invalid signature")

	// ErrSignerMismatch is returned when the gem members were signed by
	// different signer certificates.
	ErrSignerMismatch = errors.New("gem: signatures use different signer certificates")

	// ErrUntrustedChain is returned when the signer certificate chain cannot
	// be validated, or under HighSecurity does not terminate at a trusted root.
	ErrUntrustedChain = errors.New("gem: certificate chain is not trusted")

	// ErrNoTrustRoots is returned when HighSecurity is selected without a
	// configured trust store.
	ErrNoTrustRoots = errors.New("gem: no trusted roots configured")
)
