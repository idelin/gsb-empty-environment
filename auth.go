package netsec

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"io"
	"math/big"
	"strings"
)

const (
	// ClientPubKeySize is the size in bytes of an x25519 client public key.
	ClientPubKeySize = 32
	// DescriptorCookieSize is the size in bytes of the per-descriptor cookie.
	DescriptorCookieSize = 16
	// PaddingMultiple is the granularity the entry count is padded up to.
	PaddingMultiple = 16

	authLinePrefix      = "descriptor:x25519:"
	authPubKeyBase32Len = 52
	kdfInfo             = "hsdir-encrypted-data"
)

// ErrNoClients is returned by BuildDescriptor when no authorized clients remain.
var ErrNoClients = errors.New("netsec: no authorized clients remain")

var authBase32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// X25519 is the Diffie-Hellman primitive, injected by the caller.
type X25519 interface {
	// GenerateKey returns a fresh private/public keypair.
	GenerateKey(r io.Reader) (priv, pub []byte, err error)
	// SharedSecret derives the shared secret between priv and peerPub.
	// It must report an error for contributory-invalid (low-order) inputs.
	SharedSecret(priv, peerPub []byte) ([]byte, error)
}

// Cipher is the authenticated symmetric primitive, injected by the caller.
type Cipher interface {
	KeySize() int
	NonceSize() int
	Seal(key, nonce, plaintext, aad []byte) ([]byte, error)
	Open(key, nonce, ciphertext, aad []byte) ([]byte, error)
}

// Authorizer tracks the set of clients allowed to decrypt the next
// descriptor. It is safe to rebuild or mutate between descriptor revisions.
type Authorizer struct {
	x25519  X25519
	cipher  Cipher
	rand    io.Reader
	clients map[[ClientPubKeySize]byte][]byte
}

// NewAuthorizer creates an Authorizer. If r is nil, crypto/rand.Reader is used.
func NewAuthorizer(x X25519, c Cipher, r io.Reader) *Authorizer {
	if r == nil {
		r = rand.Reader
	}
	return &Authorizer{
		x25519:  x,
		cipher:  c,
		rand:    r,
		clients: make(map[[ClientPubKeySize]byte][]byte),
	}
}

// ClientCount reports how many distinct clients are currently authorized.
func (a *Authorizer) ClientCount() int {
	return len(a.clients)
}

// AddClient registers a client public key. It returns false for keys that
// are malformed, already registered, or that force an all-zero shared
// secret (low-order points).
func (a *Authorizer) AddClient(pub []byte) bool {
	if len(pub) != ClientPubKeySize {
		return false
	}
	var id [ClientPubKeySize]byte
	copy(id[:], pub)
	if _, dup := a.clients[id]; dup {
		return false
	}
	if !a.usable(pub) {
		return false
	}
	a.clients[id] = bytes.Clone(pub)
	return true
}

// Revoke removes a client; it will be absent from the next descriptor.
// It reports whether the client was present.
func (a *Authorizer) Revoke(pub []byte) bool {
	var id [ClientPubKeySize]byte
	if len(pub) != ClientPubKeySize {
		return false
	}
	copy(id[:], pub)
	if _, ok := a.clients[id]; !ok {
		return false
	}
	delete(a.clients, id)
	return true
}

// LoadAuth parses an authorization file of "descriptor:x25519:<base32>"
// lines. Malformed lines, duplicates and low-order keys are skipped.
// Blank lines are ignored without counting as skipped.
func (a *Authorizer) LoadAuth(data []byte) (added, skipped int) {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		pub, ok := parseAuthLine(line)
		if !ok {
			skipped++
			continue
		}
		if a.AddClient(pub) {
			added++
		} else {
			skipped++
		}
	}
	return added, skipped
}

// parseAuthLine validates one "descriptor:x25519:<52-char base32>" line and
// returns the decoded 32-byte public key. The encoding must be canonical
// uppercase base32 without padding.
func parseAuthLine(line string) ([]byte, bool) {
	if !strings.HasPrefix(line, authLinePrefix) {
		return nil, false
	}
	enc := line[len(authLinePrefix):]
	if len(enc) != authPubKeyBase32Len {
		return nil, false
	}
	pub, err := authBase32.DecodeString(enc)
	if err != nil || len(pub) != ClientPubKeySize {
		return nil, false
	}
	if authBase32.EncodeToString(pub) != enc {
		return nil, false
	}
	return pub, true
}

// usable reports whether pub yields a non-zero shared secret, i.e. rejects
// low-order public keys regardless of the ephemeral key in use.
func (a *Authorizer) usable(pub []byte) bool {
	priv, _, err := a.x25519.GenerateKey(a.rand)
	if err != nil {
		return false
	}
	secret, err := a.x25519.SharedSecret(priv, pub)
	if err != nil {
		return false
	}
	return !isAllZero(secret)
}

