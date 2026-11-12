package netsec

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

const (
	ciphertextScheme = "vault"
	keySize          = 32
	nonceSize        = 12
)

var (
	ErrKeyNotFound               = errors.New("transit: key not found")
	ErrKeyExists                 = errors.New("transit: key already exists")
	ErrInvalidCiphertext         = errors.New("transit: invalid ciphertext format")
	ErrBelowMinDecryptionVersion = errors.New("transit: ciphertext version below min_decryption_version")
	ErrBelowMinEncryptionVersion = errors.New("transit: requested version below min_encryption_version")
	ErrVersionNotFound           = errors.New("transit: key version not found")
	ErrDecryptionFailed          = errors.New("transit: decryption failed")
	ErrConvergentRequiresContext = errors.New("transit: convergent key requires a non-empty context")
	ErrInvalidKeySize            = errors.New("transit: key material must be 32 bytes")
)

// KeyConfig configures a named transit key.
type KeyConfig struct {
	Name string
	// MinDecryptionVersion rejects decryption of ciphertexts whose
	// embedded version is lower than this value.
	MinDecryptionVersion uint32
	// MinEncryptionVersion forces new encryptions to use at least this
	// key version.
	MinEncryptionVersion uint32
	// Convergent enables convergent encryption: the same plaintext and
	// context always produce the same ciphertext. The context takes part
	// in subkey derivation.
	Convergent bool
}

type key struct {
	cfg      KeyConfig
	versions map[uint32][]byte
	latest   uint32
}

// Engine is a minimal Transit-style versioned key service.
type Engine struct {
	mu   sync.RWMutex
	keys map[string]*key
}

func NewEngine() *Engine {
	return &Engine{keys: make(map[string]*key)}
}

