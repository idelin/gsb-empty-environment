package netsec

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"math/big"
	"testing"
)

func TestFingerprintMatchedDesignatedRevoker(t *testing.T) {
	target, targetKey := testRSAKey(t)
	revoker, revokerKey := testRSAKey(t)
	selfSig := testSelfSignature(t, targetKey, targetKey, target, revokerKey, true)
	revocation := testRevocationSignature(t, targetKey, revokerKey, revoker, true)

	targetEntity := &Entity{
		PrimaryKey:       targetKey,
		DirectSignatures: []*Signature{selfSig},
	}
	ring := NewKeyring(&Entity{PrimaryKey: revokerKey})

	if err := VerifyKeyRevocation(targetEntity, revocation, ring); err != nil {
		t.Fatalf("fingerprint-matched designated revoker was rejected: %v", err)
	}
	if err := VerifyDesignatedRevocation(targetEntity, revokerKey, revocation); err != nil {
		t.Fatalf("explicit designated revocation failed: %v", err)
	}
}

func TestKeyIDCollisionCannotRevoke(t *testing.T) {
	target, targetKey := testRSAKey(t)
	revoker, revokerKey := testRSAKey(t)
	attacker, attackerKey := testRSAKey(t)

	// Simulate a 64-bit key ID collision.  The attacker's actual key ID is
	// unrelated, but the keyring and signature metadata both advertise the
	// designated revoker's short key ID.
	attackerKey.KeyID = append([]byte(nil), revokerKey.KeyID...)
	if bytes.Equal(attackerKey.Fingerprint, revokerKey.Fingerprint) {
		t.Fatal("test keys unexpectedly have the same fingerprint")
	}

	selfSig := testSelfSignature(t, targetKey, targetKey, target, revokerKey, true)
	attackerRevocation := testRevocationSignature(t, targetKey, attackerKey, attacker, true)
	onlyKeyIDRevocation := testRevocationSignature(t, targetKey, attackerKey, attacker, false)

	targetEntity := &Entity{
		PrimaryKey:       targetKey,
		DirectSignatures: []*Signature{selfSig},
	}
	attackerRing := NewKeyring(&Entity{PrimaryKey: attackerKey})
	mixedRing := NewKeyring(&Entity{PrimaryKey: revokerKey}, &Entity{PrimaryKey: attackerKey})

	if err := VerifyKeyRevocation(targetEntity, attackerRevocation, attackerRing); err == nil {
		t.Fatal("revocation from a same-key-ID, different-fingerprint key was accepted")
	}
	if err := VerifyKeyRevocation(targetEntity, attackerRevocation, mixedRing); err == nil {
		t.Fatal("colliding key was accepted when the designated key was also present")
	}
	if err := VerifyKeyRevocationWithKey(targetEntity, attackerKey, attackerRevocation); !errors.Is(err, ErrNotDesignatedRevoker) {
		t.Fatalf("explicit colliding key check returned %v, want ErrNotDesignatedRevoker", err)
	}
	if err := VerifyDesignatedRevocation(targetEntity, attackerKey, attackerRevocation); !errors.Is(err, ErrNotDesignatedRevoker) {
		t.Fatalf("designated revocation with colliding key returned %v, want ErrNotDesignatedRevoker", err)
	}
	if err := VerifyKeyRevocation(targetEntity, onlyKeyIDRevocation, attackerRing); err == nil {
		t.Fatal("revocation identified only by a colliding key ID was accepted")
	}
	if err := VerifyKeyRevocationWithKey(targetEntity, attackerKey, onlyKeyIDRevocation); !errors.Is(err, ErrNotDesignatedRevoker) {
		t.Fatalf("key-ID-only explicit check returned %v, want ErrNotDesignatedRevoker", err)
	}

	// The keyring API exposes the ambiguity but the revocation policy must
	// still compare full fingerprints.
	if got := len(attackerRing.LookupByKeyID(revokerKey.KeyID)); got != 1 {
		t.Fatalf("test keyring lookup by key ID found %d keys, want 1", got)
	}
	if mixedRing.LookupByFingerprint(revokerKey.KeyID) != nil {
		t.Fatal("64-bit key ID was accepted as a fingerprint lookup")
	}
	if targetEntity.IsDesignatedRevoker(revokerKey.KeyID) {
		t.Fatal("64-bit key ID was accepted as a designated revoker fingerprint")
	}

	validRevocation := testRevocationSignature(t, targetKey, revokerKey, revoker, true)
	if err := VerifyKeyRevocation(targetEntity, validRevocation, mixedRing); err != nil {
		t.Fatalf("designated revoker was rejected in a mixed keyring: %v", err)
	}
}

