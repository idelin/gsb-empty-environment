package netsec

import "errors"

var (
	// ErrMalformedPayload indicates that the presented claims are not a JSON
	// object or contain a malformed reserved member.
	ErrMalformedPayload = errors.New("malformed SD-JWT payload")

	// ErrUnsupportedAlgorithm indicates that _sd_alg is not the required
	// "sha-256" value.
	ErrUnsupportedAlgorithm = errors.New("unsupported _sd_alg")

	// ErrMalformedSD indicates that an _sd member is not an array of strings.
	ErrMalformedSD = errors.New("malformed _sd array")

	// ErrMalformedDisclosure indicates that a disclosure is not a valid
	// base64url encoded [salt, name, value] or [salt, value] array.
	ErrMalformedDisclosure = errors.New("malformed disclosure")

	// ErrDuplicateDigest indicates that one digest occurs in more than one
	// disclosure slot. This includes a digest used as both a decoy and a real
	// disclosure.
	ErrDuplicateDigest = errors.New("digest occurs in more than one disclosure slot")

	// ErrDuplicateDisclosure indicates that two disclosures have the same
	// digest.
	ErrDuplicateDisclosure = errors.New("duplicate disclosure digest")

	// ErrUnmatchedDisclosure indicates that a disclosure does not match any
	// _sd entry in the payload.
	ErrUnmatchedDisclosure = errors.New("disclosure does not match any _sd entry")

	// ErrDisclosureKindMismatch indicates that an object-property disclosure
	// was used for an array element, or vice versa.
	ErrDisclosureKindMismatch = errors.New("disclosure kind does not match _sd slot")

	// ErrClaimConflict indicates that a disclosure would overwrite a claim
	// that is already present in the signed payload.
	ErrClaimConflict = errors.New("disclosure would overwrite an existing claim")

	// ErrReservedClaim indicates that a disclosure tries to create a reserved
	// member such as _sd or _sd_alg.
	ErrReservedClaim = errors.New("disclosure uses a reserved claim name")
)
