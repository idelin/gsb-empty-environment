package netsec

import (
	"bytes"
	"compress/flate"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"hash"
	"io"
	"strings"
)

const (
	maxJOSESegmentSize     = 4 << 20
	maxJWEPlaintextSize    = 2 << 20
	maxCompressedSize      = 2 << 20
	aesKeyWrapInitialValue = 0xA6
)

type parsedJWE struct {
	protected      map[string]any
	protectedBytes []byte
	encryptedKey   []byte
	iv             []byte
	ciphertext     []byte
	tag            []byte
}

func parseJWE(token string) (*parsedJWE, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 5 {
		return nil, errorf(ErrMalformedJOSE, "a compact JWE must have five segments")
	}
	for _, part := range parts {
		if len(part) > maxJOSESegmentSize {
			return nil, errorf(ErrMalformedJOSE, "JOSE segment is too large")
		}
	}
	headerBytes, err := decodeBase64URL(parts[0])
	if err != nil {
		return nil, errorf(ErrMalformedJOSE, "invalid protected header encoding")
	}
	header, err := decodeJSONObject(headerBytes)
	if err != nil {
		return nil, errorf(ErrMalformedJOSE, err.Error())
	}
	encryptedKey, err := decodeBase64URL(parts[1])
	if err != nil {
		return nil, errorf(ErrMalformedJOSE, "invalid encrypted key encoding")
	}
	iv, err := decodeBase64URL(parts[2])
	if err != nil {
		return nil, errorf(ErrMalformedJOSE, "invalid IV encoding")
	}
	ciphertext, err := decodeBase64URL(parts[3])
	if err != nil {
		return nil, errorf(ErrMalformedJOSE, "invalid ciphertext encoding")
	}
	tag, err := decodeBase64URL(parts[4])
	if err != nil {
		return nil, errorf(ErrMalformedJOSE, "invalid authentication tag encoding")
	}
	return &parsedJWE{
		protected:      header,
		protectedBytes: []byte(parts[0]),
		encryptedKey:   encryptedKey,
		iv:             iv,
		ciphertext:     ciphertext,
		tag:            tag,
	}, nil
}

func (p *Parser) decryptJWE(token string) ([]byte, map[string]any, error) {
	jwe, err := parseJWE(token)
	if err != nil {
		return nil, nil, err
	}
	alg, err := stringHeader(jwe.protected, "alg")
	if err != nil {
		return nil, nil, err
	}
	enc, err := stringHeader(jwe.protected, "enc")
	if err != nil {
		return nil, nil, err
	}
	if !p.jweAlgorithms[alg] {
		return nil, nil, errorf(ErrAlgorithmNotAllowed, "JWE alg "+alg+" is not allowed")
	}
	if !p.contentEncryptionAlgorithms[enc] {
		return nil, nil, errorf(ErrAlgorithmNotAllowed, "JWE enc "+enc+" is not allowed")
	}
	if _, exists := jwe.protected["crit"]; exists {
		return nil, nil, errorf(ErrMalformedJOSE, "critical header extensions are not supported")
	}
	if zipValue, exists := jwe.protected["zip"]; exists {
		if zipValue != "DEF" {
			return nil, nil, errorf(ErrMalformedJOSE, "unsupported JWE zip value")
		}
	}

	var kid string
	if kidValue, exists := jwe.protected["kid"]; exists {
		var ok bool
		kid, ok = kidValue.(string)
		if !ok {
			return nil, nil, errorf(ErrMalformedJOSE, "kid is not a string")
		}
	}
	keys := selectKeys(p.decryptionKeys, kid)
	if len(keys) == 0 {
		return nil, nil, ErrInvalidDecryptionKey
	}

	candidates, err := unwrapContentEncryptionKeys(alg, enc, keys, jwe.encryptedKey)
	if err != nil {
		return nil, nil, err
	}
	if len(candidates) == 0 {
		return nil, nil, ErrDecryptionFailed
	}

	var lastErr error
	for _, cek := range candidates {
		plaintext, err := decryptContent(enc, cek, jwe.iv, jwe.ciphertext, jwe.tag, jwe.protectedBytes)
		if err != nil {
			lastErr = err
			continue
		}
		if zipValue, exists := jwe.protected["zip"]; exists && zipValue == "DEF" {
			plaintext, err = inflateLimited(plaintext, maxCompressedSize)
			if err != nil {
				lastErr = err
				continue
			}
		}
		if len(plaintext) > maxJWEPlaintextSize {
			return nil, nil, errorf(ErrInvalidRequestObject, "decrypted request object is too large")
		}
		return plaintext, jwe.protected, nil
	}
	if lastErr != nil {
		return nil, nil, errorf(ErrDecryptionFailed, lastErr.Error())
	}
	return nil, nil, ErrDecryptionFailed
}