func TestUnhashedRevocationKeyIsIgnored(t *testing.T) {
	target, targetKey := testRSAKey(t)
	revoker, revokerKey := testRSAKey(t)
	selfSig := testSelfSignature(t, targetKey, targetKey, target, revokerKey, false)
	revocation := testRevocationSignature(t, targetKey, revokerKey, revoker, true)

	targetEntity := &Entity{
		PrimaryKey:       targetKey,
		DirectSignatures: []*Signature{selfSig},
	}
	ring := NewKeyring(&Entity{PrimaryKey: revokerKey})
	if err := VerifyKeyRevocation(targetEntity, revocation, ring); !errors.Is(err, ErrNotDesignatedRevoker) {
		t.Fatalf("unhashed designation returned %v, want ErrNotDesignatedRevoker", err)
	}
}

func TestForgedSelfSignatureCannotDesignateRevoker(t *testing.T) {
	_, targetKey := testRSAKey(t)
	attacker, attackerKey := testRSAKey(t)
	forged := testSelfSignature(t, targetKey, attackerKey, attacker, attackerKey, true)

	targetEntity := &Entity{
		PrimaryKey:       targetKey,
		DirectSignatures: []*Signature{forged},
	}
	revocation := testRevocationSignature(t, targetKey, attackerKey, attacker, true)
	ring := NewKeyring(&Entity{PrimaryKey: attackerKey})
	if err := VerifyKeyRevocation(targetEntity, revocation, ring); !errors.Is(err, ErrNotDesignatedRevoker) {
		t.Fatalf("forged self-signature returned %v, want ErrNotDesignatedRevoker", err)
	}
}

func TestSelfRevocationAndWrongType(t *testing.T) {
	target, targetKey := testRSAKey(t)
	selfRevocation := testRevocationSignature(t, targetKey, targetKey, target, true)
	targetEntity := &Entity{PrimaryKey: targetKey}
	if err := VerifyKeyRevocation(targetEntity, selfRevocation, nil); err != nil {
		t.Fatalf("self revocation failed: %v", err)
	}

	wrongType := testSelfSignature(t, targetKey, targetKey, target, targetKey, true)
	if err := VerifyKeyRevocation(targetEntity, wrongType, nil); !errors.Is(err, ErrWrongSignatureType) {
		t.Fatalf("wrong signature type returned %v, want ErrWrongSignatureType", err)
	}
}

func TestKeyringParsingAttachesSelfSignature(t *testing.T) {
	target, targetKey := testRSAKey(t)
	revoker, revokerKey := testRSAKey(t)
	selfSig := testSelfSignature(t, targetKey, targetKey, target, revokerKey, true)
	revocation := testRevocationSignature(t, targetKey, revokerKey, revoker, true)

	data := testPacket(6, targetKey.Body)
	data = append(data, testPacket(2, selfSig.Body)...)
	data = append(data, testPacket(6, revokerKey.Body)...)
	ring, err := ParseKeyring(data)
	if err != nil {
		t.Fatal(err)
	}
	targetEntity := ring.LookupByFingerprint(targetKey.Fingerprint)
	if targetEntity == nil {
		t.Fatal("target not found")
	}
	if len(targetEntity.DirectSignatures) != 1 {
		t.Fatalf("attached %d direct signatures, want 1", len(targetEntity.DirectSignatures))
	}
	if err := VerifyKeyRevocation(targetEntity, revocation, ring); err != nil {
		t.Fatalf("parsed keyring revocation failed: %v", err)
	}
}

func TestUserIDCertificationCanDesignateRevoker(t *testing.T) {
	target, targetKey := testRSAKey(t)
	revoker, revokerKey := testRSAKey(t)
	userID := []byte("alice@example.org")
	hashed := testSubpacket(subpacketSignatureCreationTime, []byte{0, 0, 0, 0})
	hashed = append(hashed, testIssuerSubpackets(targetKey, true)...)
	hashed = append(hashed, testDesignationSubpacket(revokerKey)...)
	signedData := append(canonicalKeyPrefix(targetKey.Version, targetKey.Body), canonicalUserIDData(userID)...)
	selfCert := testRSASignature(t, target, targetKey, SigCertPositive, signedData, hashed, nil)

	targetEntity := &Entity{
		PrimaryKey: targetKey,
		Identities: []*Identity{{
			UserID:     string(userID),
			Signatures: []*Signature{selfCert},
		}},
	}
	revocation := testRevocationSignature(t, targetKey, revokerKey, revoker, true)
	ring := NewKeyring(&Entity{PrimaryKey: revokerKey})
	if err := VerifyKeyRevocation(targetEntity, revocation, ring); err != nil {
		t.Fatalf("user-ID certification designation failed: %v", err)
	}
}

