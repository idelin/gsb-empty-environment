package netsec

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	messageMagic   = "NQ0130"
	messageVersion = byte(1)

	modeSharedKey = byte(1)
	modeECDH      = byte(2)

	nonceSize  = 12
	gcmTagSize = 16

	headerPrefix          = 10
	minimumCiphertextSize = headerPrefix + nonceSize + gcmTagSize
)

var (
	// ErrAuthentication is returned when a message cannot be authenticated.
	// A wrong party order, a wrong key, or modified ciphertext all lead here.
	ErrAuthentication = errors.New("netsec: authentication failed")

	// ErrInvalidCiphertext is returned for malformed message framing.
	ErrInvalidCiphertext = errors.New("netsec: invalid ciphertext")

	cryptoRandReader io.Reader = rand.Reader
)

// Encrypt encrypts plaintext while binding the sender partyU and the
// recipient partyV to the derived content-encryption key.
//
// Accepted forms include:
//
//	Encrypt(plaintext, partyU, partyV, keyPairU, keyPairV)
//	Encrypt(plaintext, partyU, partyV, privateKeyU, publicKeyV)
//	Encrypt(plaintext, partyU, partyV, sharedSecret)
//	Encrypt(sharedSecret, partyU, partyV, plaintext)
//
// partyU and partyV may be string, []byte, or PartyInfo values. The argument
// order for partyU and partyV is significant and is never sorted or reversed.
func Encrypt(args ...any) ([]byte, error) {
	plan, err := parseEncryptArgs(args)
	if err != nil {
		return nil, err
	}
	return plan.encrypt()
}

// Decrypt decrypts a message. partyU and partyV must be supplied in the same
// sender-then-recipient order used for encryption. Supplying the same values
// in reverse order derives a different key and returns ErrAuthentication.
//
// Accepted forms include:
//
//	Decrypt(ciphertext, partyU, partyV, keyPairV, keyPairU)
//	Decrypt(ciphertext, partyU, partyV, privateKeyV, publicKeyU)
//	Decrypt(ciphertext, partyU, partyV, sharedSecret)
//	Decrypt(sharedSecret, partyU, partyV, ciphertext)
func Decrypt(args ...any) ([]byte, error) {
	plan, err := parseDecryptArgs(args)
	if err != nil {
		return nil, err
	}
	return plan.decrypt()
}

// Seal is an alias for Encrypt.
func Seal(args ...any) ([]byte, error) {
	return Encrypt(args...)
}

// Open is an alias for Decrypt.
func Open(args ...any) ([]byte, error) {
	return Decrypt(args...)
}

// EncryptWithContext is an alias for Encrypt.
func EncryptWithContext(args ...any) ([]byte, error) {
	return Encrypt(args...)
}

// DecryptWithContext is an alias for Decrypt.
func DecryptWithContext(args ...any) ([]byte, error) {
	return Decrypt(args...)
}

// EncryptString encrypts a string and returns the ciphertext as a byte slice.
func EncryptString(plaintext string, args ...any) ([]byte, error) {
	all := make([]any, 0, len(args)+1)
	all = append(all, []byte(plaintext))
	all = append(all, args...)
	return Encrypt(all...)
}

