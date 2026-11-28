package pkcs12verify

import (
	"crypto/sha256"
	"crypto/sha512"
	"encoding/asn1"
	"hash"
)

// allowedPRF maps PBKDF2-PRF OIDs on the allow list to hash constructors.
// Absence of a PRF means hmacWithSHA1 in PKCS#5 and is rejected by the
// caller rather than defaulted.
var allowedPRF = map[string]func() hash.Hash{
	oidHMACWithSHA256.String(): sha256.New,
	oidHMACWithSHA384.String(): sha512.New384,
	oidHMACWithSHA512.String(): sha512.New,
}

// verifyPBES2 enforces that a pkcs8ShroudedKeyBag uses PBES2 with a PBKDF2
// KDF and AES-256-CBC or AES-256-GCM, applying the same salt and iteration
// policy as the integrity MAC.
func verifyPBES2(algo algorithmIdentifier, opts Options) error {
	if !algo.Algorithm.Equal(oidPBES2) {
		if name, weak := weakAlgorithms[algo.Algorithm.String()]; weak {
			return wrap(ErrWeakCipher, "weak PBE algorithm: "+name)
		}
		return wrap(ErrUnsupportedCipher, "only PBES2 is allowed, got OID "+algo.Algorithm.String())
	}

	var params pbes2Params
	if err := unmarshalStrict(algo.Parameters.FullBytes, &params); err != nil {
		return wrap(ErrMalformed, "invalid PBES2 parameters: "+err.Error())
	}

	if !params.KeyDerivationFunc.Algorithm.Equal(oidPBKDF2) {
		if name, weak := weakAlgorithms[params.KeyDerivationFunc.Algorithm.String()]; weak {
			return wrap(ErrWeakCipher, "weak key derivation: "+name)
		}
		return wrap(ErrUnsupportedCipher, "only PBKDF2 is allowed as PBES2 KDF")
	}

	if err := verifyPBKDF2Params(params.KeyDerivationFunc, opts); err != nil {
		return err
	}

	return verifyEncryptionScheme(params.EncryptionScheme)
}

// verifyPBKDF2Params validates the salt length, iteration window, explicit
// SHA-2 PRF and key length of PBKDF2-params.
func verifyPBKDF2Params(kdf algorithmIdentifier, opts Options) error {
	var params pbkdf2Params
	if err := unmarshalStrict(kdf.Parameters.FullBytes, &params); err != nil {
		return wrap(ErrMalformed, "invalid PBKDF2 parameters: "+err.Error())
	}

	// Salt CHOICE: only the primitive OCTET STRING form is permitted; an
	// AlgorithmIdentifier-based "other source" is out of policy.
	if params.Salt.Tag != asn1.TagOctetString || params.Salt.IsCompound {
		return wrap(ErrUnsupportedCipher, "PBKDF2 saltSource AlgorithmIdentifier is not allowed")
	}
	if len(params.Salt.Bytes) < opts.MinSaltLength {
		return wrap(ErrSaltTooShort, "PBKDF2 salt too short")
	}

	if params.IterationCount < opts.MinIterations || params.IterationCount > opts.MaxIterations {
		return wrap(ErrIterations, "PBKDF2 iteration count outside policy window")
	}

	if len(params.PRF.Algorithm) == 0 {
		return wrap(ErrDigestAlgorithm, "explicit SHA-2 PRF required; implicit hmacWithSHA1 forbidden")
	}
	if name, weak := weakAlgorithms[params.PRF.Algorithm.String()]; weak {
		return wrap(ErrDigestAlgorithm, "weak PBKDF2 PRF: "+name)
	}
	if _, ok := allowedPRF[params.PRF.Algorithm.String()]; !ok {
		return wrap(ErrDigestAlgorithm, "unsupported PBKDF2 PRF OID "+params.PRF.Algorithm.String())
	}
	if params.KeyLength != 0 && params.KeyLength != 32 {
		return wrap(ErrUnsupportedCipher, "only 256-bit AES keys are allowed")
	}
	return nil
}

// verifyEncryptionScheme restricts the PBES2 encryption scheme to
// AES-256-CBC or AES-256-GCM and checks their parameter encodings.
func verifyEncryptionScheme(scheme algorithmIdentifier) error {
	switch {
	case scheme.Algorithm.Equal(oidAES256CBC):
		var iv []byte
		if err := unmarshalStrict(scheme.Parameters.FullBytes, &iv); err != nil {
			return wrap(ErrMalformed, "invalid AES-CBC IV: "+err.Error())
		}
		if len(iv) != 16 {
			return wrap(ErrMalformed, "AES-CBC IV must be 16 octets")
		}
	case scheme.Algorithm.Equal(oidAES256GCM):
		var params gcmParameters
		if len(scheme.Parameters.FullBytes) == 0 {
			return wrap(ErrMalformed, "GCM parameters are required")
		}
		if err := unmarshalStrict(scheme.Parameters.FullBytes, &params); err != nil {
			return wrap(ErrMalformed, "invalid GCM parameters: "+err.Error())
		}
		if len(params.Nonce) < 12 {
			return wrap(ErrMalformed, "GCM nonce must be at least 12 octets")
		}
		if params.ICVLen != 0 && params.ICVLen != 12 && params.ICVLen != 13 && params.ICVLen != 16 {
			return wrap(ErrMalformed, "unsupported GCM ICV length")
		}
	default:
		if name, weak := weakAlgorithms[scheme.Algorithm.String()]; weak {
			return wrap(ErrWeakCipher, "weak encryption scheme: "+name)
		}
		return wrap(ErrUnsupportedCipher, "only AES-256-CBC and AES-256-GCM are allowed")
	}
	return nil
}

// unmarshalStrict validates strict DER on a nested blob before delegating to
// encoding/asn1.
func unmarshalStrict(b []byte, target any) error {
	if err := requireSingleDER(b); err != nil {
		return err
	}
	if _, err := asn1.Unmarshal(b, target); err != nil {
		return wrap(ErrMalformed, err.Error())
	}
	return nil
}
