package netsec

import "time"

// CredentialState is the relying party's persisted view of a credential.
//
// BackupEligible is recorded when the credential is registered and must never
// change. BackedUp may change as the authenticator's synchronization state
// changes. ReverificationRequired is set when a backed-up-to-not-backed-up
// transition was observed without fresh user verification.
type CredentialState struct {
	BackupEligible         bool
	BackedUp               bool
	ReverificationRequired bool
}

// RiskMarker describes the caller's risk assessment for an operation.
type RiskMarker uint8

const (
	// RiskLow is a normal operation.
	RiskLow RiskMarker = iota
	// RiskHigh requires a device-bound credential, or UV plus a recent second
	// factor for a syncable credential.
	RiskHigh
)

// SecondFactor is the caller's assertion that a second factor was verified
// recently. The package checks its presence and age; it does not verify the
// factor itself.
type SecondFactor struct {
	Verified   bool
	VerifiedAt time.Time
}

// AssertionInput contains the values supplied for one authentication assertion.
// A zero-value At means "now".
type AssertionInput struct {
	Flags        Flags
	Risk         RiskMarker
	SecondFactor SecondFactor
	At           time.Time
}

// WarningCode identifies a security event that should be written to an audit log.
type WarningCode string

const (
	// WarningBackedUpRegression records a credential moving from backed up to
	// not backed up.
	WarningBackedUpRegression WarningCode = "backed_up_regression"
)

// Warning is a security event produced while evaluating an assertion.
type Warning struct {
	Code    WarningCode
	Message string
	At      time.Time
}

// AssertionResult is returned for both accepted and rejected assertions.
// State is only advanced when the assertion is accepted, except that a
// rejected backed-up rollback sets ReverificationRequired. Warnings should be
// recorded even when an error is returned.
type AssertionResult struct {
	State       CredentialState
	Warnings    []Warning
	DeviceBound bool
}

// CheckRegistration validates registration authenticator-data flags and returns
// the state that must be persisted for later assertions.
func CheckRegistration(flags Flags) (CredentialState, error) {
	if flags.ReservedBits() != 0 {
		return CredentialState{}, newVerificationError(
			ErrorReservedBitsSet, ErrReservedBitsSet, flags.ReservedBits().String(),
		)
	}
	if !flags.Has(FlagUserPresent) {
		return CredentialState{}, newVerificationError(
			ErrorUserNotPresent, ErrUserNotPresent, "",
		)
	}
	if !flags.Has(FlagBackupEligible) && flags.Has(FlagBackedUp) {
		return CredentialState{}, newVerificationError(
			ErrorIllegalBackupState, ErrIllegalBackupState, "",
		)
	}
	if !flags.Has(FlagAttestedCredentialData) {
		return CredentialState{}, newVerificationError(
			ErrorAttestedCredentialDataMissing, ErrAttestedCredentialDataMissing, "",
		)
	}

	return CredentialState{
		BackupEligible: flags.Has(FlagBackupEligible),
		BackedUp:       flags.Has(FlagBackedUp),
	}, nil
}

// VerifyRegistration is an alias for CheckRegistration.
func VerifyRegistration(flags Flags) (CredentialState, error) {
	return CheckRegistration(flags)
}

