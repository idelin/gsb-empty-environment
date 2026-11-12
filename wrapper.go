package netsec

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

const keyWrapLabelVersion = 1

// KeyWrapContext binds a wrapped data key to its topic, data-key generation,
// and recipient. Implementations should authenticate this context.
type KeyWrapContext struct {
	Topic     string
	DataKeyID string
	KeyName   string
}

// KeyWrapper is the injectable per-recipient key wrapping primitive. The
// default implementation is RSA-OAEP with SHA-256. Implementations used by a
// Decryptor must be safe for concurrent use.
type KeyWrapper interface {
	WrapDataKey(context KeyWrapContext, dataKey []byte, publicKey *rsa.PublicKey) ([]byte, error)
	UnwrapDataKey(context KeyWrapContext, wrappedKey []byte, privateKey *rsa.PrivateKey) ([]byte, error)
}

type rsaOAEPWrapper struct{}

// NewRSAOAEPWrapper returns an RSA-OAEP-SHA256 key wrapper.
func NewRSAOAEPWrapper() KeyWrapper {
	return rsaOAEPWrapper{}
}

func (rsaOAEPWrapper) WrapDataKey(context KeyWrapContext, dataKey []byte, publicKey *rsa.PublicKey) ([]byte, error) {
	if publicKey == nil {
		return nil, fmt.Errorf("%w: nil recipient public key", ErrInvalidConfig)
	}
	wrapped, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, publicKey, dataKey, context.label())
	if err != nil {
		return nil, fmt.Errorf("wrap data key: %w", err)
	}
	return wrapped, nil
}

func (rsaOAEPWrapper) UnwrapDataKey(context KeyWrapContext, wrappedKey []byte, privateKey *rsa.PrivateKey) ([]byte, error) {
	if privateKey == nil {
		return nil, fmt.Errorf("%w: nil recipient private key", ErrInvalidConfig)
	}
	dataKey, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, privateKey, wrappedKey, context.label())
	if err != nil {
		return nil, fmt.Errorf("unwrap data key: %w", err)
	}
	return dataKey, nil
}

func (context KeyWrapContext) label() []byte {
	label := struct {
		Version   int    `json:"version"`
		Topic     string `json:"topic"`
		DataKeyID string `json:"dataKeyId"`
		KeyName   string `json:"keyName"`
	}{
		Version:   keyWrapLabelVersion,
		Topic:     context.Topic,
		DataKeyID: context.DataKeyID,
		KeyName:   context.KeyName,
	}
	data, _ := json.Marshal(label)
	return data
}
