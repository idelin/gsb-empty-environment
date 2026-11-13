package netsec

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidConfig      = errors.New("invalid admission configuration")
	ErrInvalidManifest    = errors.New("invalid adapter manifest")
	ErrInvalidSignature   = errors.New("adapter signature verification failed")
	ErrBaseDigestMismatch = errors.New("adapter base model digest does not exactly match the host base model")
	ErrDuplicateLayer     = errors.New("duplicate adapter target layer")
	ErrLayerNotFound      = errors.New("adapter target layer does not uniquely exist in the base model")
	ErrRankOutOfRange     = errors.New("adapter rank is outside the allowed range")
	ErrShapeMismatch      = errors.New("adapter tensor shape does not match the target layer")
	ErrDTypeMismatch      = errors.New("adapter tensor dtype does not match the signed dtype")
	ErrDTypeNotAllowed    = errors.New("adapter tensor dtype is not allowed")
	ErrScaleNotReduced    = errors.New("adapter scale is not a reduced rational")
	ErrScaleOutOfRange    = errors.New("adapter scale numerator or denominator is out of range")
	ErrSecurityCritical   = errors.New("adapter is not authorized for a security-critical layer")
	ErrDigestMismatch     = errors.New("tensor bytes do not match the signed digest")
	ErrTensorLength       = errors.New("tensor byte length does not match its dtype and shape")
	ErrNonFinite          = errors.New("tensor contains a non-finite floating-point element")
	ErrTensorRead         = errors.New("tensor could not be read")
	ErrTensorTooLarge     = errors.New("tensor exceeds the configured byte limit")
	ErrRevoked            = errors.New("adapter has been revoked")
	ErrNoGeneration       = errors.New("no adapter generation is available")
	ErrClosed             = errors.New("admission manager is closed")
)

// AdmissionError identifies the adapter target that failed admission.
type AdmissionError struct {
	LayerID string
	Err     error
}

func (e *AdmissionError) Error() string {
	if e.LayerID == "" {
		return e.Err.Error()
	}
	return fmt.Sprintf("layer %q: %v", e.LayerID, e.Err)
}

// Unwrap exposes the underlying sentinel error to errors.Is.
func (e *AdmissionError) Unwrap() error {
	return e.Err
}

func layerError(layerID string, err error) error {
	if layerID == "" {
		return err
	}
	return &AdmissionError{LayerID: layerID, Err: err}
}
