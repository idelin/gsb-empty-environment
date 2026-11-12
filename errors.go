package netsec

import "errors"

// ErrInvalidSignedData is returned when the DER supplied to VerifySignedData
// is not a well-formed CMS SignedData ContentInfo.
var ErrInvalidSignedData = errors.New("netsec: invalid CMS SignedData structure")

// ErrNoSigners is returned when the SignedData carries no SignerInfo.
var ErrNoSigners = errors.New("netsec: SignedData contains no signers")

// ErrMissingSignedAttribute is returned when signedAttrs does not contain a
// required attribute (content-type or message-digest), or when the
// attribute is present but has no value.
var ErrMissingSignedAttribute = errors.New("netsec: missing required signed attribute")

// ErrDuplicateSignedAttribute is returned when an attribute OID appears more
// than once in signedAttrs. RFC 5652 only defines single-valued
// content-type/message-digest attributes, so duplicates are rejected.
var ErrDuplicateSignedAttribute = errors.New("netsec: duplicate signed attribute")

// ErrContentTypeMismatch is returned when the content-type signed attribute
// does not match the encapsulated content type (eContentType).
var ErrContentTypeMismatch = errors.New("netsec: content-type attribute does not match encapsulated content type")

// ErrDigestMismatch is returned when the message-digest attribute does not
// equal the digest computed over the encapsulated content.
var ErrDigestMismatch = errors.New("netsec: message-digest attribute does not match content digest")

// ErrUnsupportedAlgorithm is returned for unsupported digest or signature
// algorithms, including legacy MD5/SHA-1.
var ErrUnsupportedAlgorithm = errors.New("netsec: unsupported algorithm")

// ErrCertificateNotFound is returned when no certificate matching a
// SignerInfo signer identifier is present in the SignedData certificate set.
var ErrCertificateNotFound = errors.New("netsec: signer certificate not found in SignedData")

// ErrSignature is returned when the cryptographic signature check fails.
var ErrSignature = errors.New("netsec: signature verification failed")

// ErrContentMismatch is returned when detached content is required or the
// supplied content disagrees with the encapsulated content.
var ErrContentMismatch = errors.New("netsec: encapsulated content mismatch")
