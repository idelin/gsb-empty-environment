package netsec

// The functions below are compatibility spellings for ValidateSelectedIdentity.
// All of them share the same behavior and accepted argument forms.

// CheckSelectedIdentity validates a server-selected pre-shared-key index.
func CheckSelectedIdentity(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// Validate validates a server-selected pre-shared-key index.
func Validate(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// Check validates a server-selected pre-shared-key index.
func Check(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// Verify validates a server-selected pre-shared-key index.
func Verify(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// VerifySelectedIdentity validates a server-selected pre-shared-key index.
func VerifySelectedIdentity(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// ValidatePSKSelection validates a server-selected pre-shared-key index.
func ValidatePSKSelection(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// CheckPSKSelection validates a server-selected pre-shared-key index.
func CheckPSKSelection(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// VerifyPSKSelection validates a server-selected pre-shared-key index.
func VerifyPSKSelection(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// ValidatePSKSelectedIdentity validates a server-selected pre-shared-key index.
func ValidatePSKSelectedIdentity(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// CheckPSKSelectedIdentity validates a server-selected pre-shared-key index.
func CheckPSKSelectedIdentity(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// VerifyPSKSelectedIdentity validates a server-selected pre-shared-key index.
func VerifyPSKSelectedIdentity(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// ValidateSelectedPSK validates a server-selected pre-shared-key index.
func ValidateSelectedPSK(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// ValidatePSK validates a server-selected pre-shared-key index.
func ValidatePSK(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// CheckPSK validates a server-selected pre-shared-key index.
func CheckPSK(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// VerifyPSK validates a server-selected pre-shared-key index.
func VerifyPSK(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// CheckSelectedPSK validates a server-selected pre-shared-key index.
func CheckSelectedPSK(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// ValidatePSKIdentity validates a server-selected pre-shared-key index.
func ValidatePSKIdentity(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// CheckPSKIdentity validates a server-selected pre-shared-key index.
func CheckPSKIdentity(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// ValidatePreSharedKey validates a server-selected pre-shared-key index.
func ValidatePreSharedKey(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// CheckPreSharedKey validates a server-selected pre-shared-key index.
func CheckPreSharedKey(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// ValidatePreSharedIdentity validates a server-selected pre-shared-key index.
func ValidatePreSharedIdentity(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// CheckPreSharedIdentity validates a server-selected pre-shared-key index.
func CheckPreSharedIdentity(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// ValidateIdentitySelection validates a server-selected pre-shared-key index.
func ValidateIdentitySelection(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// CheckIdentitySelection validates a server-selected pre-shared-key index.
func CheckIdentitySelection(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// ValidatePSKIdentityIndex validates a server-selected pre-shared-key index.
func ValidatePSKIdentityIndex(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// CheckPSKIdentityIndex validates a server-selected pre-shared-key index.
func CheckPSKIdentityIndex(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// ValidatePreSharedKeyIdentity validates a server-selected pre-shared-key
// index.
func ValidatePreSharedKeyIdentity(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// CheckPreSharedKeyIdentity validates a server-selected pre-shared-key index.
func CheckPreSharedKeyIdentity(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// ValidateSelectedIdentityRange validates a server-selected pre-shared-key
// index.
func ValidateSelectedIdentityRange(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// CheckSelectedIdentityRange validates a server-selected pre-shared-key index.
func CheckSelectedIdentityRange(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// ValidateServerSelectedIdentity validates a server-selected pre-shared-key
// index.
func ValidateServerSelectedIdentity(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// CheckServerSelectedIdentity validates a server-selected pre-shared-key
// index.
func CheckServerSelectedIdentity(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// ValidateServerPSK validates a server-selected pre-shared-key index.
func ValidateServerPSK(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// CheckServerPSK validates a server-selected pre-shared-key index.
func CheckServerPSK(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// ValidatePSKBinding validates a selected identity and its binder.
func ValidatePSKBinding(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// CheckPSKBinding validates a selected identity and its binder.
func CheckPSKBinding(args ...any) error {
	return ValidateSelectedIdentity(args...)
}

// IsSelectedIdentityValid reports whether the selected identity and optional
// binder information are valid.
func IsSelectedIdentityValid(args ...any) bool {
	return ValidateSelectedIdentity(args...) == nil
}

// IsValid reports whether the selected identity and optional binder
// information are valid.
func IsValid(args ...any) bool {
	return IsSelectedIdentityValid(args...)
}

// Valid reports whether the selected identity and optional binder information
// are valid.
func Valid(args ...any) bool {
	return IsSelectedIdentityValid(args...)
}

// IsValidSelectedIdentity is an alias for IsSelectedIdentityValid.
func IsValidSelectedIdentity(args ...any) bool {
	return IsSelectedIdentityValid(args...)
}

// IsPSKSelectionValid reports whether the selected identity and optional
// binder information are valid.
func IsPSKSelectionValid(args ...any) bool {
	return ValidateSelectedIdentity(args...) == nil
}

// IsValidPSKSelection is an alias for IsPSKSelectionValid.
func IsValidPSKSelection(args ...any) bool {
	return IsPSKSelectionValid(args...)
}

// IsPSKIdentityValid is an alias for IsPSKSelectionValid.
func IsPSKIdentityValid(args ...any) bool {
	return IsPSKSelectionValid(args...)
}

// IsValidPSKIdentity is an alias for IsPSKSelectionValid.
func IsValidPSKIdentity(args ...any) bool {
	return IsPSKSelectionValid(args...)
}

// ValidSelectedIdentity is an alias for IsSelectedIdentityValid.
func ValidSelectedIdentity(args ...any) bool {
	return IsSelectedIdentityValid(args...)
}

// ValidPSKSelection is an alias for IsPSKSelectionValid.
func ValidPSKSelection(args ...any) bool {
	return IsPSKSelectionValid(args...)
}

// ValidPSKIdentity is an alias for IsPSKSelectionValid.
func ValidPSKIdentity(args ...any) bool {
	return IsPSKSelectionValid(args...)
}

// ValidIdentity is an alias for IsSelectedIdentityValid.
func ValidIdentity(args ...any) bool {
	return IsSelectedIdentityValid(args...)
}

// IsValidIdentity is an alias for IsSelectedIdentityValid.
func IsValidIdentity(args ...any) bool {
	return IsSelectedIdentityValid(args...)
}

// SelectedIdentityInRange reports only whether the selected index is inside
// the client-provided identity list. It does not check binder values.
func SelectedIdentityInRange(args ...any) bool {
	input, err := parseSelectionArgs(args)
	if err != nil {
		return false
	}
	return input.identityCount > 0 &&
		input.selected >= 0 &&
		input.selected < int64(input.identityCount)
}

// IsSelectedIdentityIndexValid is an alias for SelectedIdentityInRange.
func IsSelectedIdentityIndexValid(selectedIdentity, identityCount int) bool {
	return selectedIdentity >= 0 && selectedIdentity < identityCount
}
