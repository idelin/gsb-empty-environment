package netsec

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"io"
	"strings"
	"testing"
)

// ecdhX25519 adapts crypto/ecdh to the injected X25519 interface.
type ecdhX25519 struct{}

func (ecdhX25519) GenerateKey(r io.Reader) (priv, pub []byte, err error) {
	k, err := ecdh.X25519().GenerateKey(r)
	if err != nil {
		return nil, nil, err
	}
	return k.Bytes(), k.PublicKey().Bytes(), nil
}

func (ecdhX25519) SharedSecret(priv, peerPub []byte) ([]byte, error) {
	k, err := ecdh.X25519().NewPrivateKey(priv)
	if err != nil {
		return nil, err
	}
	p, err := ecdh.X25519().NewPublicKey(peerPub)
	if err != nil {
		return nil, err
	}
	return k.ECDH(p)
}

// zeroSecretX25519 mimics an implementation that returns an all-zero
// shared secret without an error for low-order inputs.
type zeroSecretX25519 struct{ ecdhX25519 }

func (zeroSecretX25519) SharedSecret(priv, peerPub []byte) ([]byte, error) {
	return make([]byte, 32), nil
}

// aesGCM adapts AES-256-GCM to the injected Cipher interface.
type aesGCM struct{}

func (aesGCM) KeySize() int   { return 32 }
func (aesGCM) NonceSize() int { return 12 }

func (aesGCM) Seal(key, nonce, plaintext, aad []byte) ([]byte, error) {
	g, err := gcm(key)
	if err != nil {
		return nil, err
	}
	return g.Seal(nil, nonce, plaintext, aad), nil
}

func (aesGCM) Open(key, nonce, ciphertext, aad []byte) ([]byte, error) {
	g, err := gcm(key)
	if err != nil {
		return nil, err
	}
	return g.Open(nil, nonce, ciphertext, aad)
}

