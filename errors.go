package ipp

import "errors"

var (
	ErrAdapter             = errors.New("ipp: invalid group adapter")
	ErrN                   = errors.New("ipp: n must be a power of two in [1,1024]")
	ErrWitnessLength       = errors.New("ipp: witness length does not match n")
	ErrGeneratorLength     = errors.New("ipp: generator vector length does not match n")
	ErrMissingU            = errors.New("ipp: missing U generator")
	ErrIdentityGenerator   = errors.New("ipp: host generator must not be the identity")
	ErrInvalidScalar       = errors.New("ipp: non-canonical scalar")
	ErrZeroScalar          = errors.New("ipp: required non-zero scalar is zero")
	ErrInvalidPoint        = errors.New("ipp: invalid point encoding")
	ErrNonCanonicalPoint   = errors.New("ipp: non-canonical point encoding")
	ErrPointMismatch       = errors.New("ipp: point does not match canonical commitment")
	ErrWrongRoundCount     = errors.New("ipp: wrong number of folding rounds")
	ErrChallengeZero       = errors.New("ipp: transcript challenge derived as zero")
	ErrTranscript          = errors.New("ipp: transcript reconstruction failed")
	ErrContextMismatch     = errors.New("ipp: transcript context mismatch")
	ErrProofFormat         = errors.New("ipp: malformed proof")
	ErrTruncatedProof      = errors.New("ipp: truncated proof")
	ErrTrailingBytes       = errors.New("ipp: trailing bytes in proof")
	ErrVerificationFailed  = errors.New("ipp: inner-product proof verification failed")
	ErrWitnessCommitment   = errors.New("ipp: witness does not open commitment")
	ErrBatchEmpty          = errors.New("ipp: empty verification batch")
	ErrBatchWeights        = errors.New("ipp: insufficient unique batch weights")
	ErrDuplicateTranscript = errors.New("ipp: duplicate transcript in linear batch")
)
