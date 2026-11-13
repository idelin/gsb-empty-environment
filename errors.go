package netsec

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidProof = errors.New("invalid key-proof JWT")

	ErrUnsupportedAlgorithm = fmt.Errorf("%w: unsupported JWT signature algorithm", ErrInvalidProof)

	ErrMismatchedConfirmationKey = errors.New("proof key does not match credential cnf.jkt")
)
