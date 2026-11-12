package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"time"
)

// certSpec describes a certificate to mint in tests.
type certSpec struct {
	serial          uint64
	certType        uint32
	keyID           string
	principals      []string
	validAfter      time.Time
	validBefore     time.Time
	criticalOptions map[string]string
	extensions      map[string]string
	// signKey overrides the CA used to sign (nil => use the fixture CA).
	signKey crypto.Signer
}

// testCA bundles a CA private key with its parsed public representation.
type testCA struct {
	signer crypto.Signer
	pub    PublicKey
}

func newEd25519CA() *testCA {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	wire, err := marshalPublicKey(pub)
	if err != nil {
		panic(err)
	}
	parsed, err := parseKeyBlob(&wireReader{full: wire})
	if err != nil {
		panic(err)
	}
	return &testCA{signer: priv, pub: parsed}
}

func newRSACA() *testCA {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	wire, err := marshalPublicKey(&priv.PublicKey)
	if err != nil {
		panic(err)
	}
	parsed, err := parseKeyBlob(&wireReader{full: wire})
	if err != nil {
		panic(err)
	}
	return &testCA{signer: priv, pub: parsed}
}

func newECDSACA() *testCA {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	wire, err := marshalPublicKey(&priv.PublicKey)
	if err != nil {
		panic(err)
	}
	parsed, err := parseKeyBlob(&wireReader{full: wire})
	if err != nil {
		panic(err)
	}
	return &testCA{signer: priv, pub: parsed}
}

// mint builds the full certificate wire blob, signs it with the CA, and
// appends the signature field exactly as an SSH CA would.
func (ca *testCA) mint(spec certSpec) []byte {
	if spec.certType == 0 {
		spec.certType = CertTypeUser
	}

	caKey, err := marshalPublicKey(publicOf(ca.signer))
	if err != nil {
		panic(err)
	}
	certTypeName := certTypeFor(caKey)

	// The embedded leaf key type must match the certificate's algorithm
	// family (e.g. an ssh-rsa-cert embeds an ssh-rsa key).
	leafPub, err := newLeafKey(certTypeName)
	if err != nil {
		panic(err)
	}
	leafWire, err := marshalPublicKey(leafPub)
	if err != nil {
		panic(err)
	}

	out := writeString(nil, []byte(certTypeName))
	out = writeString(out, []byte("nonce-for-testing"))
	out = append(out, leafWire...)
	out = writeUint32(out, spec.serial)
	out = writeUint32(out, uint64(spec.certType))
	out = writeString(out, []byte(spec.keyID))
	out = marshalNameList(out, spec.principals)
	out = writeUint32(out, unixOrZero(spec.validAfter))
	out = writeUint32(out, unixOrZero(spec.validBefore))
	out = marshalOptions(out, spec.criticalOptions)
	out = marshalOptions(out, spec.extensions)
	out = writeString(out, nil) // reserved

	signer := ca.signer
	if spec.signKey != nil {
		signer = spec.signKey
	}
	sigType, sig := signBlob(signer, certTypeName, out)
	sigBlob := writeString(nil, []byte(sigType))
	sigBlob = writeString(sigBlob, sig)
	out = writeString(out, sigBlob)
	return out
}

func newLeafKey(certTypeName string) (any, error) {
	switch certTypeName {
	case "ssh-ed25519-cert-v01@openssh.com":
		pub, _, err := ed25519.GenerateKey(rand.Reader)
		return pub, err
	case "ssh-rsa-cert-v01@openssh.com":
		priv, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return nil, err
		}
		return &priv.PublicKey, nil
	case "ecdsa-sha2-nistp256-cert-v01@openssh.com":
		priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}
		return &priv.PublicKey, nil
	case "ecdsa-sha2-nistp384-cert-v01@openssh.com":
		priv, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
		if err != nil {
			return nil, err
		}
		return &priv.PublicKey, nil
	case "ecdsa-sha2-nistp521-cert-v01@openssh.com":
		priv, err := ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
		if err != nil {
			return nil, err
		}
		return &priv.PublicKey, nil
	default:
		panic("unsupported cert type")
	}
}

func publicOf(signer crypto.Signer) any {
	switch k := signer.(type) {
	case ed25519.PrivateKey:
		return k.Public()
	case *rsa.PrivateKey:
		return &k.PublicKey
	case *ecdsa.PrivateKey:
		return &k.PublicKey
	default:
		panic("unsupported signer")
	}
}

func certTypeFor(keyWire []byte) string {
	r := &wireReader{full: keyWire}
	name := r.string()
	switch name {
	case "ssh-rsa":
		return "ssh-rsa-cert-v01@openssh.com"
	case "ssh-ed25519":
		return "ssh-ed25519-cert-v01@openssh.com"
	case "ecdsa-sha2-nistp256":
		return "ecdsa-sha2-nistp256-cert-v01@openssh.com"
	case "ecdsa-sha2-nistp384":
		return "ecdsa-sha2-nistp384-cert-v01@openssh.com"
	case "ecdsa-sha2-nistp521":
		return "ecdsa-sha2-nistp521-cert-v01@openssh.com"
	default:
		panic("unsupported CA key")
	}
}

func unixOrZero(t time.Time) uint64 {
	if t.IsZero() {
		return 0
	}
	return uint64(t.UTC().Unix())
}

func marshalNameList(dst []byte, names []string) []byte {
	joined := ""
	for i, n := range names {
		if i > 0 {
			joined += ","
		}
		joined += n
	}
	return writeString(dst, []byte(joined))
}

// marshalOptions emits the inner option payload then length-prefixes it.
func marshalOptions(dst []byte, opts map[string]string) []byte {
	if len(opts) == 0 {
		return writeString(dst, nil)
	}
	inner := writeUint32(nil, uint64(len(opts)))
	// Deterministic order keeps fixtures reviewable.
	for _, name := range sortedKeys(opts) {
		inner = writeString(inner, []byte(name))
		inner = writeString(inner, []byte(opts[name]))
	}
	return writeString(dst, inner)
}

func signBlob(signer crypto.Signer, certTypeName string, toSign []byte) (string, []byte) {
	switch key := signer.(type) {
	case ed25519.PrivateKey:
		sig := ed25519.Sign(key, toSign)
		return "ssh-ed25519", sig

	case *rsa.PrivateKey:
		h := sha256.Sum256(toSign)
		sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, h[:])
		if err != nil {
			panic(err)
		}
		return "rsa-sha2-256", sig

	case *ecdsa.PrivateKey:
		var digest []byte
		var alg string
		switch certTypeName {
		case "ecdsa-sha2-nistp256-cert-v01@openssh.com":
			h := sha256.Sum256(toSign)
			digest, alg = h[:], "ecdsa-sha2-nistp256"
		case "ecdsa-sha2-nistp384-cert-v01@openssh.com":
			h := sha512.Sum384(toSign)
			digest, alg = h[:], "ecdsa-sha2-nistp384"
		case "ecdsa-sha2-nistp521-cert-v01@openssh.com":
			h := sha512.Sum512(toSign)
			digest, alg = h[:], "ecdsa-sha2-nistp521"
		default:
			panic("bad ecdsa curve")
		}
		r, s, err := ecdsa.Sign(rand.Reader, key, digest)
		if err != nil {
			panic(err)
		}
		blob := writeMpint(nil, r)
		blob = writeMpint(blob, s)
		return alg, blob

	default:
		panic("unsupported signer")
	}
}
