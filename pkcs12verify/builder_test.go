package pkcs12verify

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/asn1"
)

// pfxBuilder assembles RFC 7292 PFX test vectors from raw DER using only the
// standard library. It supports the exact mutations the security tests need.
type pfxBuilder struct {
	password       string
	iterations     int
	salt           []byte
	macOID         asn1.ObjectIdentifier
	pbmac1         bool
	bags           []safeBag
	withEncData    bool
	omitMac        bool
	tamperAuthSafe bool
}

func newBuilder() *pfxBuilder {
	return &pfxBuilder{
		password:   "correct horse battery staple",
		iterations: DefaultMinIterations,
		salt:       patternBytes(16, 0x11),
		macOID:     oidSHA256,
	}
}

func patternBytes(n int, start byte) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = start + byte(i)
	}
	return out
}

func mustMarshal(v any) []byte {
	b, err := asn1.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func rawFull(b []byte) asn1.RawValue { return asn1.RawValue{FullBytes: b} }

func nullRaw() asn1.RawValue { return asn1.RawValue{FullBytes: []byte{asn1.TagNull, 0x00}} }

func octetRaw(b []byte) asn1.RawValue {
	return asn1.RawValue{Tag: asn1.TagOctetString, Bytes: b}
}

func octetDER(b []byte) []byte { return mustMarshal(octetRaw(b)) }

func explicitContext(tag int, b []byte) asn1.RawValue {
	return asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: tag, IsCompound: true, Bytes: b}
}

// explicitOctet builds the required [0] EXPLICIT OCTET STRING wrapper for
// ContentInfo fields.
func explicitOctet(payload []byte) asn1.RawValue {
	return explicitContext(0, octetDER(payload))
}

// ---- SafeBag constructors -------------------------------------------------

func certBagDER(cert []byte) safeBag {
	inner := mustMarshal(certBag{ID: oidX509Certificate, Data: cert})
	return safeBag{ID: oidCertBag, Value: explicitContext(0, inner)}
}

func shroudedKeyBagDER(algo algorithmIdentifier, ciphertext []byte) safeBag {
	inner := mustMarshal(encryptedPrivateKeyInfo{
		EncryptionAlgorithm: algo,
		EncryptedData:       ciphertext,
	})
	return safeBag{ID: oidPKCS8ShroudedKBag, Value: explicitContext(0, inner)}
}

func plainKeyBagDER(key []byte) safeBag {
	return safeBag{ID: oidKeyBag, Value: explicitContext(0, mustMarshal(octetRaw(key)))}
}

func pbes2Algo(iter int, salt, iv []byte, gcm bool, prfOID asn1.ObjectIdentifier, includePRF bool) algorithmIdentifier {
	params := pbkdf2Params{Salt: octetRaw(salt), IterationCount: iter}
	if includePRF {
		params.PRF = algorithmIdentifier{Algorithm: prfOID, Parameters: nullRaw()}
	}
	kdf := algorithmIdentifier{Algorithm: oidPBKDF2, Parameters: rawFull(mustMarshal(params))}

	var scheme algorithmIdentifier
	if gcm {
		gcmParams := gcmParameters{Nonce: iv}
		scheme = algorithmIdentifier{Algorithm: oidAES256GCM, Parameters: rawFull(mustMarshal(gcmParams))}
	} else {
		scheme = algorithmIdentifier{Algorithm: oidAES256CBC, Parameters: rawFull(mustMarshal(iv))}
	}

	pbes2 := pbes2Params{KeyDerivationFunc: kdf, EncryptionScheme: scheme}
	return algorithmIdentifier{Algorithm: oidPBES2, Parameters: rawFull(mustMarshal(pbes2))}
}

func weakAlgo(oid asn1.ObjectIdentifier) algorithmIdentifier {
	type pbeParams struct {
		Salt       []byte
		Iterations int
	}
	return algorithmIdentifier{
		Algorithm:  oid,
		Parameters: rawFull(mustMarshal(pbeParams{Salt: patternBytes(16, 0x22), Iterations: 2048})),
	}
}

// ---- PFX assembly ---------------------------------------------------------

func (b *pfxBuilder) addCert() *pfxBuilder {
	b.bags = append(b.bags, certBagDER(patternBytes(64, 0x30)))
	return b
}

func (b *pfxBuilder) addShroudedKey(gcm bool) *pfxBuilder {
	ivLen := 16
	if gcm {
		ivLen = 12
	}
	algo := pbes2Algo(b.iterations, patternBytes(16, 0x44), patternBytes(ivLen, 0x55), gcm, oidHMACWithSHA256, true)
	plaintext := patternBytes(48, 0x66)
	ciphertext := encryptForVector(algo, plaintext)
	b.bags = append(b.bags, shroudedKeyBagDER(algo, ciphertext))
	return b
}

func (b *pfxBuilder) addPlainKey() *pfxBuilder {
	b.bags = append(b.bags, plainKeyBagDER(patternBytes(32, 0x77)))
	return b
}

func (b *pfxBuilder) addWeakShroudedKey(oid asn1.ObjectIdentifier) *pfxBuilder {
	b.bags = append(b.bags, shroudedKeyBagDER(weakAlgo(oid), patternBytes(40, 0x88)))
	return b
}

