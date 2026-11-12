package netsec

import (
	"crypto/rsa"
	"fmt"
)

// DecryptorConfig configures a topic Decryptor. The Registry is the complete
// allowed key-name set. PrivateKeys contains only the private keys this
// consumer actually holds.
type DecryptorConfig struct {
	Topic       string
	Registry    *Registry
	PrivateKeys []RecipientPrivateKey
	Cipher      Cipher
	KeyWrapper  KeyWrapper
}

// Decryptor verifies recipient policy and decrypts messages for one topic.
// It is safe for concurrent use.
type Decryptor struct {
	topic       string
	registry    *Registry
	privateKeys map[string]*rsa.PrivateKey
	cipher      Cipher
	keyWrapper  KeyWrapper
}

// NewDecryptor constructs a topic Decryptor. Every supplied private key must
// correspond to a public key registered under the same key name.
func NewDecryptor(config DecryptorConfig) (*Decryptor, error) {
	if config.Registry == nil {
		return nil, fmt.Errorf("%w: nil recipient registry", ErrInvalidConfig)
	}
	if len(config.PrivateKeys) == 0 {
		return nil, fmt.Errorf("%w: no private keys", ErrInvalidConfig)
	}

	decryptor := &Decryptor{
		topic:       config.Topic,
		registry:    config.Registry,
		privateKeys: make(map[string]*rsa.PrivateKey, len(config.PrivateKeys)),
		cipher:      config.Cipher,
		keyWrapper:  config.KeyWrapper,
	}
	for _, entry := range config.PrivateKeys {
		if entry.KeyName == "" {
			return nil, fmt.Errorf("%w: empty private key name", ErrInvalidConfig)
		}
		if entry.PrivateKey == nil {
			return nil, fmt.Errorf("%w: nil private key for %q", ErrInvalidConfig, entry.KeyName)
		}
		if _, exists := decryptor.privateKeys[entry.KeyName]; exists {
			return nil, fmt.Errorf("%w: duplicate private key %q", ErrInvalidConfig, entry.KeyName)
		}
		if err := entry.PrivateKey.Validate(); err != nil {
			return nil, fmt.Errorf("%w: invalid private key %q: %v", ErrInvalidConfig, entry.KeyName, err)
		}
		registeredKey, ok := config.Registry.publicKey(entry.KeyName)
		if !ok {
			return nil, fmt.Errorf("%w: private key %q is not registered", ErrInvalidConfig, entry.KeyName)
		}
		if !entry.PrivateKey.PublicKey.Equal(registeredKey) {
			return nil, fmt.Errorf("%w: private key %q does not match registered public key", ErrInvalidConfig, entry.KeyName)
		}
		decryptor.privateKeys[entry.KeyName] = entry.PrivateKey
	}
	if decryptor.cipher == nil {
		decryptor.cipher = NewAESGCM()
	}
	if decryptor.keyWrapper == nil {
		decryptor.keyWrapper = NewRSAOAEPWrapper()
	}
	return decryptor, nil
}

// Decrypt rejects messages containing any unregistered key name, then tries
// the private keys it holds. The entire metadata list is authenticated.
func (d *Decryptor) Decrypt(message *Message) ([]byte, error) {
	if message == nil {
		return nil, fmt.Errorf("%w: nil message", ErrInvalidMessage)
	}
	if message.Version != messageVersion {
		return nil, fmt.Errorf("%w: unsupported version %d", ErrInvalidMessage, message.Version)
	}
	if len(message.Nonce) != nonceSize {
		return nil, fmt.Errorf("%w: nonce must be %d bytes", ErrInvalidMessage, nonceSize)
	}
	if len(message.EncryptedKeys) == 0 {
		return nil, fmt.Errorf("%w: empty encrypted key list", ErrInvalidMessage)
	}

	seen := make(map[string]struct{}, len(message.EncryptedKeys))
	for _, wrapped := range message.EncryptedKeys {
		if wrapped.KeyName == "" {
			return nil, fmt.Errorf("%w: empty key name", ErrInvalidMessage)
		}
		if _, ok := d.registry.publicKey(wrapped.KeyName); !ok {
			return nil, fmt.Errorf("%w: %q", ErrUnknownKeyName, wrapped.KeyName)
		}
		if _, duplicate := seen[wrapped.KeyName]; duplicate {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateKeyName, wrapped.KeyName)
		}
		seen[wrapped.KeyName] = struct{}{}
		if len(wrapped.WrappedKey) == 0 {
			return nil, fmt.Errorf("%w: empty wrapped key for %q", ErrInvalidMessage, wrapped.KeyName)
		}
	}

	additionalData, err := buildAdditionalData(d.topic, message)
	if err != nil {
		return nil, err
	}

	foundPrivateKey := false
	var lastErr error
	for _, wrapped := range message.EncryptedKeys {
		privateKey, ok := d.privateKeys[wrapped.KeyName]
		if !ok {
			continue
		}
		foundPrivateKey = true

		dataKey, err := d.keyWrapper.UnwrapDataKey(KeyWrapContext{
			Topic:     d.topic,
			DataKeyID: message.DataKeyID,
			KeyName:   wrapped.KeyName,
		}, wrapped.WrappedKey, privateKey)
		if err != nil {
			lastErr = err
			continue
		}
		if len(dataKey) != payloadKeySize {
			clear(dataKey)
			lastErr = fmt.Errorf("%w: unwrapped data key has %d bytes", ErrInvalidMessage, len(dataKey))
			continue
		}

		plaintext, err := d.cipher.Decrypt(dataKey, message.Nonce, message.Ciphertext, additionalData)
		clear(dataKey)
		if err != nil {
			lastErr = err
			continue
		}
		return plaintext, nil
	}

	if !foundPrivateKey {
		return nil, fmt.Errorf("%w: no registered private key appears in message", ErrNoMatchingKey)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no candidate key succeeded")
	}
	return nil, fmt.Errorf("%w: %v", ErrDecrypt, lastErr)
}
