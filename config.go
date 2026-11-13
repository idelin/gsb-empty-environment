package netsec

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
)

// BucketAEAD is the callback used to seal and open every tree bucket.  The
// standard crypto/cipher.AEAD interface satisfies it, so AES-GCM or
// ChaCha20-Poly1305 can be supplied directly.
type BucketAEAD interface {
	Seal(dst, nonce, plaintext, additionalData []byte) []byte
	Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error)
	NonceSize() int
}

// Config fixes the geometry and cryptographic parameters of one tree.  The
// problem statement requires 2 <= Height <= 10 and 1 <= BucketCapacity <= 8.
type Config struct {
	// TreeID is the stable identity of the tree.  It is bound into every
	// bucket's AEAD additional data and every root receipt.
	TreeID string

	Height         int
	BucketCapacity int

	// BlockDataSize is the exact plaintext length of every logical block.
	BlockDataSize int

	// StashCapacity limits the number of decrypted blocks held by the
	// client.  Zero selects a conservative default.
	StashCapacity int

	// AEAD authenticates each bucket.  The client derives a deterministic
	// nonce from the additional data and plaintext so that replaying the
	// same journaled transaction produces byte-identical buckets.
	AEAD BucketAEAD

	// MACKey keys the HMAC-SHA256 carried by each logical block.  It must
	// not be shared with the server.
	MACKey []byte

	// RootPublicKey verifies server-signed root receipts.  Exactly one of
	// RootPublicKey and VerifyReceipt is required.
	RootPublicKey ed25519.PublicKey

	// VerifyReceipt, when non-nil, replaces the built-in ed25519 verifier.
	VerifyReceipt func(RootReceipt) error

	// Rand is the host random source used to draw new leaves.  It defaults
	// to crypto/rand.
	Rand io.Reader

	// SelectLeaf, when non-nil, replaces Rand for leaf selection.  It is
	// useful for deterministic tests and must return a value in
	// [0, 1<<Height).
	SelectLeaf func(leafCount int) (int, error)
}

const defaultStashCapacity = 1024

func (c Config) validate() (Config, error) {
	if c.TreeID == "" {
		return Config{}, fmt.Errorf("%w: empty tree ID", ErrInvalidConfig)
	}
	if c.Height < 2 || c.Height > 10 {
		return Config{}, fmt.Errorf("%w: height %d outside [2,10]", ErrInvalidConfig, c.Height)
	}
	if c.BucketCapacity < 1 || c.BucketCapacity > 8 {
		return Config{}, fmt.Errorf("%w: bucket capacity %d outside [1,8]", ErrInvalidConfig, c.BucketCapacity)
	}
	if c.BlockDataSize <= 0 || c.BlockDataSize > 1<<20 {
		return Config{}, fmt.Errorf("%w: block data size %d outside [1,1MiB]", ErrInvalidConfig, c.BlockDataSize)
	}
	if c.StashCapacity == 0 {
		c.StashCapacity = defaultStashCapacity
	}
	if c.StashCapacity < 1 {
		return Config{}, fmt.Errorf("%w: negative stash capacity", ErrInvalidConfig)
	}
	if c.AEAD == nil {
		return Config{}, fmt.Errorf("%w: nil bucket AEAD", ErrInvalidConfig)
	}
	if n := c.AEAD.NonceSize(); n < 8 || n > 32 {
		return Config{}, fmt.Errorf("%w: AEAD nonce size %d outside [8,32]", ErrInvalidConfig, n)
	}
	if len(c.MACKey) < 16 {
		return Config{}, fmt.Errorf("%w: MAC key shorter than 16 bytes", ErrInvalidConfig)
	}
	if c.VerifyReceipt == nil {
		if len(c.RootPublicKey) != ed25519.PublicKeySize {
			return Config{}, fmt.Errorf("%w: missing root verifier", ErrInvalidConfig)
		}
	}
	if c.SelectLeaf == nil {
		if c.Rand == nil {
			c.Rand = rand.Reader
		}
	}
	c.MACKey = append([]byte(nil), c.MACKey...)
	c.RootPublicKey = append(ed25519.PublicKey(nil), c.RootPublicKey...)
	return c, nil
}
