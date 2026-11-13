package netsec

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
)

const (
	// BlockSize is the AES block size and the CBC IV size.
	BlockSize = aes.BlockSize
)

// Feedback is the complete set of answers an oracle callback may expose.
type Feedback uint8

const (
	// PaddingValid means the decrypted bytes ended in syntactically valid
	// PKCS#7 padding. PKCS#7 uses k trailing bytes equal to k, for 1 <= k <= 16.
	PaddingValid Feedback = iota + 1

	// PaddingInvalid means the decrypted bytes did not end in valid PKCS#7
	// padding. It does not reveal why the padding was rejected.
	PaddingInvalid

	// InfrastructureError means the callback could not produce a reliable
	// padding answer. Callers must not collapse it into PaddingInvalid.
	InfrastructureError

	// Rejected is the single, uniform answer for any request that failed
	// authentication in an encrypt-then-MAC callback.
	Rejected
)

// Compatibility aliases with explicit names.
const (
	FeedbackPaddingValid        = PaddingValid
	FeedbackPaddingInvalid      = PaddingInvalid
	FeedbackInfrastructureError = InfrastructureError
	FeedbackRejected            = Rejected
)

func (f Feedback) String() string {
	switch f {
	case PaddingValid:
		return "padding-valid"
	case PaddingInvalid:
		return "padding-invalid"
	case InfrastructureError:
		return "infrastructure-error"
	case Rejected:
		return "rejected"
	default:
		return "unknown-feedback"
	}
}

// DecryptCallback is the only interface available to the recovery algorithm.
// It accepts an IV and a ciphertext body, and exposes padding feedback only.
// Implementations must not return or log plaintext.
type DecryptCallback func(iv, ciphertext []byte) Feedback

var (
	// ErrInvalidKey indicates that a constructor received an unusable key.
	ErrInvalidKey = errors.New("invalid key")

	// ErrInvalidInput indicates malformed IV or ciphertext input.
	ErrInvalidInput = errors.New("invalid input")
)

// NewAESCBCPaddingOracle returns the intentionally vulnerable legacy callback
// used by the regression experiment. It accepts a 16-byte AES-128 key and
// reports whether decrypted PKCS#7 padding is valid.
//
// The callback returns InfrastructureError for structurally invalid IV or
// ciphertext input. It never returns decrypted bytes.
func NewAESCBCPaddingOracle(key []byte) (DecryptCallback, error) {
	if len(key) != 16 {
		return nil, ErrInvalidKey
	}

	keyCopy := make([]byte, len(key))
	copy(keyCopy, key)

	block, err := aes.NewCipher(keyCopy)
	if err != nil {
		return nil, ErrInvalidKey
	}

	return func(iv, ciphertext []byte) Feedback {
		return cbcPaddingFeedback(block, iv, ciphertext)
	}, nil
}

// NewAuthenticatedCBCPaddingOracle returns an encrypt-then-MAC callback.
//
// The authenticated IV and ciphertext are the target pair supplied by the
// test host. The callback computes HMAC-SHA256 over every requested
// IV||ciphertext and compares it, in constant time, with the tag for that
// authenticated pair. Any request that does not authenticate receives the
// same Rejected answer before input lengths are inspected and before AES
// decryption begins. Only an authenticated request can reach decryption and
// padding validation.
func NewAuthenticatedCBCPaddingOracle(encKey, macKey, authenticatedIV, authenticatedCiphertext []byte) (DecryptCallback, error) {
	if len(encKey) != 16 || len(macKey) == 0 {
		return nil, ErrInvalidKey
	}
	if err := validateCBCInput(authenticatedIV, authenticatedCiphertext); err != nil {
		return nil, err
	}

	encKeyCopy := make([]byte, len(encKey))
	copy(encKeyCopy, encKey)
	macKeyCopy := make([]byte, len(macKey))
	copy(macKeyCopy, macKey)

	block, err := aes.NewCipher(encKeyCopy)
	if err != nil {
		return nil, ErrInvalidKey
	}

	wantTag := cbcMAC(macKeyCopy, authenticatedIV, authenticatedCiphertext)

	return func(iv, ciphertext []byte) Feedback {
		gotTag := cbcMAC(macKeyCopy, iv, ciphertext)
		if !hmac.Equal(gotTag, wantTag) {
			return Rejected
		}

		if err := validateCBCInput(iv, ciphertext); err != nil {
			return Rejected
		}
		return cbcPaddingFeedback(block, iv, ciphertext)
	}, nil
}

func cbcMAC(key, iv, ciphertext []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(iv)
	_, _ = mac.Write(ciphertext)
	return mac.Sum(nil)
}

func validateCBCInput(iv, ciphertext []byte) error {
	if len(iv) != BlockSize {
		return ErrInvalidInput
	}
	if len(ciphertext) == 0 || len(ciphertext)%BlockSize != 0 {
		return ErrInvalidInput
	}
	return nil
}

func cbcPaddingFeedback(block cipher.Block, iv, ciphertext []byte) Feedback {
	if err := validateCBCInput(iv, ciphertext); err != nil {
		return InfrastructureError
	}

	ivCopy := make([]byte, len(iv))
	copy(ivCopy, iv)

	plaintext := make([]byte, len(ciphertext))
	mode := cipher.NewCBCDecrypter(block, ivCopy)
	mode.CryptBlocks(plaintext, ciphertext)

	valid := validPKCS7(plaintext)
	clear(plaintext)
	if valid {
		return PaddingValid
	}
	return PaddingInvalid
}

func validPKCS7(plaintext []byte) bool {
	if len(plaintext) == 0 || len(plaintext)%BlockSize != 0 {
		return false
	}

	padLen := int(plaintext[len(plaintext)-1])
	if padLen < 1 || padLen > BlockSize {
		return false
	}

	expected := make([]byte, padLen)
	for i := range expected {
		expected[i] = byte(padLen)
	}
	return subtle.ConstantTimeCompare(plaintext[len(plaintext)-padLen:], expected) == 1
}