// DecryptString decrypts a message and returns the plaintext as a string.
func DecryptString(ciphertext []byte, args ...any) (string, error) {
	plaintext, err := Decrypt(append([]any{ciphertext}, args...)...)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func (plan *encryptPlan) encrypt() ([]byte, error) {
	if plan.senderPrivate != nil && plan.recipientPublic != nil {
		sharedSecret, err := plan.senderPrivate.ECDH(plan.recipientPublic)
		if err != nil {
			return nil, fmt.Errorf("netsec: ECDH failed: %w", err)
		}
		senderPublic := plan.senderPrivate.PublicKey().Bytes()
		recipientPublic := plan.recipientPublic.Bytes()
		key, err := deriveECDHKey(sharedSecret, plan.partyU, plan.partyV, senderPublic, recipientPublic)
		if err != nil {
			return nil, err
		}
		header, nonce, err := newHeader(modeECDH, senderPublic)
		if err != nil {
			return nil, err
		}
		ciphertext, err := sealWithKey(key, nonce, plan.plaintext, header)
		if err != nil {
			return nil, err
		}
		return append(header, ciphertext...), nil
	}

	if len(plan.sharedSecret) != 0 {
		key, err := DeriveKey(plan.sharedSecret, plan.partyU, plan.partyV)
		if err != nil {
			return nil, err
		}
		header, nonce, err := newHeader(modeSharedKey, nil)
		if err != nil {
			return nil, err
		}
		ciphertext, err := sealWithKey(key, nonce, plan.plaintext, header)
		if err != nil {
			return nil, err
		}
		return append(header, ciphertext...), nil
	}

	return nil, errors.New("netsec: missing key material")
}

func (plan *decryptPlan) decrypt() ([]byte, error) {
	mode, senderPublic, nonce, headerLen, err := decodeHeader(plan.ciphertext)
	if err != nil {
		return nil, err
	}
	header := plan.ciphertext[:headerLen]
	ciphertext := plan.ciphertext[headerLen:]

	var key []byte
	switch mode {
	case modeSharedKey:
		if len(plan.sharedSecret) == 0 {
			return nil, errors.New("netsec: message requires a shared secret")
		}
		key, err = DeriveKey(plan.sharedSecret, plan.partyU, plan.partyV)
		if err != nil {
			return nil, err
		}
	case modeECDH:
		if plan.recipientPrivate == nil {
			return nil, errors.New("netsec: message requires a recipient private key")
		}
		if len(plan.senderPublic) != 0 {
			if subtle.ConstantTimeCompare(plan.senderPublic, senderPublic) != 1 {
				return nil, ErrAuthentication
			}
		}
		curve := curveForKeyMaterial(nil, senderPublic)
		if curve != plan.recipientPrivate.Curve() {
			return nil, ErrAuthentication
		}
		peerPublic, err := curve.NewPublicKey(senderPublic)
		if err != nil {
			return nil, ErrInvalidCiphertext
		}
		sharedSecret, err := plan.recipientPrivate.ECDH(peerPublic)
		if err != nil {
			return nil, ErrAuthentication
		}
		key, err = deriveECDHKey(
			sharedSecret,
			plan.partyU,
			plan.partyV,
			senderPublic,
			plan.recipientPrivate.PublicKey().Bytes(),
		)
		if err != nil {
			return nil, err
		}
	default:
		return nil, ErrInvalidCiphertext
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, header)
	if err != nil {
		return nil, ErrAuthentication
	}
	return plaintext, nil
}

func newHeader(mode byte, publicKey []byte) ([]byte, []byte, error) {
	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(cryptoRandReader, nonce); err != nil {
		return nil, nil, err
	}
	header := make([]byte, headerPrefix+len(publicKey)+nonceSize)
	copy(header[:len(messageMagic)], messageMagic)
	header[6] = messageVersion
	header[7] = mode
	binary.BigEndian.PutUint16(header[8:headerPrefix], uint16(len(publicKey)))
	copy(header[headerPrefix:], publicKey)
	copy(header[headerPrefix+len(publicKey):], nonce)
	return header, nonce, nil
}

func decodeHeader(message []byte) (byte, []byte, []byte, int, error) {
	if len(message) < minimumCiphertextSize {
		return 0, nil, nil, 0, ErrInvalidCiphertext
	}
	if string(message[:len(messageMagic)]) != messageMagic || message[6] != messageVersion {
		return 0, nil, nil, 0, ErrInvalidCiphertext
	}
	mode := message[7]
	publicKeyLen := int(binary.BigEndian.Uint16(message[8:headerPrefix]))
	if publicKeyLen > 1024 {
		return 0, nil, nil, 0, ErrInvalidCiphertext
	}
	headerLen := headerPrefix + publicKeyLen + nonceSize
	if headerLen > len(message) || len(message)-headerLen < gcmTagSize {
		return 0, nil, nil, 0, ErrInvalidCiphertext
	}
	publicKey := cloneBytes(message[headerPrefix : headerPrefix+publicKeyLen])
	nonce := cloneBytes(message[headerPrefix+publicKeyLen : headerLen])
	return mode, publicKey, nonce, headerLen, nil
}

func sealWithKey(key, nonce, plaintext, additionalData []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return aead.Seal(nil, nonce, plaintext, additionalData), nil
}
