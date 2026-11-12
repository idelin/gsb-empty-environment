package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/asn1"
	"hash"
	"math/big"
	"sort"
)

func hashForDigest(digestType uint8) (hash.Hash, error) {
	switch digestType {
	case DigestSHA1:
		return sha1.New(), nil
	case DigestSHA256:
		return sha256.New(), nil
	case DigestSHA384:
		return sha512.New384(), nil
	default:
		return nil, ruleErr(RuleMalformed, "unsupported DS digest algorithm", ErrMalformed)
	}
}

func hashForSig(alg uint8) (crypto.Hash, error) {
	switch alg {
	case AlgRSASHA1:
		return crypto.SHA1, nil
	case AlgRSASHA256, AlgECDSAP256, AlgEd25519:
		return crypto.SHA256, nil
	case AlgRSASHA512:
		return crypto.SHA512, nil
	case AlgECDSAP384:
		return crypto.SHA384, nil
	default:
		return 0, ruleErr(RuleRRSIGInvalid, "unsupported DNSKEY algorithm", nil)
	}
}

// ParsePublicKey 把 DNSKEY RDATA 中的公钥解码为 crypto.PublicKey。
// 支持 RSA/SHA 系列、ECDSA P-256/P-384 与 Ed25519。
func ParsePublicKey(k DNSKEYRecord) (crypto.PublicKey, error) {
	switch k.Algorithm {
	case AlgRSASHA1, AlgRSASHA256, AlgRSASHA512:
		return parseRSAPublicKey(k.PublicKey)
	case AlgECDSAP256, AlgECDSAP384:
		return parseECDSAPublicKey(k)
	case AlgEd25519:
		if len(k.PublicKey) != ed25519.PublicKeySize {
			return nil, ruleErr(RuleMalformed, "bad Ed25519 key length", nil)
		}
		pub := make(ed25519.PublicKey, ed25519.PublicKeySize)
		copy(pub, k.PublicKey)
		return pub, nil
	case AlgEd448:
		return nil, ruleErr(RuleRRSIGInvalid, "Ed448 verification is not supported", nil)
	default:
		return nil, ruleErr(RuleRRSIGInvalid, "unsupported DNSKEY algorithm", nil)
	}
}

// RFC 3110 §2: exponent length (1 或 3 字节)、exponent、modulus，均为大端。
func parseRSAPublicKey(raw []byte) (*rsa.PublicKey, error) {
	if len(raw) < 2 {
		return nil, ruleErr(RuleMalformed, "RSA public key too short", nil)
	}
	pos := 0
	expLen := int(raw[0])
	if expLen == 0 {
		if len(raw) < 3 {
			return nil, ruleErr(RuleMalformed, "truncated RSA exponent length", nil)
		}
		expLen = int(raw[1])<<8 | int(raw[2])
		pos = 3
	} else {
		pos = 1
	}
	if pos+expLen >= len(raw) {
		return nil, ruleErr(RuleMalformed, "truncated RSA exponent", nil)
	}
	exponent := new(big.Int)
	exponent.SetBytes(raw[pos : pos+expLen])
	modulus := new(big.Int)
	modulus.SetBytes(raw[pos+expLen:])
	if exponent.Sign() == 0 || modulus.Sign() == 0 {
		return nil, ruleErr(RuleMalformed, "zero RSA exponent or modulus", nil)
	}
	return &rsa.PublicKey{N: modulus, E: int(exponent.Int64())}, nil
}

// RFC 6605 §4: P-256 为两个 32 字节坐标，P-384 为两个 48 字节坐标。
func parseECDSAPublicKey(k DNSKEYRecord) (*ecdsa.PublicKey, error) {
	var curve elliptic.Curve
	var coordLen int
	switch k.Algorithm {
	case AlgECDSAP256:
		curve, coordLen = elliptic.P256(), 32
	case AlgECDSAP384:
		curve, coordLen = elliptic.P384(), 48
	}
	if len(k.PublicKey) != 2*coordLen {
		return nil, ruleErr(RuleMalformed, "bad ECDSA key length", nil)
	}
	x := new(big.Int).SetBytes(k.PublicKey[:coordLen])
	y := new(big.Int).SetBytes(k.PublicKey[coordLen:])
	return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
}

// signedData 按 RFC 4034 §3.1.8.1 构造 RRSIG 签名输入（RRSIG RDATA
// 去掉 Signature 字段，后接规范排序的 RR 线格式）。
func signedData(set RRset, sig RRSIGRecord) []byte {
	var b []byte
	b = append(b, byte(sig.TypeCovered>>8), byte(sig.TypeCovered))
	b = append(b, sig.Algorithm, sig.Labels)
	b = append(b,
		byte(sig.OrigTTL>>24), byte(sig.OrigTTL>>16),
		byte(sig.OrigTTL>>8), byte(sig.OrigTTL))
	b = append(b,
		byte(sig.Expiration>>24), byte(sig.Expiration>>16),
		byte(sig.Expiration>>8), byte(sig.Expiration))
	b = append(b,
		byte(sig.Inception>>24), byte(sig.Inception>>16),
		byte(sig.Inception>>8), byte(sig.Inception))
	b = append(b, byte(sig.KeyTag>>8), byte(sig.KeyTag))
	b = append(b, canonicalName(sig.SignerName)...)

	owner := canonicalName(set.Name)
	rrs := make([]DNSRecord, len(set.Records))
	copy(rrs, set.Records)
	sort.Slice(rrs, func(i, j int) bool {
		return canonicalRRLess(rrs[i].rdata(), rrs[j].rdata())
	})
	for _, rr := range rrs {
		b = append(b, owner...)
		b = append(b, byte(set.Type>>8), byte(set.Type))
		b = append(b, byte(classIN>>8), byte(classIN))
		ttl := sig.OrigTTL
		b = append(b, byte(ttl>>24), byte(ttl>>16), byte(ttl>>8), byte(ttl))
		rdata := rr.rdata()
		b = append(b, byte(len(rdata)>>8), byte(len(rdata)))
		b = append(b, rdata...)
	}
	return b
}

