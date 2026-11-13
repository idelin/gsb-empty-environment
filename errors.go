package netsec

import (
	"errors"
	"fmt"
	"time"
)

// ErrorCode identifies a rejection reason. It is stable for callers that
// prefer structured handling over matching error text.
type ErrorCode string

const (
	ErrorReservedBitsSet                   ErrorCode = "reserved_bits_set"
	ErrorUserNotPresent                    ErrorCode = "user_not_present"
	ErrorIllegalBackupState                ErrorCode = "illegal_backup_state"
	ErrorAttestedCredentialDataMissing     ErrorCode = "attested_credential_data_missing"
	ErrorAttestedCredentialDataInAssertion ErrorCode = "attested_credential_data_in_assertion"
	ErrorBackupEligibilityChanged          ErrorCode = "backup_eligibility_changed"
	ErrorUserVerificationRequired          ErrorCode = "user_verification_required"
	ErrorHighRiskUserVerificationRequired  ErrorCode = "high_risk_user_verification_required"
	ErrorHighRiskSecondFactorMissing       ErrorCode = "high_risk_second_factor_missing"
	ErrorHighRiskSecondFactorExpired       ErrorCode = "high_risk_second_factor_expired"
	ErrorInvalidRiskMarker                 ErrorCode = "invalid_risk_marker"
)

var (
	// ErrReservedBitsSet is returned when either reserved flag bit is set.
	ErrReservedBitsSet = errors.New("reserved authentication-data flag bit is set")
	// ErrUserNotPresent is returned when the user-presence flag is clear.
	ErrUserNotPresent = errors.New("user-presence flag is not set")
	// ErrIllegalBackupState is returned for backup-eligible=false, backed-up=true.
	ErrIllegalBackupState = errors.New("backup-eligible flag is clear while backed-up flag is set")
	// ErrAttestedCredentialDataMissing is returned when registration lacks the ACD flag.
	ErrAttestedCredentialDataMissing = errors.New("attested credential data flag is missing at registration")
	// ErrAttestedCredentialDataInAssertion is returned when an assertion carries the ACD flag.
	ErrAttestedCredentialDataInAssertion = errors.New("attested credential data flag is set in an assertion")
	// ErrBackupEligibilityChanged is returned when the backup-eligible bit changes.
	ErrBackupEligibilityChanged = errors.New("backup-eligibility flag changed after registration")
	// ErrUserVerificationRequired is returned when a backed-up rollback needs fresh UV.
	ErrUserVerificationRequired = errors.New("fresh user verification is required after backup rollback")
	// ErrHighRiskUserVerificationRequired is returned for a syncable credential at high risk
	// without user verification.
	ErrHighRiskUserVerificationRequired = errors.New("high-risk operation on a syncable credential requires user verification")
	// ErrHighRiskSecondFactorMissing is returned when the caller did not attest a second factor.
	ErrHighRiskSecondFactorMissing = errors.New("high-risk operation on a syncable credential requires an attested second factor")
	// ErrHighRiskSecondFactorExpired is returned when the second factor is older than five minutes.
	ErrHighRiskSecondFactorExpired = errors.New("high-risk second factor is outside the five-minute validity window")
	// ErrInvalidRiskMarker is returned when the caller supplies an unknown risk marker.
	ErrInvalidRiskMarker = errors.New("invalid caller risk marker")
)

// SecondFactorMaxAge is the maximum age of the caller-attested second factor.
const SecondFactorMaxAge = 5 * time.Minute

// VerificationError carries a stable rejection code while remaining compatible
// with errors.Is against the exported sentinel errors.
type VerificationError struct {
	Code    ErrorCode
	Message string
	cause   error
}

func (e *VerificationError) Error() string {
	return e.Message
}

func (e *VerificationError) Unwrap() error {
	return e.cause
}

// CodeOf returns the stable rejection code carried by err, if any.
func CodeOf(err error) (ErrorCode, bool) {
	var verificationError *VerificationError
	if errors.As(err, &verificationError) {
		return verificationError.Code, true
	}
	return "", false
}

func newVerificationError(code ErrorCode, cause error, detail string) error {
	message := cause.Error()
	if detail != "" {
		message = fmt.Sprintf("%s: %s", message, detail)
	}
	return &VerificationError{Code: code, Message: message, cause: cause}
}
