package netsec

import "errors"

// Sentinel errors returned by the package. Callers should test them with
// errors.Is because returned errors add contextual detail.
var (
	ErrInvalidHeader             = errors.New("netsec: invalid header")
	ErrInvalidConsensus          = errors.New("netsec: invalid consensus signature")
	ErrInsufficientPower         = errors.New("netsec: insufficient consensus power")
	ErrUnknownValidatorSet       = errors.New("netsec: unknown validator set")
	ErrMissingParent             = errors.New("netsec: missing parent header")
	ErrBelowCheckpoint           = errors.New("netsec: header below trusted checkpoint")
	ErrFinalityViolation         = errors.New("netsec: finality violation")
	ErrFrozen                    = errors.New("netsec: light client frozen")
	ErrInvalidEvent              = errors.New("netsec: invalid event")
	ErrProofBinding              = errors.New("netsec: proof fields are not bound together")
	ErrDestinationMismatch       = errors.New("netsec: destination chain mismatch")
	ErrBridgeMismatch            = errors.New("netsec: bridge instance mismatch")
	ErrUnknownHeader             = errors.New("netsec: header is not known")
	ErrBlockNotCanonical         = errors.New("netsec: block is not canonical")
	ErrInsufficientConfirmations = errors.New("netsec: insufficient confirmations")
	ErrNotFinal                  = errors.New("netsec: block is not final")
	ErrInvalidMerkleProof        = errors.New("netsec: invalid Merkle proof")
	ErrInvalidTxProof            = errors.New("netsec: transaction is not included")
	ErrInvalidEventProof         = errors.New("netsec: event is not included")
	ErrForgedEvent               = errors.New("netsec: event does not match the committed event leaf")
	ErrAlreadyProcessed          = errors.New("netsec: message already processed")
	ErrStoreClosed               = errors.New("netsec: store is closed")
	ErrCorruptStore              = errors.New("netsec: corrupt replay store")
	ErrUnknownEventKind          = errors.New("netsec: unsupported event kind")
)
