package netsec

import (
	"crypto/aes"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func testAES(key IRK, block [16]byte) ([16]byte, error) {
	cipher, err := aes.NewCipher(key[:])
	if err != nil {
		return [16]byte{}, err
	}
	var output [16]byte
	cipher.Encrypt(output[:], block[:])
	return output, nil
}

func collisionAES(IRK, [16]byte) ([16]byte, error) {
	var output [16]byte
	copy(output[:], []byte{0xaa, 0xbb, 0xcc})
	return output, nil
}

func addressFor(t *testing.T, aesFn AESFunc, irk IRK, prand [3]byte) Address {
	t.Helper()
	prand[0] = (prand[0] & 0x3f) | ResolvablePRandType
	var block [16]byte
	copy(block[:3], prand[:])
	output, err := aesFn(irk, block)
	if err != nil {
		t.Fatalf("AES callback: %v", err)
	}
	return Address{output[0], output[1], output[2], prand[0], prand[1], prand[2]}
}

type testDevice struct {
	id         DeviceID
	irk        IRK
	generation RecordGeneration
	publicKey  ed25519.PublicKey
	privateKey ed25519.PrivateKey
}

func newTestDevice(id DeviceID, generation RecordGeneration) testDevice {
	irkSeed := sha256.Sum256([]byte("test-irk/" + string(id)))
	keySeed := sha256.Sum256([]byte("test-long-term-key/" + string(id)))
	privateKey := ed25519.NewKeyFromSeed(keySeed[:])
	publicKey := privateKey.Public().(ed25519.PublicKey)

	device := testDevice{
		id:         id,
		generation: generation,
		publicKey:  publicKey,
		privateKey: privateKey,
	}
	copy(device.irk[:], irkSeed[:])
	return device
}

func (d testDevice) record() DeviceRecord {
	return DeviceRecord{
		DeviceID:          d.id,
		IRK:               d.irk,
		Generation:        d.generation,
		State:             RecordActive,
		IdentityPublicKey: append([]byte(nil), d.publicKey...),
	}
}

func testSignatureVerifier(publicKey, message, signature []byte) error {
	if len(publicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("public key length %d", len(publicKey))
	}
	if len(signature) != ed25519.SignatureSize {
		return fmt.Errorf("signature length %d", len(signature))
	}
	if !ed25519.Verify(ed25519.PublicKey(publicKey), message, signature) {
		return errors.New("signature mismatch")
	}
	return nil
}

func signChallenge(t *testing.T, key ed25519.PrivateKey, address Address, connectionID uint64, nonce []byte) []byte {
	t.Helper()
	message, err := ChallengeMessage(address, connectionID, nonce)
	if err != nil {
		t.Fatalf("ChallengeMessage: %v", err)
	}
	return ed25519.Sign(key, message)
}

type identityCapture struct {
	mu         sync.Mutex
	identities []Identity
}

func (c *identityCapture) publish(identity Identity) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.identities = append(c.identities, identity)
	return nil
}

func (c *identityCapture) snapshot() []Identity {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Identity(nil), c.identities...)
}

func (c *identityCapture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.identities)
}

type fakeClock struct {
	mu      sync.Mutex
	current time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{current: time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current
}

func (c *fakeClock) Advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.current = c.current.Add(duration)
}