func TestEd25519DesignatedRevocation(t *testing.T) {
	targetPub, targetPriv := testEd25519Key(t)
	revokerPub, revokerPriv := testEd25519Key(t)
	targetKey := testEd25519PublicKey(t, targetPub)
	revokerKey := testEd25519PublicKey(t, revokerPub)

	selfSig := testEd25519Signature(t, targetPriv, targetKey, SigDirectKey,
		canonicalKeyPrefix(targetKey.Version, targetKey.Body), testDesignationSubpacket(revokerKey))
	revocation := testEd25519Signature(t, revokerPriv, revokerKey, SigKeyRevocation,
		canonicalKeyPrefix(targetKey.Version, targetKey.Body), nil)
	targetEntity := &Entity{PrimaryKey: targetKey, DirectSignatures: []*Signature{selfSig}}
	ring := NewKeyring(&Entity{PrimaryKey: revokerKey})

	if err := VerifyKeyRevocation(targetEntity, revocation, ring); err != nil {
		t.Fatalf("Ed25519 designated revocation failed: %v", err)
	}
}

func TestECDSADesignatedRevocation(t *testing.T) {
	targetPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	revokerPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	targetKey := testECDSAPublicKey(t, &targetPriv.PublicKey)
	revokerKey := testECDSAPublicKey(t, &revokerPriv.PublicKey)

	selfSig := testECDSASignature(t, targetPriv, targetKey, SigDirectKey,
		canonicalKeyPrefix(targetKey.Version, targetKey.Body), testDesignationSubpacket(revokerKey))
	revocation := testECDSASignature(t, revokerPriv, revokerKey, SigKeyRevocation,
		canonicalKeyPrefix(targetKey.Version, targetKey.Body), nil)
	targetEntity := &Entity{PrimaryKey: targetKey, DirectSignatures: []*Signature{selfSig}}
	ring := NewKeyring(&Entity{PrimaryKey: revokerKey})

	if err := VerifyKeyRevocation(targetEntity, revocation, ring); err != nil {
		t.Fatalf("ECDSA designated revocation failed: %v", err)
	}
}

