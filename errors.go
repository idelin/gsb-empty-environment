package sharevault

import "errors"

var (
	ErrInvalidCoordinate     = errors.New("sharevault: invalid x coordinate")
	ErrInvalidShareValue     = errors.New("sharevault: invalid share value")
	ErrInvalidSecret         = errors.New("sharevault: invalid secret")
	ErrInvalidThreshold      = errors.New("sharevault: invalid threshold")
	ErrNoShares              = errors.New("sharevault: no shares provided")
	ErrDuplicateX            = errors.New("sharevault: duplicate x coordinate")
	ErrGenerationMismatch    = errors.New("sharevault: generation mismatch")
	ErrUnknownParticipant    = errors.New("sharevault: unknown participant")
	ErrDuplicateContribution = errors.New("sharevault: duplicate contribution")
	ErrNonZeroConstant       = errors.New("sharevault: contribution polynomial has non-zero constant term")
	ErrInconsistentDeltas    = errors.New("sharevault: contribution deltas are inconsistent")
	ErrIncompleteRound       = errors.New("sharevault: refresh round is missing contributions")
	ErrMissingConfirmation   = errors.New("sharevault: refresh round is missing confirmations")
	ErrDigestMismatch        = errors.New("sharevault: round digest mismatch")
	ErrAlreadyCommitted      = errors.New("sharevault: refresh round already committed")
)
