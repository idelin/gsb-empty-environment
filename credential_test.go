package netsec_test

import (
	"errors"
	"testing"
	"time"

	netsec "example.org/netsec/q0791"
)

func requireError(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("got error %v, want %v", err, target)
	}
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRegistrationRules(t *testing.T) {
	tests := []struct {
		name    string
		flags   netsec.Flags
		wantBE  bool
		wantBU  bool
		wantErr error
	}{
		{
			name:  "device bound",
			flags: 0x41,
		},
		{
			name:   "backup eligible",
			flags:  0x49,
			wantBE: true,
		},
		{
			name:   "backed up",
			flags:  0x59,
			wantBE: true,
			wantBU: true,
		},
		{
			name:    "extension data is allowed",
			flags:   0xc1,
			wantErr: nil,
		},
		{
			name:    "attested credential data required",
			flags:   0x01,
			wantErr: netsec.ErrAttestedCredentialDataMissing,
		},
		{
			name:    "user presence required",
			flags:   0x40,
			wantErr: netsec.ErrUserNotPresent,
		},
		{
			name:    "illegal backup combination",
			flags:   0x51,
			wantErr: netsec.ErrIllegalBackupState,
		},
		{
			name:    "reserved bit 1",
			flags:   0x43,
			wantErr: netsec.ErrReservedBitsSet,
		},
		{
			name:    "reserved bit 2",
			flags:   0x61,
			wantErr: netsec.ErrReservedBitsSet,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, err := netsec.CheckRegistration(test.flags)
			if test.wantErr != nil {
				requireError(t, err, test.wantErr)
				if state != (netsec.CredentialState{}) {
					t.Fatalf("state must not be recorded on failure: %+v", state)
				}
				return
			}
			requireNoError(t, err)
			if state.BackupEligible != test.wantBE {
				t.Fatalf("BackupEligible = %v, want %v", state.BackupEligible, test.wantBE)
			}
			if state.BackedUp != test.wantBU {
				t.Fatalf("BackedUp = %v, want %v", state.BackedUp, test.wantBU)
			}
		})
	}
}

func TestAssertionFlagRules(t *testing.T) {
	syncable := netsec.CredentialState{BackupEligible: true}
	deviceBound := netsec.CredentialState{}

	tests := []struct {
		name    string
		state   netsec.CredentialState
		flags   netsec.Flags
		wantErr error
	}{
		{
			name:    "reserved bit 1",
			state:   deviceBound,
			flags:   0x03,
			wantErr: netsec.ErrReservedBitsSet,
		},
		{
			name:    "reserved bit 2",
			state:   deviceBound,
			flags:   0x21,
			wantErr: netsec.ErrReservedBitsSet,
		},
		{
			name:    "user presence required",
			state:   deviceBound,
			flags:   0x80,
			wantErr: netsec.ErrUserNotPresent,
		},
		{
			name:    "illegal backup combination",
			state:   deviceBound,
			flags:   0x11,
			wantErr: netsec.ErrIllegalBackupState,
		},
		{
			name:    "illegal persisted state",
			state:   netsec.CredentialState{BackedUp: true},
			flags:   0x01,
			wantErr: netsec.ErrIllegalBackupState,
		},
		{
			name:    "attested credential data forbidden",
			state:   deviceBound,
			flags:   0x41,
			wantErr: netsec.ErrAttestedCredentialDataInAssertion,
		},
		{
			name:    "backup eligibility cannot change",
			state:   syncable,
			flags:   0x01,
			wantErr: netsec.ErrBackupEligibilityChanged,
		},
		{
			name:    "backup eligibility cannot be invented",
			state:   deviceBound,
			flags:   0x09,
			wantErr: netsec.ErrBackupEligibilityChanged,
		},
		{
			name:  "extension data is allowed",
			state: deviceBound,
			flags: 0x81,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := netsec.CheckAssertion(test.state, netsec.AssertionInput{
				Flags: test.flags,
			})
			if test.wantErr != nil {
				requireError(t, err, test.wantErr)
				return
			}
			requireNoError(t, err)
		})
	}
}

func TestBackedUpTransitions(t *testing.T) {
	state := netsec.CredentialState{BackupEligible: true}

	result, err := netsec.CheckAssertion(state, netsec.AssertionInput{
		Flags: 0x09,
	})
	requireNoError(t, err)
	if result.State.BackedUp {
		t.Fatal("0 to 0 transition changed BackedUp")
	}

	result, err = netsec.CheckAssertion(result.State, netsec.AssertionInput{
		Flags: 0x19,
	})
	requireNoError(t, err)
	if !result.State.BackedUp {
		t.Fatal("0 to 1 transition did not update BackedUp")
	}

	state = result.State
	result, err = netsec.CheckAssertion(state, netsec.AssertionInput{
		Flags: 0x09,
	})
	requireError(t, err, netsec.ErrUserVerificationRequired)
	if !result.State.BackedUp {
		t.Fatal("rejected rollback must retain BackedUp=true")
	}
	if !result.State.ReverificationRequired {
		t.Fatal("rejected rollback must require re-verification")
	}
	if len(result.Warnings) != 1 || result.Warnings[0].Code != netsec.WarningBackedUpRegression {
		t.Fatalf("missing rollback warning: %+v", result.Warnings)
	}

	result, err = netsec.CheckAssertion(result.State, netsec.AssertionInput{
		Flags: 0x0d,
	})
	requireNoError(t, err)
	if result.State.BackedUp {
		t.Fatal("verified rollback did not update BackedUp")
	}
	if result.State.ReverificationRequired {
		t.Fatal("verified rollback did not clear re-verification requirement")
	}
	if len(result.Warnings) != 1 || result.Warnings[0].Code != netsec.WarningBackedUpRegression {
		t.Fatalf("missing rollback warning: %+v", result.Warnings)
	}
}

