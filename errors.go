package netsec

import "errors"

var (
	ErrUnsupportedAlgorithm = errors.New("tpm: unsupported hash algorithm")
	ErrInvalidBinding       = errors.New("tpm: invalid policy-session binding")
	ErrInvalidCommand       = errors.New("tpm: invalid command template")
	ErrInvalidResponse      = errors.New("tpm: invalid response")
	ErrSessionBusy          = errors.New("tpm: policy session already has an active transaction")
	ErrSessionUnknown       = errors.New("tpm: prior command outcome is unknown; quote and audit before reuse")
	ErrSessionExpired       = errors.New("tpm: policy session has already been consumed")
	ErrGeneration           = errors.New("tpm: policy session handle generation changed")
	ErrStartupChanged       = errors.New("tpm: TPM startup count changed")
	ErrHandleInUse          = errors.New("tpm: policy session handle is already in use")
	ErrHandleMismatch       = errors.New("tpm: loaded session handle differs from expected handle")
	ErrPolicyORBranch       = errors.New("tpm: selected PolicyOR branch does not match current policy digest")
	ErrPolicyDigest         = errors.New("tpm: policy digest changed after the command was approved")
	ErrTemplateFrozen       = errors.New("tpm: command template is frozen; only authorization values may be filled")
	ErrAuthRequired         = errors.New("tpm: authorization values have not been supplied")
	ErrAlreadySubmitted     = errors.New("tpm: authorization was already submitted")
	ErrOutcomeUnknown       = errors.New("tpm: command outcome is unknown")
	ErrNoUnknownOutcome     = errors.New("tpm: session has no unknown command outcome")
	ErrRecoveryUnverified   = errors.New("tpm: quote/audit recovery proof was not verified")
	ErrBindingMismatch      = errors.New("tpm: restored context does not match the saved session binding")
	ErrUnregisteredContext  = errors.New("tpm: context was not saved by this authority")
)