func gcm(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func newAuthorizer() *Authorizer {
	return NewAuthorizer(ecdhX25519{}, aesGCM{}, rand.Reader)
}

func genClient(t *testing.T) (priv, pub []byte) {
	t.Helper()
	priv, pub, err := ecdhX25519{}.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return priv, pub
}

func authLine(pub []byte) string {
	return authLinePrefix + authBase32.EncodeToString(pub)
}

func TestLoadAuthFormatErrors(t *testing.T) {
	_, pub := genClient(t)
	good := authLine(pub)
	bad := []string{
		"descriptor:ed25519:" + authBase32.EncodeToString(pub), // wrong algorithm
		"onion:x25519:" + authBase32.EncodeToString(pub),       // wrong prefix
		good[:len(good)-1],       // too short
		good + "A",               // too long
		strings.ToLower(good),    // lowercase base32
		good + "=",               // padding not allowed
		good + ":extra",          // trailing field
		good[:len(good)-1] + "0", // invalid base32 char
		good[:len(good)-1] + "!", // invalid char
		"descriptor:x25519:",     // empty key
	}
	// Non-canonical final character: decodes but is not the canonical form.
	last := good[len(good)-1]
	repl := byte('B')
	if last == 'B' {
		repl = 'C'
	}
	bad = append(bad, good[:len(good)-1]+string(repl))

	a := newAuthorizer()
	added, skipped := a.LoadAuth([]byte(strings.Join(bad, "\n")))
	if added != 0 || skipped != len(bad) {
		t.Fatalf("added=%d skipped=%d, want 0/%d", added, skipped, len(bad))
	}
	if a.ClientCount() != 0 {
		t.Fatalf("ClientCount=%d, want 0", a.ClientCount())
	}

	added, skipped = a.LoadAuth([]byte("\n" + good + "\n\n"))
	if added != 1 || skipped != 0 {
		t.Fatalf("added=%d skipped=%d, want 1/0", added, skipped)
	}
}

func TestLoadAuthDuplicate(t *testing.T) {
	_, pub := genClient(t)
	line := authLine(pub)
	a := newAuthorizer()
	added, skipped := a.LoadAuth([]byte(line + "\n" + line))
	if added != 1 || skipped != 1 {
		t.Fatalf("added=%d skipped=%d, want 1/1", added, skipped)
	}
	if a.AddClient(pub) {
		t.Fatal("AddClient accepted a duplicate key")
	}
	if a.ClientCount() != 1 {
		t.Fatalf("ClientCount=%d, want 1", a.ClientCount())
	}
}

func TestLowOrderPublicKeysRejected(t *testing.T) {
	lowOrder := [][]byte{
		make([]byte, 32),                       // u = 0
		append([]byte{1}, make([]byte, 31)...), // u = 1
		// u = p-1, p, p+1 (little-endian), all of order dividing 8.
		append([]byte{0xec}, append(bytes.Repeat([]byte{0xff}, 30), 0x7f)...),
		append([]byte{0xed}, append(bytes.Repeat([]byte{0xff}, 30), 0x7f)...),
		append([]byte{0xee}, append(bytes.Repeat([]byte{0xff}, 30), 0x7f)...),
		append(make([]byte, 31), 0x80), // non-canonical encoding of u = 0
	}
	a := newAuthorizer()
	var lines []string
	for _, pub := range lowOrder {
		if a.AddClient(pub) {
			t.Fatalf("AddClient accepted low-order key %x", pub)
		}
		lines = append(lines, authLine(pub))
	}
	added, skipped := a.LoadAuth([]byte(strings.Join(lines, "\n")))
	if added != 0 || skipped != len(lowOrder) {
		t.Fatalf("added=%d skipped=%d, want 0/%d", added, skipped, len(lowOrder))
	}
	if a.ClientCount() != 0 {
		t.Fatalf("ClientCount=%d, want 0", a.ClientCount())
	}
}

func TestAllZeroSharedSecretRejected(t *testing.T) {
	a := NewAuthorizer(zeroSecretX25519{}, aesGCM{}, rand.Reader)
	_, pub := genClient(t)
	if a.AddClient(pub) {
		t.Fatal("AddClient accepted a key with all-zero shared secret")
	}
}

func TestBuildDescriptorFiltersZeroSecret(t *testing.T) {
	a := newAuthorizer()
	_, pub := genClient(t)
	if !a.AddClient(pub) {
		t.Fatal("AddClient rejected a valid key")
	}
	// Swap in an X25519 that yields all-zero secrets at build time.
	a.x25519 = zeroSecretX25519{}
	if _, err := a.BuildDescriptor(); err != ErrNoClients {
		t.Fatalf("BuildDescriptor err=%v, want ErrNoClients", err)
	}
}

func TestFreshEphemeralKeyAndCookiePerRevision(t *testing.T) {
	a := newAuthorizer()
	_, pub := genClient(t)
	a.AddClient(pub)
	d1, err := a.BuildDescriptor()
	if err != nil {
		t.Fatal(err)
	}
	d2, err := a.BuildDescriptor()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(d1.Cookie, d2.Cookie) {
		t.Fatal("descriptor cookie reused across revisions")
	}
	if bytes.Equal(d1.EphemeralPub, d2.EphemeralPub) {
		t.Fatal("ephemeral key reused across revisions")
	}
}

func TestRevokeAndRegenerate(t *testing.T) {
	a := newAuthorizer()
	victimPriv, victimPub := genClient(t)
	keeperPriv, keeperPub := genClient(t)
	a.AddClient(victimPub)
	a.AddClient(keeperPub)

	d1, err := a.BuildDescriptor()
	if err != nil {
		t.Fatal(err)
	}
	if !ClientDecrypt(d1, ecdhX25519{}, aesGCM{}, victimPriv, victimPub) {
		t.Fatal("victim could not decrypt pre-revocation descriptor")
	}

	if !a.Revoke(victimPub) {
		t.Fatal("Revoke reported victim absent")
	}
	d2, err := a.BuildDescriptor()
	if err != nil {
		t.Fatal(err)
	}
	if ClientDecrypt(d2, ecdhX25519{}, aesGCM{}, victimPriv, victimPub) {
		t.Fatal("revoked client still decrypts the new descriptor")
	}
	if !ClientDecrypt(d2, ecdhX25519{}, aesGCM{}, keeperPriv, keeperPub) {
		t.Fatal("remaining client lost access after revocation")
	}
	if bytes.Equal(d1.Cookie, d2.Cookie) || bytes.Equal(d1.EphemeralPub, d2.EphemeralPub) {
		t.Fatal("post-revocation descriptor reused cookie or ephemeral key")
	}
}

func TestPaddingBoundaries(t *testing.T) {
	cases := []struct{ clients, wantEntries int }{
		{1, 16},
		{15, 16},
		{16, 16},
		{17, 32},
		{31, 32},
		{32, 32},
		{33, 48},
	}
	for _, tc := range cases {
		a := newAuthorizer()
		for i := 0; i < tc.clients; i++ {
			_, pub := genClient(t)
			if !a.AddClient(pub) {
				t.Fatalf("clients=%d: AddClient rejected key %d", tc.clients, i)
			}
		}
		d, err := a.BuildDescriptor()
		if err != nil {
			t.Fatalf("clients=%d: %v", tc.clients, err)
		}
		if len(d.Entries) != tc.wantEntries {
			t.Fatalf("clients=%d: entries=%d, want %d", tc.clients, len(d.Entries), tc.wantEntries)
		}
		if len(d.Entries)%PaddingMultiple != 0 {
			t.Fatalf("clients=%d: entry count not a multiple of %d", tc.clients, PaddingMultiple)
		}
		size := len(d.Entries[0])
		for i, e := range d.Entries {
			if len(e) != size {
				t.Fatalf("clients=%d: entry %d has length %d, want %d", tc.clients, i, len(e), size)
			}
		}
	}
}

func TestPaddingHidesClientCount(t *testing.T) {
	entryCounts := map[int]int{}
	for _, n := range []int{1, 7, 15} {
		a := newAuthorizer()
		for i := 0; i < n; i++ {
			_, pub := genClient(t)
			a.AddClient(pub)
		}
		d, err := a.BuildDescriptor()
		if err != nil {
			t.Fatal(err)
		}
		entryCounts[n] = len(d.Entries)
	}
	if entryCounts[1] != entryCounts[7] || entryCounts[7] != entryCounts[15] {
		t.Fatalf("entry count leaks client count: %v", entryCounts)
	}
}

func TestRoundTripAndNonClient(t *testing.T) {
	a := newAuthorizer()
	var clients [][2][]byte
	for i := 0; i < 3; i++ {
		priv, pub := genClient(t)
		a.AddClient(pub)
		clients = append(clients, [2][]byte{priv, pub})
	}
	d, err := a.BuildDescriptor()
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range clients {
		if !ClientDecrypt(d, ecdhX25519{}, aesGCM{}, c[0], c[1]) {
			t.Fatalf("client %d failed to decrypt", i)
		}
	}
	attackerPriv, attackerPub := genClient(t)
	if ClientDecrypt(d, ecdhX25519{}, aesGCM{}, attackerPriv, attackerPub) {
		t.Fatal("unauthorized client decrypted the descriptor")
	}
}

func TestBuildDescriptorNoClients(t *testing.T) {
	a := newAuthorizer()
	if _, err := a.BuildDescriptor(); err != ErrNoClients {
		t.Fatalf("err=%v, want ErrNoClients", err)
	}
}