func TestBackedUpRollbackWithUV(t *testing.T) {
	state := netsec.CredentialState{
		BackupEligible: true,
		BackedUp:       true,
	}

	result, err := netsec.CheckAssertion(state, netsec.AssertionInput{
		Flags: 0x0d,
	})
	requireNoError(t, err)
	if result.State.BackedUp {
		t.Fatal("1 to 0 transition did not update BackedUp")
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("want one rollback warning, got %+v", result.Warnings)
	}
	if result.Warnings[0].Code != netsec.WarningBackedUpRegression {
		t.Fatalf("unexpected warning code %q", result.Warnings[0].Code)
	}
}

func TestHighRiskDeviceBoundCredential(t *testing.T) {
	state, err := netsec.CheckRegistration(0x41)
	requireNoError(t, err)

	result, err := netsec.CheckAssertion(state, netsec.AssertionInput{
		Flags: 0x01,
		Risk:  netsec.RiskHigh,
	})
	requireNoError(t, err)
	if !result.DeviceBound {
		t.Fatal("device-bound credential was not reported as device bound")
	}
}

func TestHighRiskSyncableCredential(t *testing.T) {
	state, err := netsec.CheckRegistration(0x49)
	requireNoError(t, err)

	now := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	validSecondFactor := netsec.SecondFactor{
		Verified:   true,
		VerifiedAt: now.Add(-4 * time.Minute),
	}

	tests := []struct {
		name         string
		flags        netsec.Flags
		secondFactor netsec.SecondFactor
		at           time.Time
		wantErr      error
	}{
		{
			name:         "requires UV",
			flags:        0x09,
			secondFactor: validSecondFactor,
			wantErr:      netsec.ErrHighRiskUserVerificationRequired,
		},
		{
			name:    "requires second factor",
			flags:   0x0d,
			wantErr: netsec.ErrHighRiskSecondFactorMissing,
		},
		{
			name:  "valid UV and second factor",
			flags: 0x0d,
			secondFactor: netsec.SecondFactor{
				Verified:   true,
				VerifiedAt: now.Add(-2 * time.Minute),
			},
		},
		{
			name:  "five minutes is still valid",
			flags: 0x0d,
			secondFactor: netsec.SecondFactor{
				Verified:   true,
				VerifiedAt: now.Add(-netsec.SecondFactorMaxAge),
			},
		},
		{
			name:  "older than five minutes",
			flags: 0x0d,
			secondFactor: netsec.SecondFactor{
				Verified:   true,
				VerifiedAt: now.Add(-netsec.SecondFactorMaxAge - time.Second),
			},
			wantErr: netsec.ErrHighRiskSecondFactorExpired,
		},
		{
			name:  "future timestamp",
			flags: 0x0d,
			secondFactor: netsec.SecondFactor{
				Verified:   true,
				VerifiedAt: now.Add(time.Second),
			},
			wantErr: netsec.ErrHighRiskSecondFactorExpired,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			at := test.at
			if at.IsZero() {
				at = now
			}
			_, err := netsec.CheckAssertion(state, netsec.AssertionInput{
				Flags:        test.flags,
				Risk:         netsec.RiskHigh,
				SecondFactor: test.secondFactor,
				At:           at,
			})
			if test.wantErr != nil {
				requireError(t, err, test.wantErr)
				return
			}
			requireNoError(t, err)
		})
	}
}

func TestNormalRiskDoesNotRequireSecondFactor(t *testing.T) {
	state, err := netsec.CheckRegistration(0x49)
	requireNoError(t, err)

	_, err = netsec.CheckAssertion(state, netsec.AssertionInput{
		Flags: 0x09,
		Risk:  netsec.RiskLow,
	})
	requireNoError(t, err)
}

func TestOneShotCheck(t *testing.T) {
	now := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)

	_, err := netsec.Check(netsec.CheckInput{
		RegistrationFlags: 0x49,
		AssertionFlags:    0x0d,
		State:             netsec.CredentialState{BackupEligible: true},
		Risk:              netsec.RiskHigh,
		SecondFactor: netsec.SecondFactor{
			Verified:   true,
			VerifiedAt: now.Add(-2 * time.Minute),
		},
		At: now,
	})
	requireNoError(t, err)

	_, err = netsec.Check(netsec.CheckInput{
		RegistrationFlags: 0x49,
		AssertionFlags:    0x01,
		State:             netsec.CredentialState{},
		Risk:              netsec.RiskLow,
	})
	requireError(t, err, netsec.ErrBackupEligibilityChanged)

	_, err = netsec.Check(netsec.CheckInput{
		RegistrationFlags: 0x43,
		AssertionFlags:    0x05,
		State:             netsec.CredentialState{},
		Risk:              netsec.RiskLow,
	})
	requireError(t, err, netsec.ErrReservedBitsSet)
}

func TestStableErrorCodes(t *testing.T) {
	_, err := netsec.CheckRegistration(0x43)
	code, ok := netsec.CodeOf(err)
	if !ok {
		t.Fatalf("missing error code for %v", err)
	}
	if code != netsec.ErrorReservedBitsSet {
		t.Fatalf("code = %q, want %q", code, netsec.ErrorReservedBitsSet)
	}
}
