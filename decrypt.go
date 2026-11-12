package netsec

// DecryptionResult is the outcome of one controller-key decryption attempt.
type DecryptionResult struct {
	Plaintext map[string][]byte
	Err       error
}

// DecryptionAttempt records the label used by one key and that key's result.
type DecryptionAttempt struct {
	Key    string
	Label  string
	Result DecryptionResult
}

// ControllerKey is one private key in the controller's rotation set.
type ControllerKey struct {
	Name string
	// Decrypt must decrypt with exactly the supplied label.
	Decrypt func(label string, encryptedData map[string]string) (map[string][]byte, error)
}

// LabelDeriver supplies the label that the current object requires.
type LabelDeriver interface {
	ExpectedLabel() (string, error)
}

// ValidateLabel succeeds only when label equals the label derived from the
// current object. A mismatch is reported as ErrDecryptionFailed.
func ValidateLabel(secret LabelDeriver, label string) error {
	expected, err := secret.ExpectedLabel()
	if err != nil {
		return err
	}
	if label != expected {
		return ErrDecryptionFailed
	}
	return nil
}

// ValidateLabel succeeds only when label equals the label derived from the
// current object. A mismatch is reported as ErrDecryptionFailed.
func (s SealedSecret) ValidateLabel(label string) error {
	return ValidateLabel(s, label)
}

// ValidateDecryption succeeds only when the attempt used the label derived from
// the current object and the decryption itself succeeded.
func ValidateDecryption(secret LabelDeriver, label string, result DecryptionResult) error {
	if err := ValidateLabel(secret, label); err != nil {
		return err
	}
	if result.Err != nil {
		return ErrDecryptionFailed
	}
	return nil
}

// ValidateDecryption succeeds only when the attempt used the label derived from
// the current object and the decryption itself succeeded.
func (s SealedSecret) ValidateDecryption(label string, result DecryptionResult) error {
	return ValidateDecryption(s, label, result)
}

// ValidateDecrypted is the plaintext-and-error form of ValidateDecryption.
func ValidateDecrypted(secret LabelDeriver, label string, plaintext map[string][]byte, decryptErr error) error {
	return ValidateDecryption(secret, label, DecryptionResult{Plaintext: plaintext, Err: decryptErr})
}

// ValidateDecrypted is the plaintext-and-error form of ValidateDecryption.
func (s SealedSecret) ValidateDecrypted(label string, plaintext map[string][]byte, decryptErr error) error {
	return ValidateDecrypted(s, label, plaintext, decryptErr)
}

// ValidateAttempts tries the results of multiple controller keys in order.
// Every failure, whether caused by a wrong label or by bad ciphertext, is
// reported as the same ErrDecryptionFailed value.
func ValidateAttempts(secret LabelDeriver, attempts []DecryptionAttempt) (map[string][]byte, error) {
	expected, err := secret.ExpectedLabel()
	if err != nil {
		return nil, err
	}
	for _, attempt := range attempts {
		if attempt.Result.Err != nil || attempt.Label != expected {
			continue
		}
		return attempt.Result.Plaintext, nil
	}
	return nil, ErrDecryptionFailed
}

// ValidateAttempts tries the results of multiple controller keys in order.
// Every failure, whether caused by a wrong label or by bad ciphertext, is
// reported as the same ErrDecryptionFailed value.
func (s SealedSecret) ValidateAttempts(attempts []DecryptionAttempt) (map[string][]byte, error) {
	return ValidateAttempts(s, attempts)
}

// Unseal tries each controller key in order, passing the label derived from the
// current object. It returns the first successful plaintext; otherwise it
// returns ErrDecryptionFailed without exposing any individual key's failure.
func (s SealedSecret) Unseal(keys ...ControllerKey) (map[string][]byte, error) {
	label, err := s.ExpectedLabel()
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		if key.Decrypt == nil {
			continue
		}
		plaintext, decryptErr := key.Decrypt(label, s.EncryptedData)
		if decryptErr != nil {
			continue
		}
		if s.ValidateDecryption(label, DecryptionResult{Plaintext: plaintext}) == nil {
			return plaintext, nil
		}
	}
	return nil, ErrDecryptionFailed
}

// Unsealer is implemented by SealedSecret and supports sequential key rotation.
type Unsealer interface {
	Unseal(...ControllerKey) (map[string][]byte, error)
}

// Unseal is the package-level form of SealedSecret.Unseal.
func Unseal(secret Unsealer, keys []ControllerKey) (map[string][]byte, error) {
	return secret.Unseal(keys...)
}
