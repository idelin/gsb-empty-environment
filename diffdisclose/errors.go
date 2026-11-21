package diffdisclose

import "errors"

var (
	errBadAddress        = errors.New("diffdisclose: malformed node address")
	errKeyOutOfRange     = errors.New("diffdisclose: key exceeds 48-bit space")
	errBadSalt           = errors.New("diffdisclose: leaf salt must be 32 bytes")
	errBadHeight         = errors.New("diffdisclose: tree height out of range")
	errDuplicateKey      = errors.New("diffdisclose: duplicate key in tree")
	errBadInterval       = errors.New("diffdisclose: invalid interval set")
	errRevocationAligned = errors.New("diffdisclose: revoked interval must be subtree-aligned")
	errRevocationScope   = errors.New("diffdisclose: revoked interval must lie inside authorized set")
	errBadManifest       = errors.New("diffdisclose: malformed manifest")
	errBadSignature      = errors.New("diffdisclose: manifest signature verification failed")
	errRootMismatch      = errors.New("diffdisclose: recomputed root does not match manifest")
	errBadCacheEntry     = errors.New("diffdisclose: unauthenticated or mismatched cache entry")
	errMalformedProof    = errors.New("diffdisclose: malformed proof encoding")
	errNonCanonical      = errors.New("diffdisclose: proof is not the unique minimal canonical sequence")
	errRecordUnnecessary = errors.New("diffdisclose: proof record fails necessity check")
	errUnderreport       = errors.New("diffdisclose: proof under-reports a change or deletion")
	errDigestHeight      = errors.New("diffdisclose: empty digest reused at a different height")
	errUnauthorizedEvent = errors.New("diffdisclose: event key lies outside authorized non-revoked scope")
)
