package netsec

import (
	"encoding/json"
	"fmt"
)

const messageVersion = 1

// WrappedDataKey is one recipient's independently wrapped copy of the data key.
// KeyName identifies the registered recipient public key, not the data key.
type WrappedDataKey struct {
	KeyName    string `json:"keyName"`
	WrappedKey []byte `json:"wrappedKey"`
}

// Message is the transportable envelope produced by an Encryptor.
//
// The metadata and nonce are authenticated as AES-GCM additional data. Callers
// may serialize the whole struct, for example with encoding/json.
type Message struct {
	Version       int              `json:"version"`
	DataKeyID     string           `json:"dataKeyId"`
	Nonce         []byte           `json:"nonce"`
	EncryptedKeys []WrappedDataKey `json:"encryptedKeys"`
	Ciphertext    []byte           `json:"ciphertext"`
}

type messageHeader struct {
	Version       int              `json:"version"`
	Topic         string           `json:"topic"`
	DataKeyID     string           `json:"dataKeyId"`
	Nonce         []byte           `json:"nonce"`
	EncryptedKeys []WrappedDataKey `json:"encryptedKeys"`
}

// buildAdditionalData authenticates the topic, data key identity, nonce, and
// the complete recipient key list. Any metadata edit therefore fails GCM
// authentication even before recipient policy is checked.
func buildAdditionalData(topic string, message *Message) ([]byte, error) {
	header := messageHeader{
		Version:       message.Version,
		Topic:         topic,
		DataKeyID:     message.DataKeyID,
		Nonce:         message.Nonce,
		EncryptedKeys: message.EncryptedKeys,
	}
	data, err := json.Marshal(header)
	if err != nil {
		return nil, fmt.Errorf("marshal additional data: %w", err)
	}
	return data, nil
}

func cloneWrappedKeys(keys []WrappedDataKey) []WrappedDataKey {
	out := make([]WrappedDataKey, len(keys))
	for i, key := range keys {
		out[i] = WrappedDataKey{
			KeyName:    key.KeyName,
			WrappedKey: append([]byte(nil), key.WrappedKey...),
		}
	}
	return out
}
