package knownhosts

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"math/big"
)

type testPublicKey struct {
	keyType string
	blob    []byte
}

func (k testPublicKey) Type() string    { return k.keyType }
func (k testPublicKey) Marshal() []byte { return k.blob }

func putString(out, data []byte) []byte {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(data)))
	out = append(out, length[:]...)
	return append(out, data...)
}

func encodeB64(key testPublicKey) string {
	return base64.StdEncoding.EncodeToString(key.blob)
}

func encodeLine(host string, key testPublicKey) string {
	return host + " " + encodeB64(key)
}

func putUint32(out []byte, value uint32) []byte {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], value)
	return append(out, buf[:]...)
}

func putUint64(out []byte, value uint64) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], value)
	return append(out, buf[:]...)
}

func marshalEd25519(pub ed25519.PublicKey) []byte {
	out := putString(nil, []byte("ssh-ed25519"))
	return putString(out, pub)
}

func marshalRSA(pub *rsa.PublicKey) []byte {
	out := putString(nil, []byte("ssh-rsa"))
	out = putString(out, big.NewInt(int64(pub.E)).Bytes())
	out = putString(out, pub.N.Bytes())
	return out
}

func marshalECDSA(pub *ecdsa.PublicKey) []byte {
	curveName := map[elliptic.Curve]string{
		elliptic.P256(): "nistp256",
		elliptic.P384(): "nistp384",
		elliptic.P521(): "nistp521",
	}[pub.Curve]
	out := putString(nil, []byte("ecdsa-sha2-"+curveName))
	out = putString(out, []byte(curveName))
	out = putString(out, elliptic.MarshalCompressed(pub.Curve, pub.X, pub.Y))
	return out
}

func ed25519KeyPair() (ed25519.PublicKey, ed25519.PrivateKey) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	return pub, priv
}

func rsaKeyPair() *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return key
}

func ecdsaKeyPair() *ecdsa.PrivateKey {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	return key
}

// buildEd25519Certificate assembles a signed ssh-ed25519-cert-v01 host
// certificate. An empty principals list means the cert is valid for every
// host, matching OpenSSH behavior.
func buildEd25519Certificate(hostKey ed25519.PublicKey, caKey ed25519.PrivateKey, caPub ed25519.PublicKey, principals []string, tamper bool) testPublicKey {
	return buildEd25519CertificateWindow(hostKey, caKey, caPub, principals, tamper, 0, ^uint64(0))
}

// buildEd25519CertificateWindow is buildEd25519Certificate with an explicit
// validity interval given in Unix seconds.
func buildEd25519CertificateWindow(hostKey ed25519.PublicKey, caKey ed25519.PrivateKey, caPub ed25519.PublicKey, principals []string, tamper bool, validAfter, validBefore uint64) testPublicKey {
	certType := "ssh-ed25519-cert-v01@openssh.com"

	var body []byte
	body = putString(body, []byte(certType))
	body = putString(body, []byte("nonce-0123456789"))
	body = putString(body, hostKey)
	body = putUint64(body, 1234) // serial
	body = putUint32(body, hostCertificateType)
	body = putString(body, []byte("test-host-key")) // key id

	var principalList []byte
	principalList = putUint32(principalList, uint32(len(principals)))
	for _, principal := range principals {
		principalList = putString(principalList, []byte(principal))
	}
	body = putString(body, principalList)
	body = putUint64(body, validAfter)
	body = putUint64(body, validBefore)
	body = putString(body, []byte{}) // critical options
	body = putString(body, []byte{}) // extensions
	body = putString(body, []byte{}) // reserved
	body = putString(body, marshalEd25519(caPub))

	signedBody := body
	if tamper {
		// Flip a bit inside the public key portion so the signature breaks.
		signedBody = append([]byte(nil), body...)
		signedBody[len(certType)+4+16+4] ^= 0x01
	}
	signature := ed25519.Sign(caKey, signedBody)

	var sigField []byte
	sigField = putString(sigField, []byte("ssh-ed25519"))
	sigField = putString(sigField, signature)
	body = putString(body, sigField)

	return testPublicKey{keyType: certType, blob: body}
}

// buildRSACertificate assembles a signed ssh-rsa-cert-v01 host certificate
// with an rsa-sha2-256 signature.
func buildRSACertificate(hostKey *rsa.PublicKey, caKey *rsa.PrivateKey, principals []string) testPublicKey {
	certType := "ssh-rsa-cert-v01@openssh.com"

	var body []byte
	body = putString(body, []byte(certType))
	body = putString(body, []byte("nonce-0123456789"))
	body = putString(body, big.NewInt(int64(hostKey.E)).Bytes())
	body = putString(body, hostKey.N.Bytes())
	body = putUint64(body, 1234)
	body = putUint32(body, hostCertificateType)
	body = putString(body, []byte("test-host-key"))

	var principalList []byte
	principalList = putUint32(principalList, uint32(len(principals)))
	for _, principal := range principals {
		principalList = putString(principalList, []byte(principal))
	}
	body = putString(body, principalList)
	body = putUint64(body, 0)
	body = putUint64(body, ^uint64(0))
	body = putString(body, []byte{})
	body = putString(body, []byte{})
	body = putString(body, []byte{})
	body = putString(body, marshalRSA(&caKey.PublicKey))

	digest := sha256.Sum256(body)
	signature, err := rsa.SignPKCS1v15(rand.Reader, caKey, crypto.SHA256, digest[:])
	if err != nil {
		panic(err)
	}

	var sigField []byte
	sigField = putString(sigField, []byte("rsa-sha2-256"))
	sigField = putString(sigField, signature)
	body = putString(body, sigField)

	return testPublicKey{keyType: certType, blob: body}
}

// buildECDSACertificate assembles a signed ecdsa-sha2-nistp256 host
// certificate.
func buildECDSACertificate(hostKey *ecdsa.PublicKey, caKey *ecdsa.PrivateKey, principals []string) testPublicKey {
	certType := "ecdsa-sha2-nistp256-cert-v01@openssh.com"

	var body []byte
	body = putString(body, []byte(certType))
	body = putString(body, []byte("nonce-0123456789"))
	body = putString(body, []byte("nistp256"))
	body = putString(body, elliptic.MarshalCompressed(hostKey.Curve, hostKey.X, hostKey.Y))
	body = putUint64(body, 1234)
	body = putUint32(body, hostCertificateType)
	body = putString(body, []byte("test-host-key"))

	var principalList []byte
	principalList = putUint32(principalList, uint32(len(principals)))
	for _, principal := range principals {
		principalList = putString(principalList, []byte(principal))
	}
	body = putString(body, principalList)
	body = putUint64(body, 0)
	body = putUint64(body, ^uint64(0))
	body = putString(body, []byte{})
	body = putString(body, []byte{})
	body = putString(body, []byte{})
	body = putString(body, marshalECDSA(&caKey.PublicKey))

	digest := sha256.Sum256(body)
	rawR, rawS, err := ecdsa.Sign(rand.Reader, caKey, digest[:])
	if err != nil {
		panic(err)
	}

	var sigField []byte
	sigField = putString(sigField, []byte("ecdsa-sha2-nistp256"))
	var sigBlob []byte
	sigBlob = putString(sigBlob, rawR.Bytes())
	sigBlob = putString(sigBlob, rawS.Bytes())
	sigField = putString(sigField, sigBlob)
	body = putString(body, sigField)

	return testPublicKey{keyType: certType, blob: body}
}