// CreateKey registers a new named key with version 1 key material.
func (e *Engine) CreateKey(cfg KeyConfig, keyMaterial []byte) error {
	if len(keyMaterial) != keySize {
		return ErrInvalidKeySize
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.keys[cfg.Name]; ok {
		return ErrKeyExists
	}
	material := make([]byte, keySize)
	copy(material, keyMaterial)
	e.keys[cfg.Name] = &key{
		cfg:      cfg,
		versions: map[uint32][]byte{1: material},
		latest:   1,
	}
	return nil
}

// Rotate adds a new key version and returns its version number.
func (e *Engine) Rotate(name string, keyMaterial []byte) (uint32, error) {
	if len(keyMaterial) != keySize {
		return 0, ErrInvalidKeySize
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	k, ok := e.keys[name]
	if !ok {
		return 0, ErrKeyNotFound
	}
	k.latest++
	material := make([]byte, keySize)
	copy(material, keyMaterial)
	k.versions[k.latest] = material
	return k.latest, nil
}

// SetMinVersions updates min_decryption_version / min_encryption_version.
func (e *Engine) SetMinVersions(name string, minDecryption, minEncryption uint32) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	k, ok := e.keys[name]
	if !ok {
		return ErrKeyNotFound
	}
	k.cfg.MinDecryptionVersion = minDecryption
	k.cfg.MinEncryptionVersion = minEncryption
	return nil
}

// LatestVersion reports the latest version of a key.
func (e *Engine) LatestVersion(name string) (uint32, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	k, ok := e.keys[name]
	if !ok {
		return 0, ErrKeyNotFound
	}
	return k.latest, nil
}

// aad builds the AEAD additional authenticated data. It cryptographically
// binds the key name and the key version so that neither the version prefix
// nor the key name can be swapped without failing authentication. The
// encoding is unambiguous (length-prefixed) and independent of the textual
// ciphertext prefix.
func aad(keyName string, version uint32) []byte {
	buf := make([]byte, 0, 2+len(keyName)+4)
	buf = binary.BigEndian.AppendUint16(buf, uint16(len(keyName)))
	buf = append(buf, keyName...)
	buf = binary.BigEndian.AppendUint32(buf, version)
	return buf
}

// deriveSubkey derives a per-context subkey for convergent encryption.
// The context participates in the derivation, so ciphertexts are only
// decryptable with the same context.
func deriveSubkey(master, context []byte) []byte {
	mac := hmac.New(sha256.New, master)
	mac.Write([]byte("transit-convergent-derive"))
	mac.Write(context)
	return mac.Sum(nil)
}

// convergentNonce derives a deterministic nonce from the subkey and
// plaintext, making encryption convergent per (key, context, plaintext).
func convergentNonce(subkey, plaintext []byte) []byte {
	mac := hmac.New(sha256.New, subkey)
	mac.Write([]byte("transit-convergent-nonce"))
	mac.Write(plaintext)
	return mac.Sum(nil)[:nonceSize]
}

func newAEAD(keyMaterial []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(keyMaterial)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// encryptVersion picks the version used for a new encryption:
// max(latest, min_encryption_version).
func (k *key) encryptVersion() (uint32, error) {
	version := k.latest
	if k.cfg.MinEncryptionVersion > version {
		version = k.cfg.MinEncryptionVersion
	}
	if _, ok := k.versions[version]; !ok {
		return 0, fmt.Errorf("%w: v%d", ErrVersionNotFound, version)
	}
	return version, nil
}

func (k *key) seal(version uint32, plaintext, context []byte) (string, error) {
	material, ok := k.versions[version]
	if !ok {
		return "", fmt.Errorf("%w: v%d", ErrVersionNotFound, version)
	}
	encKey := material
	var nonce []byte
	if k.cfg.Convergent {
		if len(context) == 0 {
			return "", ErrConvergentRequiresContext
		}
		encKey = deriveSubkey(material, context)
		defer zero(encKey)
		nonce = convergentNonce(encKey, plaintext)
	} else {
		nonce = make([]byte, nonceSize)
		if _, err := rand.Read(nonce); err != nil {
			return "", err
		}
	}
	aeadCipher, err := newAEAD(encKey)
	if err != nil {
		return "", err
	}
	sealed := aeadCipher.Seal(nil, nonce, plaintext, aad(k.cfg.Name, version))
	blob := append(nonce, sealed...)
	return ciphertextScheme + ":v" + strconv.FormatUint(uint64(version), 10) + ":" +
		base64.StdEncoding.EncodeToString(blob), nil
}

func (k *key) open(version uint32, blob, context []byte) ([]byte, error) {
	if version < k.cfg.MinDecryptionVersion {
		return nil, fmt.Errorf("%w: v%d < v%d", ErrBelowMinDecryptionVersion, version, k.cfg.MinDecryptionVersion)
	}
	material, ok := k.versions[version]
	if !ok {
		return nil, fmt.Errorf("%w: v%d", ErrVersionNotFound, version)
	}
	if len(blob) < nonceSize {
		return nil, ErrInvalidCiphertext
	}
	encKey := material
	if k.cfg.Convergent {
		if len(context) == 0 {
			return nil, ErrConvergentRequiresContext
		}
		encKey = deriveSubkey(material, context)
		defer zero(encKey)
	}
	aeadCipher, err := newAEAD(encKey)
	if err != nil {
		return nil, err
	}
	nonce, sealed := blob[:nonceSize], blob[nonceSize:]
	plaintext, err := aeadCipher.Open(nil, nonce, sealed, aad(k.cfg.Name, version))
	if err != nil {
		return nil, ErrDecryptionFailed
	}
	return plaintext, nil
}

// parseCiphertext splits "vault:v<version>:<base64>" into its parts. The
// parsed version only selects which key version to try; authenticity comes
// from the AEAD additional data, never from the prefix alone.
func parseCiphertext(ciphertext string) (uint32, []byte, error) {
	parts := strings.SplitN(ciphertext, ":", 3)
	if len(parts) != 3 || parts[0] != ciphertextScheme || !strings.HasPrefix(parts[1], "v") {
		return 0, nil, ErrInvalidCiphertext
	}
	version, err := strconv.ParseUint(parts[1][1:], 10, 32)
	if err != nil || version == 0 {
		return 0, nil, ErrInvalidCiphertext
	}
	blob, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return 0, nil, ErrInvalidCiphertext
	}
	return uint32(version), blob, nil
}

// Encrypt encrypts plaintext under the named key and returns a
// "vault:v<version>:<base64>" ciphertext.
func (e *Engine) Encrypt(name string, plaintext, context []byte) (string, error) {
	e.mu.RLock()
	k, ok := e.keys[name]
	e.mu.RUnlock()
	if !ok {
		return "", ErrKeyNotFound
	}
	version, err := k.encryptVersion()
	if err != nil {
		return "", err
	}
	return k.seal(version, plaintext, context)
}

// Decrypt decrypts a versioned ciphertext under the named key.
func (e *Engine) Decrypt(name, ciphertext string, context []byte) ([]byte, error) {
	version, blob, err := parseCiphertext(ciphertext)
	if err != nil {
		return nil, err
	}
	e.mu.RLock()
	k, ok := e.keys[name]
	e.mu.RUnlock()
	if !ok {
		return nil, ErrKeyNotFound
	}
	return k.open(version, blob, context)
}

// Rewrap re-encrypts a ciphertext at the target version (0 means the
// current encryption version). The plaintext is decrypted and re-sealed
// inside the engine and never returned to the caller.
func (e *Engine) Rewrap(name, ciphertext string, context []byte, targetVersion uint32) (string, error) {
	version, blob, err := parseCiphertext(ciphertext)
	if err != nil {
		return "", err
	}
	e.mu.RLock()
	k, ok := e.keys[name]
	e.mu.RUnlock()
	if !ok {
		return "", ErrKeyNotFound
	}
	plaintext, err := k.open(version, blob, context)
	if err != nil {
		return "", err
	}
	defer zero(plaintext)
	if targetVersion == 0 {
		targetVersion, err = k.encryptVersion()
		if err != nil {
			return "", err
		}
	} else {
		if targetVersion < k.cfg.MinEncryptionVersion {
			return "", fmt.Errorf("%w: v%d < v%d", ErrBelowMinEncryptionVersion, targetVersion, k.cfg.MinEncryptionVersion)
		}
		if _, ok := k.versions[targetVersion]; !ok {
			return "", fmt.Errorf("%w: v%d", ErrVersionNotFound, targetVersion)
		}
	}
	return k.seal(targetVersion, plaintext, context)
}

func zero(buf []byte) {
	for i := range buf {
		buf[i] = 0
	}
}