func stringHeader(header map[string]any, name string) (string, error) {
	value, exists := header[name]
	if !exists {
		return "", errorf(ErrMalformedJOSE, "protected header has no "+name)
	}
	text, ok := value.(string)
	if !ok || text == "" {
		return "", errorf(ErrMalformedJOSE, name+" is not a non-empty string")
	}
	return text, nil
}

func unwrapContentEncryptionKeys(alg, enc string, keys []any, encryptedKey []byte) ([][]byte, error) {
	switch alg {
	case "RSA-OAEP", "RSA-OAEP-256":
		var hashFactory func() hash.Hash
		if alg == "RSA-OAEP" {
			hashFactory = sha1.New
		} else {
			hashFactory = sha256.New
		}
		var candidates [][]byte
		for _, key := range keys {
			privateKey, ok := keyValue(key).(*rsa.PrivateKey)
			if !ok || privateKey == nil {
				continue
			}
			cek, err := rsa.DecryptOAEP(hashFactory(), rand.Reader, privateKey, encryptedKey, nil)
			if err == nil {
				candidates = append(candidates, cek)
			}
		}
		if len(candidates) == 0 {
			return nil, errorf(ErrDecryptionFailed, "RSA-OAEP unwrap failed")
		}
		return candidates, nil
	case "dir":
		if len(encryptedKey) != 0 {
			return nil, errorf(ErrMalformedJOSE, "dir requires an empty encrypted key")
		}
		var candidates [][]byte
		for _, key := range keys {
			secret, ok := keyAsBytes(keyValue(key))
			if !ok {
				continue
			}
			candidates = append(candidates, secret)
		}
		if len(candidates) == 0 {
			return nil, ErrInvalidDecryptionKey
		}
		return candidates, nil
	case "A128KW", "A192KW", "A256KW":
		expectedSize := map[string]int{"A128KW": 16, "A192KW": 24, "A256KW": 32}[alg]
		var candidates [][]byte
		for _, key := range keys {
			kek, ok := keyAsBytes(keyValue(key))
			if !ok || len(kek) != expectedSize {
				continue
			}
			cek, err := aesKeyUnwrap(kek, encryptedKey)
			if err == nil {
				candidates = append(candidates, cek)
			}
		}
		if len(candidates) == 0 {
			return nil, errorf(ErrDecryptionFailed, "AES key unwrap failed")
		}
		return candidates, nil
	default:
		return nil, errorf(ErrUnsupportedJWEAlg, alg)
	}
}

