package netsec

import "errors"

// Errors returned by this package. They are wrapped with context where useful,
// so callers should test them with errors.Is.
var (
	ErrInvalidLocation     = errors.New("netsec: invalid weight location")
	ErrInvalidInitializer  = errors.New("netsec: invalid initializer")
	ErrDuplicateTensorID   = errors.New("netsec: duplicate tensor id")
	ErrSymlink             = errors.New("netsec: symbolic link is not allowed")
	ErrNotRegular          = errors.New("netsec: weight entry is not a regular file")
	ErrNotDirectory        = errors.New("netsec: path component is not a directory")
	ErrHardlink            = errors.New("netsec: multiply-linked weight file is not allowed")
	ErrChangedDuringOpen   = errors.New("netsec: file changed while it was being opened")
	ErrOverlappingRanges   = errors.New("netsec: overlapping weight ranges are not allowed")
	ErrDigestMismatch      = errors.New("netsec: weight digest mismatch")
	ErrShortRead           = errors.New("netsec: short weight read")
	ErrInvalidTensorLength = errors.New("netsec: tensor length does not match dtype and shape")
	ErrUnsupportedDType    = errors.New("netsec: unsupported tensor dtype")
	ErrUnknownTensor       = errors.New("netsec: unknown tensor")
	ErrManifestMismatch    = errors.New("netsec: weight manifest mismatch")
	ErrModelInvalid        = errors.New("netsec: model instance is invalid")
	ErrModelClosed         = errors.New("netsec: model instance is closed")
)
