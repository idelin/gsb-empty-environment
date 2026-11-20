package prescreen

import "errors"

// Admission is fail-closed: every malformed, untrusted or unsatisfiable
// condition maps to one of these sentinel errors.
var (
	ErrMalformedMap     = errors.New("prescreen: malformed affine map")
	ErrMalformedRequest = errors.New("prescreen: malformed read request")
	ErrUntrustedRoot    = errors.New("prescreen: signature does not verify under a trusted root")
	ErrBadSignature     = errors.New("prescreen: ed25519 signature verification failed")
	ErrTenantMismatch   = errors.New("prescreen: capability tenant does not bind request")
	ErrActionMismatch   = errors.New("prescreen: capability action does not permit read")
	ErrObjectMismatch   = errors.New("prescreen: capability object binding mismatch")
	ErrViewNotFound     = errors.New("prescreen: view edge not found in manifest")
	ErrVersionMismatch  = errors.New("prescreen: view edge parent version mismatch")
	ErrViewCycle        = errors.New("prescreen: view chain contains a cycle")
	ErrTooManySlices    = errors.New("prescreen: more than MaxSlices subspaces for one object")
	ErrNotCovered       = errors.New("prescreen: physical set is not covered by valid grants")
	ErrRevoked          = errors.New("prescreen: physical set intersects a valid revocation slice")
	ErrCertificate      = errors.New("prescreen: algebraic certificate does not verify")
	ErrTicketMismatch   = errors.New("prescreen: ticket content differs from recomputed admission")
)
