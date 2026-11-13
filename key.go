package netsec

import (
	"crypto/ecdh"
	"crypto/rand"
	"errors"
)

// KeyPair is an X25519 key pair. Private and Public contain the raw wire
// encodings used by crypto/ecdh.
type KeyPair struct {
	Private []byte
	Public  []byte

	curve ecdh.Curve
}

// GenerateKeyPair creates a fresh X25519 key pair.
func GenerateKeyPair() (*KeyPair, error) {
	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return keyPairFromPrivate(privateKey), nil
}

// NewKeyPair is an alias for GenerateKeyPair.
func NewKeyPair() (*KeyPair, error) {
	return GenerateKeyPair()
}

// GenerateKey is an alias for GenerateKeyPair.
func GenerateKey() (*KeyPair, error) {
	return GenerateKeyPair()
}

// PrivateKeyBytes returns a copy of the raw private-key encoding.
func (k *KeyPair) PrivateKeyBytes() []byte {
	if k == nil {
		return nil
	}
	return cloneBytes(k.Private)
}

// PublicKeyBytes returns a copy of the raw public-key encoding.
func (k *KeyPair) PublicKeyBytes() []byte {
	if k == nil {
		return nil
	}
	if len(k.Public) != 0 {
		return cloneBytes(k.Public)
	}
	privateKey, err := k.ecdhPrivateKey()
	if err != nil {
		return nil
	}
	return privateKey.PublicKey().Bytes()
}

// PrivateKey returns a copy of the raw private-key encoding.
func (k *KeyPair) PrivateKey() []byte {
	return k.PrivateKeyBytes()
}

// PublicKey returns a copy of the raw public-key encoding.
func (k *KeyPair) PublicKey() []byte {
	return k.PublicKeyBytes()
}

// ECDH performs key agreement with peerPublic and returns the raw shared
// secret.
func (k *KeyPair) ECDH(peerPublic any) ([]byte, error) {
	privateKey, err := k.ecdhPrivateKey()
	if err != nil {
		return nil, err
	}
	publicKey, err := publicKeyFrom(peerPublic)
	if err != nil {
		return nil, err
	}
	return privateKey.ECDH(publicKey)
}

func keyPairFromPrivate(privateKey *ecdh.PrivateKey) *KeyPair {
	return &KeyPair{
		Private: privateKey.Bytes(),
		Public:  privateKey.PublicKey().Bytes(),
		curve:   privateKey.Curve(),
	}
}

func (k *KeyPair) ecdhPrivateKey() (*ecdh.PrivateKey, error) {
	if k == nil || len(k.Private) == 0 {
		return nil, errors.New("netsec: missing private key")
	}
	curve := k.curve
	if curve == nil {
		curve = curveForKeyMaterial(k.Private, k.Public)
	}
	privateKey, err := curve.NewPrivateKey(k.Private)
	if err != nil {
		return nil, errors.New("netsec: invalid private key")
	}
	return privateKey, nil
}

func privateKeyFrom(value any) (*ecdh.PrivateKey, error) {
	switch key := value.(type) {
	case *ecdh.PrivateKey:
		if key == nil {
			return nil, errors.New("netsec: nil private key")
		}
		return key, nil
	case ecdh.PrivateKey:
		copyOfKey := key
		return &copyOfKey, nil
	case *KeyPair:
		return key.ecdhPrivateKey()
	case KeyPair:
		copyOfPair := key
		return copyOfPair.ecdhPrivateKey()
	case []byte:
		curve := curveForKeyMaterial(key, nil)
		privateKey, err := curve.NewPrivateKey(key)
		if err != nil {
			return nil, errors.New("netsec: invalid private key")
		}
		return privateKey, nil
	default:
		return nil, errors.New("netsec: unsupported private-key type")
	}
}

func publicKeyFrom(value any) (*ecdh.PublicKey, error) {
	switch key := value.(type) {
	case *ecdh.PublicKey:
		if key == nil {
			return nil, errors.New("netsec: nil public key")
		}
		return key, nil
	case ecdh.PublicKey:
		copyOfKey := key
		return &copyOfKey, nil
	case *KeyPair:
		if key == nil {
			return nil, errors.New("netsec: nil key pair")
		}
		publicBytes := key.PublicKeyBytes()
		if len(publicBytes) == 0 {
			return nil, errors.New("netsec: missing public key")
		}
		curve := key.curve
		if curve == nil {
			curve = curveForKeyMaterial(nil, publicBytes)
		}
		publicKey, err := curve.NewPublicKey(publicBytes)
		if err != nil {
			return nil, errors.New("netsec: invalid public key")
		}
		return publicKey, nil
	case KeyPair:
		copyOfPair := key
		return publicKeyFrom(&copyOfPair)
	case []byte:
		curve := curveForKeyMaterial(nil, key)
		publicKey, err := curve.NewPublicKey(key)
		if err != nil {
			return nil, errors.New("netsec: invalid public key")
		}
		return publicKey, nil
	default:
		return nil, errors.New("netsec: unsupported public-key type")
	}
}

func curveForKeyMaterial(private, public []byte) ecdh.Curve {
	switch len(public) {
	case 65:
		return ecdh.P256()
	case 97:
		return ecdh.P384()
	case 133:
		return ecdh.P521()
	}
	switch len(private) {
	case 48:
		return ecdh.P384()
	case 66:
		return ecdh.P521()
	}
	return ecdh.X25519()
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	return append([]byte(nil), value...)
}
