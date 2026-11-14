package netsec

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

const (
	accountKeySize       = 32
	keyAgreementKeySize  = 32
	wrappingNonceSize    = 12
	wrappingVersionSize  = 4
	wrappingHeaderSize   = wrappingVersionSize + keyAgreementKeySize
	wrappingEnvelopeSize = wrappingHeaderSize + wrappingNonceSize + accountKeySize + 16
	wrappingInfo         = "netsec/account-key-wrap/v1"
	wrappingBindingLabel = "netsec/device-key-binding/v1"
)

var wrappingMagic = [4]byte{'N', 'W', 'K', '1'}

// WrapAccountKey encrypts accountKey for a device's X25519 public key.
// binding must identify the intended account, device, and enrollment; see
// DeviceKeyBinding. Supplying the same binding when unwrapping is required,
// which prevents an envelope from being transplanted to another device or
// account.
func WrapAccountKey(accountKey, recipientPublicKey, binding []byte) ([]byte, error) {
	if len(accountKey) != accountKeySize {
		return nil, fmt.Errorf("%w: account key must be %d bytes", ErrInvalidArgument, accountKeySize)
	}
	if len(recipientPublicKey) != keyAgreementKeySize {
		return nil, fmt.Errorf("%w: recipient key must be %d bytes", ErrInvalidArgument, keyAgreementKeySize)
	}
	if len(binding) == 0 {
		return nil, fmt.Errorf("%w: empty key binding", ErrInvalidArgument)
	}

	curve := ecdh.X25519()
	recipient, err := curve.NewPublicKey(recipientPublicKey)
	if err != nil {
		return nil, fmt.Errorf("%w: recipient key: %v", ErrInvalidArgument, err)
	}
	ephemeral, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("netsec: generate ephemeral key: %w", err)
	}
	ephemeralPublicKey := ephemeral.PublicKey().Bytes()

	shared, err := ephemeral.ECDH(recipient)
	if err != nil {
		return nil, fmt.Errorf("%w: key agreement: %v", ErrInvalidArgument, err)
	}
	defer wipe(shared)

	salt := sha256.Sum256(append(append([]byte(nil), ephemeralPublicKey...), recipientPublicKey...))
	derived, err := hkdf.Key(sha256.New, shared, salt[:], wrappingInfo+"\x00"+string(binding), 32)
	if err != nil {
		return nil, fmt.Errorf("netsec: derive wrapping key: %w", err)
	}
	defer wipe(derived)

	block, err := aes.NewCipher(derived)
	if err != nil {
		return nil, fmt.Errorf("netsec: create cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("netsec: create aead: %w", err)
	}

	nonce := make([]byte, wrappingNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("netsec: generate nonce: %w", err)
	}

	envelope := make([]byte, 0, wrappingEnvelopeSize)
	envelope = append(envelope, wrappingMagic[:]...)
	envelope = append(envelope, ephemeralPublicKey...)
	envelope = append(envelope, nonce...)
	sealed := aead.Seal(nil, nonce, accountKey, wrappingAAD(ephemeralPublicKey, binding))
	envelope = append(envelope, sealed...)
	return envelope, nil
}