// Descriptor is the client-authorization portion of one descriptor revision.
type Descriptor struct {
	Cookie       []byte
	EphemeralPub []byte
	Entries      [][]byte
}

// BuildDescriptor produces the authorization block for a new descriptor
// revision. A fresh ephemeral key and a fresh descriptor cookie are drawn
// for every call and never reused across revisions. The entry list is
// padded with random entries up to a multiple of PaddingMultiple and
// shuffled, so its size and order reveal nothing about the real client
// count or identities.
func (a *Authorizer) BuildDescriptor() (*Descriptor, error) {
	if len(a.clients) == 0 {
		return nil, ErrNoClients
	}
	priv, ephPub, err := a.x25519.GenerateKey(a.rand)
	if err != nil {
		return nil, err
	}
	cookie := make([]byte, DescriptorCookieSize)
	if _, err := io.ReadFull(a.rand, cookie); err != nil {
		return nil, err
	}
	aad := bindAAD(cookie, ephPub)

	var entries [][]byte
	for _, clientPub := range a.clients {
		secret, err := a.x25519.SharedSecret(priv, clientPub)
		if err != nil || isAllZero(secret) {
			continue
		}
		entry, err := a.sealEntry(deriveKey(secret, cookie, clientPub, a.cipher.KeySize()), clientPub, aad)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if len(entries) == 0 {
		return nil, ErrNoClients
	}

	target := (len(entries) + PaddingMultiple - 1) / PaddingMultiple * PaddingMultiple
	for len(entries) < target {
		entry, err := a.paddingEntry(aad)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := shuffleEntries(a.rand, entries); err != nil {
		return nil, err
	}
	return &Descriptor{Cookie: cookie, EphemeralPub: ephPub, Entries: entries}, nil
}

// sealEntry encrypts plaintext for one entry: nonce || ciphertext.
func (a *Authorizer) sealEntry(key, plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, a.cipher.NonceSize())
	if _, err := io.ReadFull(a.rand, nonce); err != nil {
		return nil, err
	}
	ct, err := a.cipher.Seal(key, nonce, plaintext, aad)
	if err != nil {
		return nil, err
	}
	entry := make([]byte, 0, len(nonce)+len(ct))
	entry = append(entry, nonce...)
	return append(entry, ct...), nil
}

// paddingEntry produces an entry indistinguishable from a real one by
// encrypting random plaintext under a random key.
func (a *Authorizer) paddingEntry(aad []byte) ([]byte, error) {
	key := make([]byte, a.cipher.KeySize())
	if _, err := io.ReadFull(a.rand, key); err != nil {
		return nil, err
	}
	junk := make([]byte, ClientPubKeySize)
	if _, err := io.ReadFull(a.rand, junk); err != nil {
		return nil, err
	}
	return a.sealEntry(key, junk, aad)
}

// ClientDecrypt reports whether the holder of clientPriv/clientPub is
// authorized by d, i.e. whether any entry decrypts to clientPub.
func ClientDecrypt(d *Descriptor, x X25519, c Cipher, clientPriv, clientPub []byte) bool {
	secret, err := x.SharedSecret(clientPriv, d.EphemeralPub)
	if err != nil || isAllZero(secret) {
		return false
	}
	key := deriveKey(secret, d.Cookie, clientPub, c.KeySize())
	aad := bindAAD(d.Cookie, d.EphemeralPub)
	for _, entry := range d.Entries {
		if len(entry) < c.NonceSize() {
			continue
		}
		nonce, ct := entry[:c.NonceSize()], entry[c.NonceSize():]
		pt, err := c.Open(key, nonce, ct, aad)
		if err == nil && bytes.Equal(pt, clientPub) {
			return true
		}
	}
	return false
}

func bindAAD(cookie, ephPub []byte) []byte {
	aad := make([]byte, 0, len(cookie)+len(ephPub))
	aad = append(aad, cookie...)
	return append(aad, ephPub...)
}

// deriveKey is an HKDF-expand-style KDF keyed by the shared secret.
func deriveKey(secret, cookie, clientPub []byte, keySize int) []byte {
	var out, prev []byte
	for counter := byte(1); len(out) < keySize; counter++ {
		h := hmac.New(sha256.New, secret)
		h.Write(prev)
		h.Write([]byte(kdfInfo))
		h.Write(cookie)
		h.Write(clientPub)
		h.Write([]byte{counter})
		prev = h.Sum(nil)
		out = append(out, prev...)
	}
	return out[:keySize]
}

func isAllZero(b []byte) bool {
	var acc byte
	for _, v := range b {
		acc |= v
	}
	return acc == 0
}

func shuffleEntries(r io.Reader, entries [][]byte) error {
	for i := len(entries) - 1; i > 0; i-- {
		n, err := rand.Int(r, big.NewInt(int64(i+1)))
		if err != nil {
			return err
		}
		j := int(n.Int64())
		entries[i], entries[j] = entries[j], entries[i]
	}
	return nil
}