// CheckAssertion validates one assertion against the persisted credential state.
//
// Fundamental flag checks are performed first. Backup eligibility must match the
// registration record. A backed-up-to-not-backed-up transition requires the
// assertion's UV bit and produces an audit warning. For high risk on a syncable
// credential, UV and a caller-attested second factor no older than five minutes
// are required.
func CheckAssertion(state CredentialState, input AssertionInput) (AssertionResult, error) {
	now := input.At
	if now.IsZero() {
		now = time.Now()
	}

	result := AssertionResult{
		State:       state,
		DeviceBound: !state.BackupEligible,
	}

	if input.Flags.ReservedBits() != 0 {
		return result, newVerificationError(
			ErrorReservedBitsSet, ErrReservedBitsSet, input.Flags.ReservedBits().String(),
		)
	}
	if !input.Flags.Has(FlagUserPresent) {
		return result, newVerificationError(
			ErrorUserNotPresent, ErrUserNotPresent, "",
		)
	}
	if !input.Flags.Has(FlagBackupEligible) && input.Flags.Has(FlagBackedUp) {
		return result, newVerificationError(
			ErrorIllegalBackupState, ErrIllegalBackupState, "",
		)
	}
	if !state.BackupEligible && state.BackedUp {
		return result, newVerificationError(
			ErrorIllegalBackupState, ErrIllegalBackupState, "persisted credential state",
		)
	}
	if input.Flags.Has(FlagAttestedCredentialData) {
		return result, newVerificationError(
			ErrorAttestedCredentialDataInAssertion, ErrAttestedCredentialDataInAssertion, "",
		)
	}
	if input.Flags.Has(FlagBackupEligible) != state.BackupEligible {
		return result, newVerificationError(
			ErrorBackupEligibilityChanged, ErrBackupEligibilityChanged,
			"registered="+boolString(state.BackupEligible)+" asserted="+boolString(input.Flags.Has(FlagBackupEligible)),
		)
	}
	if input.Risk != RiskLow && input.Risk != RiskHigh {
		return result, newVerificationError(
			ErrorInvalidRiskMarker, ErrInvalidRiskMarker, "",
		)
	}

	backupRegression := state.BackedUp && !input.Flags.Has(FlagBackedUp)
	needsReverification := backupRegression || state.ReverificationRequired
	if backupRegression {
		result.Warnings = append(result.Warnings, Warning{
			Code:    WarningBackedUpRegression,
			Message: "credential changed from backed up to not backed up",
			At:      now,
		})
	}
	if needsReverification && !input.Flags.Has(FlagUserVerified) {
		if backupRegression {
			result.State.ReverificationRequired = true
		}
		return result, newVerificationError(
			ErrorUserVerificationRequired, ErrUserVerificationRequired, "",
		)
	}

	if input.Risk == RiskHigh && state.BackupEligible {
		if !input.Flags.Has(FlagUserVerified) {
			return result, newVerificationError(
				ErrorHighRiskUserVerificationRequired, ErrHighRiskUserVerificationRequired, "",
			)
		}
		if !input.SecondFactor.Verified {
			return result, newVerificationError(
				ErrorHighRiskSecondFactorMissing, ErrHighRiskSecondFactorMissing, "",
			)
		}
		if input.SecondFactor.VerifiedAt.IsZero() ||
			now.Before(input.SecondFactor.VerifiedAt) ||
			now.Sub(input.SecondFactor.VerifiedAt) > SecondFactorMaxAge {
			return result, newVerificationError(
				ErrorHighRiskSecondFactorExpired, ErrHighRiskSecondFactorExpired, "",
			)
		}
	}

	result.State.ReverificationRequired = false
	if backupRegression {
		result.State.BackedUp = false
	} else if !state.BackedUp && input.Flags.Has(FlagBackedUp) {
		result.State.BackedUp = true
	}
	return result, nil
}

// VerifyAssertion is a convenience wrapper for CheckAssertion.
func VerifyAssertion(
	state CredentialState,
	flags Flags,
	risk RiskMarker,
	secondFactor SecondFactor,
	at time.Time,
) (AssertionResult, error) {
	return CheckAssertion(state, AssertionInput{
		Flags:        flags,
		Risk:         risk,
		SecondFactor: secondFactor,
		At:           at,
	})
}

// CheckInput combines the registration flags, current credential state, and
// assertion values for a single end-to-end check.
type CheckInput struct {
	RegistrationFlags Flags
	AssertionFlags    Flags
	State             CredentialState
	Risk              RiskMarker
	SecondFactor      SecondFactor
	At                time.Time
}

// Check validates the registration flags, checks that the recorded backup
// eligibility still agrees with them, and then validates the assertion.
func Check(input CheckInput) (AssertionResult, error) {
	registered, err := CheckRegistration(input.RegistrationFlags)
	if err != nil {
		return AssertionResult{State: input.State}, err
	}
	if registered.BackupEligible != input.State.BackupEligible {
		return AssertionResult{
				State:       input.State,
				DeviceBound: !input.State.BackupEligible,
			}, newVerificationError(
				ErrorBackupEligibilityChanged, ErrBackupEligibilityChanged,
				"registered flag and persisted credential state disagree",
			)
	}
	return CheckAssertion(input.State, AssertionInput{
		Flags:        input.AssertionFlags,
		Risk:         input.Risk,
		SecondFactor: input.SecondFactor,
		At:           input.At,
	})
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
