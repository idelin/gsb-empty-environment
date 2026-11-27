package webauthn

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"
)

func encodeCOSEKey(publicKey *ecdsa.PublicKey) []byte {
	curveID := int64(coseCrvP256)
	coordLen := 32
	algorithm := int64(algES256)
	switch publicKey.Curve {
	case elliptic.P384():
		curveID = coseCrvP384
		coordLen = 48
		algorithm = algES384
	case elliptic.P521():
		curveID = coseCrvP521
		coordLen = 66
		algorithm = algES512
	}
	xFixed := make([]byte, coordLen)
	yFixed := make([]byte, coordLen)
	copy(xFixed[coordLen-len(publicKey.X.Bytes()):], publicKey.X.Bytes())
	copy(yFixed[coordLen-len(publicKey.Y.Bytes()):], publicKey.Y.Bytes())

	// Canonical map key order: 1, 3, -1, -2, -3.
	return encMap(
		mapEntry{intKey(coseKty), encInt(coseKtyEC2)},
		mapEntry{intKey(coseAlg), encInt(algorithm)},
		mapEntry{intKey(coseCrv), encInt(curveID)},
		mapEntry{intKey(coseX), encBytes(xFixed)},
		mapEntry{intKey(coseY), encBytes(yFixed)},
	)
}

type authDataOptions struct {
	rpIDHash   []byte
	flags      byte
	signCount  uint32
	aaguid     AAGUID
	credID     []byte
	coseKey    []byte
	extensions []byte
}

func buildAuthData(opts authDataOptions) []byte {
	out := append([]byte(nil), opts.rpIDHash...)
	out = append(out, opts.flags)
	count := make([]byte, 4)
	binary.BigEndian.PutUint32(count, opts.signCount)
	out = append(out, count...)

	if opts.flags&flagAT != 0 {
		out = append(out, opts.aaguid[:]...)
		lenBuf := make([]byte, 2)
		binary.BigEndian.PutUint16(lenBuf, uint16(len(opts.credID)))
		out = append(out, lenBuf...)
		out = append(out, opts.credID...)
		out = append(out, opts.coseKey...)
	}
	if opts.flags&flagED != 0 && opts.extensions != nil {
		out = append(out, opts.extensions...)
	}
	return out
}

type registrationFixture struct {
	rpID           string
	origin         string
	challenge      []byte
	aaguid         AAGUID
	credentialKey  *ecdsa.PrivateKey
	authData       []byte
	clientDataJSON []byte
	policy         *Policy
}

func newRegistrationFixture(t *testing.T) *registrationFixture {
	t.Helper()
	rpID := "login.example.com"
	origin := "https://login.example.com"
	challenge := []byte("one-time-server-challenge-0001")

	var aaguid AAGUID
	for i := range aaguid {
		aaguid[i] = byte(i + 1)
	}
	credentialKey := mustGenerateECKey(t)
	rpIDHash := sha256.Sum256([]byte(rpID))
	credID := []byte("test-credential-identifier-128b!!")

	authData := buildAuthData(authDataOptions{
		rpIDHash:  rpIDHash[:],
		flags:     flagUP | flagAT,
		signCount: 42,
		aaguid:    aaguid,
		credID:    credID,
		coseKey:   encodeCOSEKey(&credentialKey.PublicKey),
	})
	clientDataJSON := buildClientData(t, challenge, origin, false)

	policy := &Policy{
		RPID:           rpID,
		Origin:         origin,
		TrustAnchors:   x509.NewCertPool(),
		AllowedAAGUIDs: []AAGUID{aaguid},
	}
	return &registrationFixture{
		rpID:           rpID,
		origin:         origin,
		challenge:      challenge,
		aaguid:         aaguid,
		credentialKey:  credentialKey,
		authData:       authData,
		clientDataJSON: clientDataJSON,
		policy:         policy,
	}
}

func buildClientData(t *testing.T, challenge []byte, origin string, crossOrigin bool) []byte {
	t.Helper()
	payload := map[string]any{
		"type":      expectedClientDataType,
		"challenge": base64.RawURLEncoding.EncodeToString(challenge),
		"origin":    origin,
	}
	if crossOrigin {
		payload["crossOrigin"] = true
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal client data: %v", err)
	}
	return raw
}

func (f *registrationFixture) signedPayload() []byte {
	hash := sha256.Sum256(f.clientDataJSON)
	return append(append([]byte(nil), f.authData...), hash[:]...)
}

func ecdsaSignASN1(t *testing.T, signer *ecdsa.PrivateKey, message []byte) []byte {
	t.Helper()
	digest := sha256.Sum256(message)
	signature, err := ecdsa.SignASN1(rand.Reader, signer, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signature
}

func (f *registrationFixture) attestationObject(format string, attStmt []byte) []byte {
	// Canonical top-level order: fmt (3), attStmt (7), authData (8).
	return encMap(
		mapEntry{textKey("fmt"), encText(format)},
		mapEntry{textKey("attStmt"), attStmt},
		mapEntry{textKey("authData"), encBytes(f.authData)},
	)
}

func (f *registrationFixture) buildPackedFullObject(t *testing.T, chainDER [][]byte, signer *ecdsa.PrivateKey) []byte {
	t.Helper()
	items := make([][]byte, len(chainDER))
	for i, der := range chainDER {
		items[i] = encBytes(der)
	}
	attStmt := encMap(
		mapEntry{textKey("alg"), encInt(algES256)},
		mapEntry{textKey("sig"), encBytes(ecdsaSignASN1(t, signer, f.signedPayload()))},
		mapEntry{textKey("x5c"), encArray(items...)},
	)
	return f.attestationObject("packed", attStmt)
}

func (f *registrationFixture) buildPackedSelfObject(t *testing.T) []byte {
	t.Helper()
	attStmt := encMap(
		mapEntry{textKey("alg"), encInt(algES256)},
		mapEntry{textKey("sig"), encBytes(ecdsaSignASN1(t, f.credentialKey, f.signedPayload()))},
	)
	return f.attestationObject("packed", attStmt)
}

func (f *registrationFixture) buildNoneObject() []byte {
	return f.attestationObject("none", encMap())
}
