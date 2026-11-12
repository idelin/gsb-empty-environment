package netsec

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
)

var (
	sharedKeyOnce sync.Once
	sharedKey     *rsa.PrivateKey
	sharedKeyErr  error
)

func testRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	sharedKeyOnce.Do(func() {
		sharedKey, sharedKeyErr = rsa.GenerateKey(rand.Reader, 2048)
	})
	if sharedKeyErr != nil {
		t.Fatalf("generate shared RSA key: %v", sharedKeyErr)
	}
	return sharedKey
}

func generateRSAKey(t *testing.T, bits int) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return key
}

func testRegistry(t *testing.T, names ...string) *Registry {
	t.Helper()
	key := testRSAKey(t)
	recipients := make([]Recipient, 0, len(names))
	for _, name := range names {
		recipients = append(recipients, Recipient{
			KeyName:   name,
			PublicKey: &key.PublicKey,
		})
	}
	registry, err := NewRegistry(recipients)
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	return registry
}

// fakeWrapper exercises the injectable wrapping seam without RSA operations.
// The topic, data key ID, and recipient name are authenticated by the fake
// encoding, just as RSA-OAEP's label does in the default wrapper.
type fakeWrapper struct {
	mu        sync.Mutex
	wrapCalls map[string]int
}

func newFakeWrapper() *fakeWrapper {
	return &fakeWrapper{wrapCalls: make(map[string]int)}
}

func fakeWrapPrefix(context KeyWrapContext) []byte {
	return []byte(fmt.Sprintf("fake\x00%s\x00%s\x00%s\x00", context.Topic, context.DataKeyID, context.KeyName))
}

func (w *fakeWrapper) WrapDataKey(context KeyWrapContext, dataKey []byte, _ *rsa.PublicKey) ([]byte, error) {
	w.mu.Lock()
	w.wrapCalls[context.KeyName]++
	w.mu.Unlock()

	prefix := fakeWrapPrefix(context)
	wrapped := make([]byte, 0, len(prefix)+len(dataKey))
	wrapped = append(wrapped, prefix...)
	wrapped = append(wrapped, dataKey...)
	return wrapped, nil
}

func (w *fakeWrapper) UnwrapDataKey(context KeyWrapContext, wrappedKey []byte, _ *rsa.PrivateKey) ([]byte, error) {
	prefix := fakeWrapPrefix(context)
	if !bytes.HasPrefix(wrappedKey, prefix) {
		return nil, fmt.Errorf("fake wrapper context mismatch")
	}
	return append([]byte(nil), wrappedKey[len(prefix):]...), nil
}

func (w *fakeWrapper) calls(name string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.wrapCalls[name]
}

// recordingCipher records every nonce and key seen by the payload layer and
// fails the test's invariant if a nonce repeats under one data key.
type recordingCipher struct {
	mu         sync.Mutex
	delegate   Cipher
	seenNonce  map[string]map[string]struct{}
	keyIDs     map[string]struct{}
	nonces     [][]byte
	duplicates int
}

func newRecordingCipher() *recordingCipher {
	return &recordingCipher{
		delegate:  NewAESGCM(),
		seenNonce: make(map[string]map[string]struct{}),
		keyIDs:    make(map[string]struct{}),
	}
}

func (c *recordingCipher) Encrypt(key, nonce, plaintext, additionalData []byte) ([]byte, error) {
	c.mu.Lock()
	keyID := hex.EncodeToString(key)
	c.keyIDs[keyID] = struct{}{}
	if c.seenNonce[keyID] == nil {
		c.seenNonce[keyID] = make(map[string]struct{})
	}
	if _, exists := c.seenNonce[keyID][string(nonce)]; exists {
		c.duplicates++
	} else {
		c.seenNonce[keyID][string(nonce)] = struct{}{}
	}
	c.nonces = append(c.nonces, append([]byte(nil), nonce...))
	c.mu.Unlock()

	return c.delegate.Encrypt(key, nonce, plaintext, additionalData)
}

func (c *recordingCipher) Decrypt(key, nonce, ciphertext, additionalData []byte) ([]byte, error) {
	return c.delegate.Decrypt(key, nonce, ciphertext, additionalData)
}

func (c *recordingCipher) duplicateCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.duplicates
}

func (c *recordingCipher) distinctKeyCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.keyIDs)
}

func (c *recordingCipher) observedNonces() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([][]byte, len(c.nonces))
	for i := range c.nonces {
		out[i] = append([]byte(nil), c.nonces[i]...)
	}
	return out
}

func cloneMessage(t *testing.T, message *Message) *Message {
	t.Helper()
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	var clone Message
	if err := json.Unmarshal(encoded, &clone); err != nil {
		t.Fatalf("unmarshal message: %v", err)
	}
	return &clone
}
