package disclosure

import "crypto/ed25519"

func ed25519Pair() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	return publicKey, privateKey, err
}