// encryptForVector produces genuine AES-256 ciphertext so the vector is a
// realistic EncryptedPrivateKeyInfo. The validator never decrypts it, but the
// exercise keeps the allowed crypto primitives wired in tests.
func encryptForVector(algo algorithmIdentifier, plaintext []byte) []byte {
	var params pbes2Params
	if err := unmarshalStrict(algo.Parameters.FullBytes, &params); err != nil {
		panic(err)
	}
	var kdf pbkdf2Params
	if err := unmarshalStrict(params.KeyDerivationFunc.Parameters.FullBytes, &kdf); err != nil {
		panic(err)
	}
	key := pbkdf2HMAC([]byte("vector-password"), kdf.Salt.Bytes, kdf.IterationCount, 32, sha256.New)

	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	switch params.EncryptionScheme.Algorithm.String() {
	case oidAES256CBC.String():
		var iv []byte
		if err := unmarshalStrict(params.EncryptionScheme.Parameters.FullBytes, &iv); err != nil {
			panic(err)
		}
		padded := pkcs7Pad(plaintext, block.BlockSize())
		out := make([]byte, len(padded))
		cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, padded)
		return out
	default:
		var gcmP gcmParameters
		if err := unmarshalStrict(params.EncryptionScheme.Parameters.FullBytes, &gcmP); err != nil {
			panic(err)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			panic(err)
		}
		return aead.Seal(nil, gcmP.Nonce, plaintext, nil)
	}
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	pad := blockSize - len(data)%blockSize
	out := make([]byte, len(data)+pad)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(pad)
	}
	return out
}

// Build returns the complete PFX DER and the raw authenticatedSafe content.
func (b *pfxBuilder) Build() (pfxDER, authSafeContent []byte) {
	safeContents := mustMarshal(b.bags)

	authSafe := []contentInfo{{
		ContentType: oidData,
		Content:     explicitOctet(safeContents),
	}}
	if b.withEncData {
		algo := pbes2Algo(b.iterations, patternBytes(16, 0x91), patternBytes(16, 0x92), false, oidHMACWithSHA384, true)
		body := encryptedDataBody{
			Version: 0,
			EncryptedContentInfo: encryptedContentInfo{
				ContentType:                oidData,
				ContentEncryptionAlgorithm: algo,
				EncryptedContent:           patternBytes(48, 0x93),
			},
		}
		authSafe = append(authSafe, contentInfo{
			ContentType: oidEncryptedData,
			Content:     explicitOctet(mustMarshal(body)),
		})
	}

	authSafeContent = mustMarshal(authSafe)
	originalContent := authSafeContent

	// Tampering simulates an injected CA: the SafeContents inside the octet
	// string is replaced while the MAC computed over the ORIGINAL content is
	// retained, exactly mirroring a real tampering attack.
	if b.tamperAuthSafe {
		injected := append([]safeBag{}, b.bags...)
		injected = append(injected, certBagDER(patternBytes(48, 0xee)))
		tamperedContents := mustMarshal(injected)
		tamperedAuthSafe := []contentInfo{{ContentType: oidData, Content: explicitOctet(tamperedContents)}}
		authSafeContent = mustMarshal(tamperedAuthSafe)
	}

	pfx := pfxPdu{
		Version: 3,
		AuthSafe: contentInfo{
			ContentType: oidData,
			Content:     explicitOctet(authSafeContent),
		},
	}
	if !b.omitMac {
		pfx.MacData = b.buildMacData(originalContent)
	}
	return mustMarshal(pfx), authSafeContent
}

func (b *pfxBuilder) buildMacData(authSafeContent []byte) macData {
	if b.pbmac1 {
		kdfParams := pbkdf2Params{
			Salt:           octetRaw(b.salt),
			IterationCount: b.iterations,
			PRF:            algorithmIdentifier{Algorithm: oidHMACWithSHA256, Parameters: nullRaw()},
		}
		kdf := algorithmIdentifier{Algorithm: oidPBKDF2, Parameters: rawFull(mustMarshal(kdfParams))}
		scheme := algorithmIdentifier{Algorithm: oidHMACWithSHA256, Parameters: nullRaw()}
		params := pbmac1Params{KeyDerivationFunc: kdf, MessageAuthScheme: scheme}
		algoID := algorithmIdentifier{Algorithm: oidPBMAC1, Parameters: rawFull(mustMarshal(params))}

		key := pbkdf2HMAC([]byte(b.password), b.salt, b.iterations, sha256.Size, sha256.New)
		macValue := hmac.New(sha256.New, key)
		macValue.Write(authSafeContent)
		return macData{Mac: digestInfo{Algorithm: algoID, Digest: macValue.Sum(nil)}}
	}

	newHash := allowedLegacyDigest[b.macOID.String()]
	h := newHash()
	key := pkcs12KDF(b.password, b.salt, 3, b.iterations, h.Size(), newHash)
	macValue := hmac.New(newHash, key)
	macValue.Write(authSafeContent)
	return macData{
		Mac: digestInfo{
			Algorithm: algorithmIdentifier{Algorithm: b.macOID, Parameters: nullRaw()},
			Digest:    macValue.Sum(nil),
		},
		MacSalt:    b.salt,
		Iterations: b.iterations,
	}
}
