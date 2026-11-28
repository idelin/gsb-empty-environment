package pkcs12verify

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/asn1"
	"hash"
)

// allowedLegacyDigest maps the RFC 7292 MacData MAC digest OIDs on the allow
// list to hash constructors. MD5 and SHA-1 are intentionally absent.
var allowedLegacyDigest = map[string]func() hash.Hash{
	oidSHA256.String(): sha256.New,
	oidSHA384.String(): sha512.New384,
	oidSHA512.String(): sha512.New,
}

// verifyMAC performs the MacData policy checks and authenticates the raw
// authSafe.content octets. Both the legacy PKCS#12 MAC and PBMAC1 are
// supported; comparison is always constant time.
func verifyMAC(mac macData, algoID algorithmIdentifier, authSafeContent []byte, password string, opts Options) error {
	if algoID.Algorithm.Equal(oidPBMAC1) {
		return verifyPBMAC1(mac, algoID, authSafeContent, password, opts)
	}
	return verifyLegacyMAC(mac, algoID, authSafeContent, password, opts)
}

// verifyLegacyMAC handles the original PKCS#12 integrity mode:
// DigestInfo carrying the MAC digest, salt and iterations in MacData and the
// RFC 7292 Appendix B KDF (ID = 3) for the MAC key.
func verifyLegacyMAC(mac macData, algoID algorithmIdentifier, authSafeContent []byte, password string, opts Options) error {
	if name, weak := weakAlgorithms[algoID.Algorithm.String()]; weak {
		return wrap(ErrDigestAlgorithm, "weak MAC algorithm: "+name)
	}
	newHash, ok := allowedLegacyDigest[algoID.Algorithm.String()]
	if !ok {
		return wrap(ErrDigestAlgorithm, "MAC digest OID not on SHA-2 allow list: "+algoID.Algorithm.String())
	}

	// MacData.mac.digestAlgorithm and the outer algorithm choice must agree.
	if !mac.Mac.Algorithm.Algorithm.Equal(algoID.Algorithm) {
		return wrap(ErrDigestAlgorithm, "MacData digest algorithms disagree")
	}

	if len(mac.MacSalt) < opts.MinSaltLength {
		return wrap(ErrSaltTooShort, "MAC salt too short")
	}
	if mac.Iterations < opts.MinIterations || mac.Iterations > opts.MaxIterations {
		return wrap(ErrIterations, "MAC iteration count outside policy window")
	}

	h := newHash()
	expectedLen := h.Size()
	if len(mac.Mac.Digest) != expectedLen {
		return wrap(ErrIntegrity, "MAC digest length mismatch")
	}

	key := pkcs12KDF(password, mac.MacSalt, 3, mac.Iterations, expectedLen, newHash)
	macValue := hmac.New(newHash, key)
	macValue.Write(authSafeContent)
	expected := macValue.Sum(nil)

	if !hmac.Equal(expected, mac.Mac.Digest) {
		return wrap(ErrIntegrity, "authSafe MAC mismatch")
	}
	return nil
}

// verifyPBMAC1 handles RFC 9579 PBMAC1: PBKDF2-HMAC-SHA256 key derivation
// followed by HMAC-SHA-256 over the authSafe content. The password is an
// octet string (UTF-8), with no BMPString transformation.
func verifyPBMAC1(mac macData, algoID algorithmIdentifier, authSafeContent []byte, password string, opts Options) error {
	// In PBMAC1 all key derivation parameters live inside the AlgorithmIdentifier;
	// the legacy MacData salt/iteration fields must not be present.
	if len(mac.MacSalt) != 0 || mac.Iterations != 0 {
		return wrap(ErrMalformed, "PBMAC1 must not carry legacy MacData salt or iterations")
	}

	var params pbmac1Params
	if err := unmarshalStrict(algoID.Parameters.FullBytes, &params); err != nil {
		return wrap(ErrMalformed, "invalid PBMAC1 parameters: "+err.Error())
	}

	if !params.KeyDerivationFunc.Algorithm.Equal(oidPBKDF2) {
		return wrap(ErrUnsupportedCipher, "PBMAC1 requires PBKDF2 key derivation")
	}
	if err := verifyPBKDF2Params(params.KeyDerivationFunc, opts); err != nil {
		return err
	}

	if !params.MessageAuthScheme.Algorithm.Equal(oidHMACWithSHA256) {
		if name, weak := weakAlgorithms[params.MessageAuthScheme.Algorithm.String()]; weak {
			return wrap(ErrDigestAlgorithm, "weak PBMAC1 scheme: "+name)
		}
		return wrap(ErrDigestAlgorithm, "PBMAC1 requires hmacWithSHA256")
	}

	var kdfParams pbkdf2Params
	if err := unmarshalStrict(params.KeyDerivationFunc.Parameters.FullBytes, &kdfParams); err != nil {
		return wrap(ErrMalformed, "invalid PBMAC1 PBKDF2 parameters")
	}
	keyLength := kdfParams.KeyLength
	if keyLength == 0 {
		keyLength = sha256.Size
	}
	if keyLength != sha256.Size {
		return wrap(ErrUnsupportedCipher, "PBMAC1 requires a 256-bit MAC key")
	}

	if len(mac.Mac.Digest) != sha256.Size {
		return wrap(ErrIntegrity, "PBMAC1 digest length mismatch")
	}

	key := pbkdf2HMAC([]byte(password), kdfParams.Salt.Bytes, kdfParams.IterationCount, keyLength, sha256.New)
	macValue := hmac.New(sha256.New, key)
	macValue.Write(authSafeContent)

	if !hmac.Equal(macValue.Sum(nil), mac.Mac.Digest) {
		return wrap(ErrIntegrity, "PBMAC1 authSafe MAC mismatch")
	}
	return nil
}

// macAlgorithmIdentifier extracts the effective MAC AlgorithmIdentifier. The
// legacy PKCS#12 choice is DigestInfo; PBMAC1 places the OID and parameters on
// MacData.mac itself.
func macAlgorithmIdentifier(mac macData) (algorithmIdentifier, error) {
	if len(mac.Mac.Algorithm.Algorithm) == 0 {
		return algorithmIdentifier{}, wrap(ErrMalformed, "missing MAC algorithm identifier")
	}
	if mac.Mac.Algorithm.Algorithm.Equal(oidPBMAC1) {
		return mac.Mac.Algorithm, nil
	}
	// Ensure the digest algorithm parameters are absent or NULL.
	params := mac.Mac.Algorithm.Parameters
	if len(params.FullBytes) != 0 && params.Tag != asn1.TagNull {
		return algorithmIdentifier{}, wrap(ErrMalformed, "unexpected digest algorithm parameters")
	}
	return mac.Mac.Algorithm, nil
}