func decryptContent(enc string, cek, iv, ciphertext, tag, aad []byte) ([]byte, error) {
	switch enc {
	case "A128GCM", "A192GCM", "A256GCM":
		expectedKeySize := map[string]int{"A128GCM": 16, "A192GCM": 24, "A256GCM": 32}[enc]
		if len(cek) != expectedKeySize {
			return nil, errorf(ErrDecryptionFailed, "wrong content encryption key size")
		}
		if len(iv) != 12 {
			return nil, errorf(ErrMalformedJOSE, "GCM IV must be 12 bytes")
		}
		if len(tag) != 16 {
			return nil, errorf(ErrMalformedJOSE, "GCM tag must be 16 bytes")
		}
		block, err := aes.NewCipher(cek)
		if err != nil {
			return nil, errorf(ErrDecryptionFailed, err.Error())
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, errorf(ErrDecryptionFailed, err.Error())
		}
		combined := append(append([]byte(nil), ciphertext...), tag...)
		plaintext, err := aead.Open(nil, iv, combined, aad)
		if err != nil {
			return nil, errorf(ErrDecryptionFailed, "authentication failed")
		}
		return plaintext, nil
	case "A128CBC-HS256", "A256CBC-HS512":
		var macKey, encKey []byte
		var hashFactory func() hash.Hash
		var expectedTagSize int
		switch enc {
		case "A128CBC-HS256":
			if len(cek) != 32 {
				return nil, errorf(ErrDecryptionFailed, "wrong content encryption key size")
			}
			macKey, encKey = cek[:16], cek[16:]
			hashFactory = sha256.New
			expectedTagSize = 16
		case "A256CBC-HS512":
			if len(cek) != 64 {
				return nil, errorf(ErrDecryptionFailed, "wrong content encryption key size")
			}
			macKey, encKey = cek[:32], cek[32:]
			hashFactory = sha512.New
			expectedTagSize = 32
		}
		if len(iv) != aes.BlockSize {
			return nil, errorf(ErrMalformedJOSE, "CBC IV must be 16 bytes")
		}
		if len(tag) != expectedTagSize {
			return nil, errorf(ErrMalformedJOSE, "wrong CBC authentication tag size")
		}
		if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
			return nil, errorf(ErrMalformedJOSE, "CBC ciphertext must be a non-zero multiple of the block size")
		}
		mac := hmac.New(hashFactory, macKey)
		mac.Write(aad)
		mac.Write(iv)
		mac.Write(ciphertext)
		expected := mac.Sum(nil)[:expectedTagSize]
		if subtle.ConstantTimeCompare(expected, tag) != 1 {
			return nil, errorf(ErrDecryptionFailed, "authentication failed")
		}
		block, err := aes.NewCipher(encKey)
		if err != nil {
			return nil, errorf(ErrDecryptionFailed, err.Error())
		}
		plaintext := make([]byte, len(ciphertext))
		cipher.NewCBCDecrypter(block, iv).CryptBlocks(plaintext, ciphertext)
		unpadded, err := pkcs7Unpad(plaintext, aes.BlockSize)
		if err != nil {
			return nil, errorf(ErrDecryptionFailed, err.Error())
		}
		return unpadded, nil
	default:
		return nil, errorf(ErrUnsupportedJWEEnc, enc)
	}
}

func pkcs7Unpad(value []byte, blockSize int) ([]byte, error) {
	if len(value) == 0 || len(value)%blockSize != 0 {
		return nil, errorf(ErrDecryptionFailed, "invalid PKCS#7 padding")
	}
	padding := int(value[len(value)-1])
	if padding == 0 || padding > blockSize || padding > len(value) {
		return nil, errorf(ErrDecryptionFailed, "invalid PKCS#7 padding")
	}
	for _, b := range value[len(value)-padding:] {
		if int(b) != padding {
			return nil, errorf(ErrDecryptionFailed, "invalid PKCS#7 padding")
		}
	}
	return value[:len(value)-padding], nil
}

func aesKeyUnwrap(kek, wrapped []byte) ([]byte, error) {
	if len(wrapped) < 24 || len(wrapped)%8 != 0 {
		return nil, errorf(ErrDecryptionFailed, "invalid AES wrapped key length")
	}
	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, errorf(ErrDecryptionFailed, err.Error())
	}
	n := len(wrapped)/8 - 1
	a := make([]byte, 8)
	copy(a, wrapped[:8])
	r := make([][]byte, n)
	for i := 0; i < n; i++ {
		r[i] = append([]byte(nil), wrapped[8*(i+1):8*(i+2)]...)
	}
	var blockInput [16]byte
	var blockOutput [16]byte
	for j := 5; j >= 0; j-- {
		for i := n; i >= 1; i-- {
			t := uint64(n*j + i)
			for k := 0; k < 8; k++ {
				blockInput[k] = a[k] ^ byte(t>>(56-8*k))
			}
			copy(blockInput[8:], r[i-1])
			block.Decrypt(blockOutput[:], blockInput[:])
			copy(a, blockOutput[:8])
			copy(r[i-1], blockOutput[8:])
		}
	}
	for _, b := range a {
		if b != aesKeyWrapInitialValue {
			return nil, errorf(ErrDecryptionFailed, "AES key unwrap integrity check failed")
		}
	}
	var out []byte
	for _, part := range r {
		out = append(out, part...)
	}
	return out, nil
}

func inflateLimited(data []byte, limit int) ([]byte, error) {
	reader := flate.NewReader(bytes.NewReader(data))
	defer reader.Close()
	out, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil {
		return nil, errorf(ErrDecryptionFailed, "invalid DEFLATE data")
	}
	if len(out) > limit {
		return nil, errorf(ErrInvalidRequestObject, "decompressed request object is too large")
	}
	return out, nil
}
