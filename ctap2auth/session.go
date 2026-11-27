package ctap2auth

// AuthSession is one established PIN/UV Auth Protocol Two ECDH session. It owns
// the platform ephemeral key, the validated shared secret Z and the two
// independently derived keys.
type AuthSession struct {
	platform     *PlatformKey
	sharedSecret []byte
	keys         *ProtocolTwoKeys
}

// NewAuthSession generates a platform P-256 key and waits for the peer point.
func NewAuthSession() (*AuthSession, error) {
	platform, err := GeneratePlatformKey()
	if err != nil {
		return nil, err
	}
	return &AuthSession{platform: platform}, nil
}

// PlatformKeyAgreement returns the SEC1 uncompressed platform public point to
// send inside the ClientPIN keyAgreement field.
func (s *AuthSession) PlatformKeyAgreement() []byte {
	return s.platform.PublicKey()
}

// Establish validates the authenticator keyAgreement point, runs ECDH and
// derives the separate HMAC and AES keys. An invalid/low-order peer point is
// rejected before any key material is produced.
func (s *AuthSession) Establish(authenticatorKeyAgreement []byte) error {
	z, err := s.platform.SharedSecret(authenticatorKeyAgreement)
	if err != nil {
		return err
	}
	keys, err := DeriveKeys(z)
	if err != nil {
		return err
	}
	s.sharedSecret = z
	s.keys = keys
	return nil
}

// SharedSecret returns the validated 32-byte ECDH output Z.
func (s *AuthSession) SharedSecret() []byte { return s.sharedSecret }

// Keys returns the derived Protocol Two key pair.
func (s *AuthSession) Keys() *ProtocolTwoKeys { return s.keys }

// Encrypt encrypts with the session AES key and a fresh random IV.
func (s *AuthSession) Encrypt(plaintext []byte) ([]byte, error) {
	return AES256CBCEncrypt(s.keys.AES, plaintext)
}

// Decrypt decrypts with the session AES key and validates the random IV.
func (s *AuthSession) Decrypt(ivAndCiphertext []byte) ([]byte, error) {
	return AES256CBCDecrypt(s.keys.AES, ivAndCiphertext)
}

// Authenticate produces the full 32-byte HMAC-SHA-256 tag.
func (s *AuthSession) Authenticate(message []byte) ([]byte, error) {
	return Authenticate(s.keys.HMAC, message)
}

// VerifyAuth verifies a full-length tag and rejects Protocol One downgrades.
func (s *AuthSession) VerifyAuth(message, authParam []byte) (bool, error) {
	return VerifyAuth(s.keys.HMAC, message, authParam)
}