func TestArmoredKeyring(t *testing.T) {
	target, targetKey := testRSAKey(t)
	revoker, revokerKey := testRSAKey(t)
	selfSig := testSelfSignature(t, targetKey, targetKey, target, revokerKey, true)
	revocation := testRevocationSignature(t, targetKey, revokerKey, revoker, true)

	binaryData := testPacket(6, targetKey.Body)
	binaryData = append(binaryData, testPacket(2, selfSig.Body)...)
	binaryData = append(binaryData, testPacket(6, revokerKey.Body)...)

	var armor bytes.Buffer
	armor.WriteString("-----BEGIN PGP PUBLIC KEY BLOCK-----\n\n")
	encoded := base64.StdEncoding.EncodeToString(binaryData)
	for len(encoded) > 64 {
		armor.WriteString(encoded[:64])
		armor.WriteByte('\n')
		encoded = encoded[64:]
	}
	armor.WriteString(encoded)
	armor.WriteByte('\n')
	checksum := crc24(binaryData)
	armor.WriteString("=" + base64.StdEncoding.EncodeToString([]byte{byte(checksum >> 16), byte(checksum >> 8), byte(checksum)}) + "\n")
	armor.WriteString("-----END PGP PUBLIC KEY BLOCK-----\n")

	ring, err := ParseKeyring(armor.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	targetEntity := ring.LookupByFingerprint(targetKey.Fingerprint)
	if targetEntity == nil {
		t.Fatal("armored target not found")
	}
	if err := VerifyKeyRevocation(targetEntity, revocation, ring); err != nil {
		t.Fatalf("armored keyring revocation failed: %v", err)
	}
}

func testRSAKey(t *testing.T) (*rsa.PrivateKey, *Key) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	key, err := parsePublicKeyPacket(6, testRSAPublicBody(t, &priv.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	return priv, key
}

func testEd25519Key(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func testEd25519PublicKey(t *testing.T, pub ed25519.PublicKey) *Key {
	t.Helper()
	body := []byte{4, 0, 0, 0, 0, byte(PubKeyEdDSA), 9}
	binary.BigEndian.PutUint32(body[1:5], 1700000000)
	body = append(body, []byte{0x2b, 0x06, 0x01, 0x04, 0x01, 0xda, 0x47, 0x0f, 0x01}...)
	point := append([]byte{0x40}, pub...)
	body = append(body, testMPI(new(big.Int).SetBytes(point))...)
	key, err := parsePublicKeyPacket(6, body)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func testECDSAPublicKey(t *testing.T, pub *ecdsa.PublicKey) *Key {
	t.Helper()
	var oid []byte
	switch pub.Curve {
	case elliptic.P256():
		oid = []byte{0x2a, 0x86, 0x48, 0xce, 0x3d, 0x03, 0x01, 0x07}
	case elliptic.P384():
		oid = []byte{0x2b, 0x81, 0x04, 0x00, 0x22}
	case elliptic.P521():
		oid = []byte{0x2b, 0x81, 0x04, 0x00, 0x23}
	default:
		t.Fatal("unsupported test curve")
	}
	body := []byte{4, 0, 0, 0, 0, byte(PubKeyECDSA), byte(len(oid))}
	binary.BigEndian.PutUint32(body[1:5], 1700000000)
	body = append(body, oid...)
	point := elliptic.Marshal(pub.Curve, pub.X, pub.Y)
	body = append(body, testMPI(new(big.Int).SetBytes(point))...)
	key, err := parsePublicKeyPacket(6, body)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func testMPI(value *big.Int) []byte {
	bits := value.BitLen()
	data := make([]byte, 2+(bits+7)/8)
	binary.BigEndian.PutUint16(data[:2], uint16(bits))
	value.FillBytes(data[2:])
	return data
}

func testPacket(tag byte, body []byte) []byte {
	header := []byte{0xc0 | tag}
	n := len(body)
	switch {
	case n < 192:
		header = append(header, byte(n))
	case n < 8384:
		n -= 192
		header = append(header, byte(192+(n>>8)), byte(n))
	default:
		header = append(header, 255, 0, 0, 0, 0)
		binary.BigEndian.PutUint32(header[len(header)-4:], uint32(n))
	}
	return append(header, body...)
}

func testSubpacket(typ byte, data []byte) []byte {
	contentLen := 1 + len(data)
	var header []byte
	switch {
	case contentLen < 192:
		header = []byte{byte(contentLen)}
	case contentLen < 8384:
		n := contentLen - 192
		header = []byte{byte(192 + (n >> 8)), byte(n)}
	default:
		header = []byte{255, 0, 0, 0, 0}
		binary.BigEndian.PutUint32(header[1:], uint32(contentLen))
	}
	return append(append(header, typ), data...)
}

func testDesignationSubpacket(revoker *Key) []byte {
	data := append([]byte{0x80, byte(revoker.Algorithm)}, revoker.Fingerprint...)
	return testSubpacket(subpacketRevocationKey, data)
}

func testIssuerSubpackets(key *Key, includeFingerprint bool) []byte {
	var out []byte
	out = append(out, testSubpacket(subpacketIssuerKeyID, key.KeyID)...)
	if includeFingerprint {
		data := append([]byte{key.Version}, key.Fingerprint...)
		out = append(out, testSubpacket(subpacketIssuerFingerprint, data)...)
	}
	return out
}

func testSelfSignature(t *testing.T, signedKey, signerKey *Key, signedBy *rsa.PrivateKey, revokerKey *Key, hashedDesignation bool) *Signature {
	t.Helper()
	hashed := testSubpacket(subpacketSignatureCreationTime, []byte{0, 0, 0, 0})
	hashed = append(hashed, testIssuerSubpackets(signerKey, true)...)
	designation := testDesignationSubpacket(revokerKey)
	var unhashed []byte
	if hashedDesignation {
		hashed = append(hashed, designation...)
	} else {
		unhashed = append(unhashed, designation...)
	}
	signedData := canonicalKeyPrefix(signedKey.Version, signedKey.Body)
	sig := testRSASignature(t, signedBy, signerKey, SigDirectKey, signedData, hashed, unhashed)
	return sig
}

func testRevocationSignature(t *testing.T, targetKey, signerKey *Key, signer *rsa.PrivateKey, includeFingerprint bool) *Signature {
	t.Helper()
	hashed := testSubpacket(subpacketSignatureCreationTime, []byte{0, 0, 0, 0})
	hashed = append(hashed, testIssuerSubpackets(signerKey, includeFingerprint)...)
	signedData := canonicalKeyPrefix(targetKey.Version, targetKey.Body)
	return testRSASignature(t, signer, signerKey, SigKeyRevocation, signedData, hashed, nil)
}

func testRSAPublicBody(t *testing.T, pub *rsa.PublicKey) []byte {
	t.Helper()
	body := []byte{4, 0, 0, 0, 0, byte(PubKeyRSAEncryptSign)}
	binary.BigEndian.PutUint32(body[1:5], 1700000000)
	body = append(body, testMPI(pub.N)...)
	body = append(body, testMPI(big.NewInt(int64(pub.E)))...)
	return body
}

func testRSASignature(t *testing.T, priv *rsa.PrivateKey, key *Key, sigType SignatureType, signedData, hashed, unhashed []byte) *Signature {
	t.Helper()
	header := []byte{4, byte(sigType), byte(PubKeyRSAEncryptSign), byte(HashSHA256)}
	header = append(header, byte(len(hashed)>>8), byte(len(hashed)))
	header = append(header, hashed...)
	trailer := []byte{4, 0xff, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(trailer[2:], uint32(len(header)))

	input := append(append(append([]byte(nil), signedData...), header...), trailer...)
	digest := sha256.Sum256(input)
	signature, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}

	body := append([]byte(nil), header...)
	body = append(body, byte(len(unhashed)>>8), byte(len(unhashed)))
	body = append(body, unhashed...)
	body = append(body, digest[0], digest[1])
	body = append(body, testMPI(new(big.Int).SetBytes(signature))...)

	sig, err := parseSignaturePacket(body)
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

func testEd25519Signature(t *testing.T, priv ed25519.PrivateKey, key *Key, sigType SignatureType, signedData []byte, extraHashed []byte) *Signature {
	t.Helper()
	hashed := testSubpacket(subpacketSignatureCreationTime, []byte{0, 0, 0, 0})
	hashed = append(hashed, testIssuerSubpackets(key, true)...)
	hashed = append(hashed, extraHashed...)
	header := []byte{4, byte(sigType), byte(PubKeyEdDSA), byte(HashSHA256)}
	header = append(header, byte(len(hashed)>>8), byte(len(hashed)))
	header = append(header, hashed...)
	trailer := []byte{4, 0xff, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(trailer[2:], uint32(len(header)))
	input := append(append(append([]byte(nil), signedData...), header...), trailer...)
	digest := sha256.Sum256(input)
	signature := ed25519.Sign(priv, digest[:])
	if len(signature) != ed25519.SignatureSize {
		t.Fatal("unexpected Ed25519 signature size")
	}

	body := append([]byte(nil), header...)
	body = append(body, 0, 0)
	body = append(body, digest[0], digest[1])
	body = append(body, testMPI(new(big.Int).SetBytes(signature[:32]))...)
	body = append(body, testMPI(new(big.Int).SetBytes(signature[32:]))...)
	sig, err := parseSignaturePacket(body)
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

func testECDSASignature(t *testing.T, priv *ecdsa.PrivateKey, key *Key, sigType SignatureType, signedData []byte, extraHashed []byte) *Signature {
	t.Helper()
	hashed := testSubpacket(subpacketSignatureCreationTime, []byte{0, 0, 0, 0})
	hashed = append(hashed, testIssuerSubpackets(key, true)...)
	hashed = append(hashed, extraHashed...)
	header := []byte{4, byte(sigType), byte(PubKeyECDSA), byte(HashSHA256)}
	header = append(header, byte(len(hashed)>>8), byte(len(hashed)))
	header = append(header, hashed...)
	trailer := []byte{4, 0xff, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(trailer[2:], uint32(len(header)))
	input := append(append(append([]byte(nil), signedData...), header...), trailer...)
	digest := sha256.Sum256(input)
	r, s, err := ecdsa.Sign(rand.Reader, priv, digest[:])
	if err != nil {
		t.Fatal(err)
	}

	body := append([]byte(nil), header...)
	body = append(body, 0, 0)
	body = append(body, digest[0], digest[1])
	body = append(body, testMPI(r)...)
	body = append(body, testMPI(s)...)
	sig, err := parseSignaturePacket(body)
	if err != nil {
		t.Fatal(err)
	}
	return sig
}