// canonicalRRLess 实现 RFC 4034 §6.3 的 RDATA 规范排序（无名称压缩的字典序）。
func canonicalRRLess(a, b []byte) bool {
	limit := len(a)
	if len(b) < limit {
		limit = len(b)
	}
	for i := 0; i < limit; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

type ecdsaSignature struct {
	R, S *big.Int
}

func verifySignature(alg uint8, pub crypto.PublicKey, digest, signature []byte) error {
	switch alg {
	case AlgRSASHA1, AlgRSASHA256, AlgRSASHA512:
		rsaKey, ok := pub.(*rsa.PublicKey)
		if !ok {
			return ruleErr(RuleRRSIGInvalid, "not an RSA public key", ErrRRSIGInvalid)
		}
		h, err := hashForSig(alg)
		if err != nil {
			return err
		}
		if err := rsa.VerifyPKCS1v15(rsaKey, h, digest, signature); err != nil {
			return ruleErr(RuleRRSIGInvalid, "RSA signature mismatch", ErrRRSIGInvalid)
		}
		return nil
	case AlgECDSAP256, AlgECDSAP384:
		ecKey, ok := pub.(*ecdsa.PublicKey)
		if !ok {
			return ruleErr(RuleRRSIGInvalid, "not an ECDSA public key", ErrRRSIGInvalid)
		}
		var es ecdsaSignature
		if rest, err := asn1.Unmarshal(signature, &es); err != nil || len(rest) != 0 ||
			es.R == nil || es.S == nil {
			return ruleErr(RuleRRSIGInvalid, "malformed ECDSA signature", ErrRRSIGInvalid)
		}
		if !ecdsa.Verify(ecKey, digest, es.R, es.S) {
			return ruleErr(RuleRRSIGInvalid, "ECDSA signature mismatch", ErrRRSIGInvalid)
		}
		return nil
	case AlgEd25519:
		edKey, ok := pub.(ed25519.PublicKey)
		if !ok {
			return ruleErr(RuleRRSIGInvalid, "not an Ed25519 public key", ErrRRSIGInvalid)
		}
		if !ed25519.Verify(edKey, signedDataNoop(digest), signature) {
			return ruleErr(RuleRRSIGInvalid, "Ed25519 signature mismatch", ErrRRSIGInvalid)
		}
		return nil
	default:
		_, err := hashForSig(alg)
		return err
	}
}

// Ed25519 直接对完整签名输入签名（无预哈希），调用方传入的 digest 即签名输入。
func signedDataNoop(b []byte) []byte { return b }

// verifyRRset 对一个已签名 RRset 做完整密码学校验，返回实际签名用的 DNSKEY。
// 仅校验密码学与 RRSIG 结构；是否为当前 DS 所指 KSK 由上层判定。
func verifyRRset(set RRset, now uint32, signers []Signer) (DNSKEYRecord, error) {
	if set.Sig == nil {
		return DNSKEYRecord{}, ruleErr(RuleRRSIGUnsigned, set.Name+" RRset has no RRSIG", ErrRRSIGUnsigned)
	}
	sig := set.Sig
	if sig.TypeCovered != set.Type ||
		!sameCanonicalName(sig.SignerName, set.Name) ||
		int(sig.Labels) != labelCount(set.Name) {
		return DNSKEYRecord{}, ruleErr(RuleRRSIGSignerMismatch,
			"RRSIG signer/type/labels do not cover the RRset", ErrRRSIGSignerMismatch)
	}
	if now < sig.Inception || now > sig.Expiration {
		return DNSKEYRecord{}, ruleErr(RuleRRSIGTime,
			"RRSIG validity window does not contain observation time", ErrRRSIGTime)
	}

	input := signedData(set, *sig)
	var lastErr error
	for _, s := range signers {
		if s.Key.Algorithm != sig.Algorithm || KeyTag(s.Key) != sig.KeyTag {
			continue
		}
		var digest []byte
		if sig.Algorithm != AlgEd25519 {
			h, err := hashForSig(sig.Algorithm)
			if err != nil {
				return DNSKEYRecord{}, err
			}
			hasher := h.New()
			hasher.Write(input)
			digest = hasher.Sum(nil)
		} else {
			digest = input
		}
		if err := verifySignature(sig.Algorithm, s.PublicKey, digest, sig.Signature); err != nil {
			lastErr = err
			continue
		}
		return s.Key, nil
	}
	if lastErr != nil {
		return DNSKEYRecord{}, lastErr
	}
	return DNSKEYRecord{}, ruleErr(RuleRRSIGInvalid,
		"no published DNSKEY matches RRSIG key tag and algorithm", ErrRRSIGInvalid)
}

func sameCanonicalName(a, b string) bool {
	return string(canonicalName(a)) == string(canonicalName(b))
}

// labelCount 返回非根域名的标签数；RRSIG Labels 不含空根标签。
func labelCount(name string) int {
	n := trimSuffixDot(name)
	if n == "" {
		return 0
	}
	count := 1
	for i := 0; i < len(n); i++ {
		if n[i] == '.' {
			count++
		}
	}
	return count
}

func trimSuffixDot(s string) string {
	for len(s) > 0 && s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	return s
}