// UnwrapAccountKey authenticates and decrypts an envelope produced by
// WrapAccountKey. recipientPrivateKey is the 32-byte X25519 private key
// belonging to the device for which the envelope was created.
func UnwrapAccountKey(envelope, recipientPrivateKey, binding []byte) ([]byte, error) {
	if len(envelope) != wrappingEnvelopeSize {
		return nil, fmt.Errorf("%w: wrong envelope length", ErrInvalidWrappedKey)
	}
	if len(recipientPrivateKey) != keyAgreementKeySize {
		return nil, fmt.Errorf("%w: private key must be %d bytes", ErrInvalidArgument, keyAgreementKeySize)
	}
	if len(binding) == 0 {
		return nil, fmt.Errorf("%w: empty key binding", ErrInvalidArgument)
	}
	if !bytes.Equal(envelope[:wrappingVersionSize], wrappingMagic[:]) {
		return nil, fmt.Errorf("%w: unknown envelope version", ErrInvalidWrappedKey)
	}

	ephemeralPublicKey := envelope[wrappingVersionSize:wrappingHeaderSize]
	nonce := envelope[wrappingHeaderSize : wrappingHeaderSize+wrappingNonceSize]
	ciphertext := envelope[wrappingHeaderSize+wrappingNonceSize:]

	curve := ecdh.X25519()
	recipient, err := curve.NewPrivateKey(recipientPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("%w: private key: %v", ErrInvalidArgument, err)
	}
	ephemeral, err := curve.NewPublicKey(ephemeralPublicKey)
	if err != nil {
		return nil, fmt.Errorf("%w: ephemeral key: %v", ErrInvalidWrappedKey, err)
	}
	shared, err := recipient.ECDH(ephemeral)
	if err != nil {
		return nil, fmt.Errorf("%w: key agreement: %v", ErrAccountKeyDecrypt, err)
	}
	defer wipe(shared)

	salt := sha256.Sum256(append(append([]byte(nil), ephemeralPublicKey...), recipient.PublicKey().Bytes()...))
	derived, err := hkdf.Key(sha256.New, shared, salt[:], wrappingInfo+"\x00"+string(binding), 32)
	if err != nil {
		return nil, fmt.Errorf("netsec: derive wrapping key: %w", err)
	}
	defer wipe(derived)

	block, err := aes.NewCipher(derived)
	if err != nil {
		return nil, fmt.Errorf("netsec: create cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("netsec: create aead: %w", err)
	}
	accountKey, err := aead.Open(nil, nonce, ciphertext, wrappingAAD(ephemeralPublicKey, binding))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAccountKeyDecrypt, err)
	}
	if len(accountKey) != accountKeySize {
		wipe(accountKey)
		return nil, fmt.Errorf("%w: decrypted key has wrong length", ErrAccountKeyDecrypt)
	}
	return accountKey, nil
}

// DeviceKeyBinding returns the canonical context that binds an wrapped
// account key to one account, device, and enrollment. Use the same value
// for WrapAccountKey and UnwrapAccountKey.
func DeviceKeyBinding(accountID AccountID, deviceID DeviceID, enrollmentID EnrollmentID) []byte {
	var buf bytes.Buffer
	buf.WriteString(wrappingBindingLabel)
	writeBindingField(&buf, string(accountID))
	writeBindingField(&buf, string(deviceID))
	writeBindingField(&buf, string(enrollmentID))
	return buf.Bytes()
}

// EnrollmentKeyBinding is an alias for DeviceKeyBinding.
func EnrollmentKeyBinding(accountID AccountID, deviceID DeviceID, enrollmentID EnrollmentID) []byte {
	return DeviceKeyBinding(accountID, deviceID, enrollmentID)
}

func writeBindingField(buf *bytes.Buffer, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	buf.Write(length[:])
	buf.WriteString(value)
}

func wrappingAAD(ephemeralPublicKey, binding []byte) []byte {
	aad := make([]byte, 0, wrappingVersionSize+len(ephemeralPublicKey)+8+len(binding))
	aad = append(aad, wrappingMagic[:]...)
	aad = append(aad, ephemeralPublicKey...)
	var bindingLen [8]byte
	binary.BigEndian.PutUint64(bindingLen[:], uint64(len(binding)))
	aad = append(aad, bindingLen[:]...)
	aad = append(aad, binding...)
	return aad
}

func validateWrappedKey(envelope []byte) error {
	if len(envelope) != wrappingEnvelopeSize {
		return ErrInvalidWrappedKey
	}
	if !bytes.Equal(envelope[:wrappingVersionSize], wrappingMagic[:]) {
		return ErrInvalidWrappedKey
	}
	curve := ecdh.X25519()
	if _, err := curve.NewPublicKey(envelope[wrappingVersionSize:wrappingHeaderSize]); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidWrappedKey, err)
	}
	return nil
}

func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
