package netsec

// EncryptWithSharedKey encrypts with an explicitly supplied pre-shared secret.
// It accepts the same argument forms as Encrypt.
func EncryptWithSharedKey(args ...any) ([]byte, error) {
	return Encrypt(args...)
}

// DecryptWithSharedKey decrypts with an explicitly supplied pre-shared secret.
// It accepts the same argument forms as Decrypt.
func DecryptWithSharedKey(args ...any) ([]byte, error) {
	return Decrypt(args...)
}

// EncryptWithECDH encrypts using the sender private key and recipient public
// key. partyU is the sender and partyV is the recipient.
func EncryptWithECDH(args ...any) ([]byte, error) {
	return Encrypt(args...)
}

// DecryptWithECDH decrypts using the recipient private key and the sender
// public key. partyU is the sender and partyV is the recipient.
func DecryptWithECDH(args ...any) ([]byte, error) {
	return Decrypt(args...)
}

// EncryptMessage is an alias for Encrypt.
func EncryptMessage(args ...any) ([]byte, error) {
	return Encrypt(args...)
}

// DecryptMessage is an alias for Decrypt.
func DecryptMessage(args ...any) ([]byte, error) {
	return Decrypt(args...)
}

// EncryptCOSE is an alias for Encrypt.
func EncryptCOSE(args ...any) ([]byte, error) {
	return Encrypt(args...)
}

// DecryptCOSE is an alias for Decrypt.
func DecryptCOSE(args ...any) ([]byte, error) {
	return Decrypt(args...)
}

// DeriveKeyFromContext is an alias for DeriveKeyWithContext.
func DeriveKeyFromContext(sharedSecret []byte, context KDFContext) ([]byte, error) {
	return DeriveKeyWithContext(sharedSecret, context)
}

// KeyAgreement performs ECDH key agreement between a private key and a peer
// public key.
func KeyAgreement(privateKey, peerPublic any) ([]byte, error) {
	private, err := privateKeyFrom(privateKey)
	if err != nil {
		return nil, err
	}
	public, err := publicKeyFrom(peerPublic)
	if err != nil {
		return nil, err
	}
	return private.ECDH(public)
}

// SharedSecret is an alias for KeyAgreement.
func SharedSecret(privateKey, peerPublic any) ([]byte, error) {
	return KeyAgreement(privateKey, peerPublic)
}
