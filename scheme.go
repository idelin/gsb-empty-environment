package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/tls"
	"fmt"
	"math/big"
	"reflect"
	"strings"
)

// SignatureScheme is the TLS signature scheme identifier.
type SignatureScheme = tls.SignatureScheme

// Scheme constants use the names from the IANA TLS SignatureScheme registry.
// The Go crypto/tls names are provided as aliases for convenience.
const (
	RSA_PKCS1_SHA1   = tls.PKCS1WithSHA1
	RSA_PKCS1_SHA256 = tls.PKCS1WithSHA256
	RSA_PKCS1_SHA384 = tls.PKCS1WithSHA384
	RSA_PKCS1_SHA512 = tls.PKCS1WithSHA512

	RSA_PSS_RSAE_SHA256 = tls.PSSWithSHA256
	RSA_PSS_RSAE_SHA384 = tls.PSSWithSHA384
	RSA_PSS_RSAE_SHA512 = tls.PSSWithSHA512

	RSA_PSS_PSS_SHA256 SignatureScheme = 0x0809
	RSA_PSS_PSS_SHA384 SignatureScheme = 0x080a
	RSA_PSS_PSS_SHA512 SignatureScheme = 0x080b

	ECDSA_SHA1             = tls.ECDSAWithSHA1
	ECDSA_SECP256R1_SHA256 = tls.ECDSAWithP256AndSHA256
	ECDSA_SECP384R1_SHA384 = tls.ECDSAWithP384AndSHA384
	ECDSA_SECP521R1_SHA512 = tls.ECDSAWithP521AndSHA512

	ED25519 = tls.Ed25519

	PKCS1WithSHA1          = tls.PKCS1WithSHA1
	PKCS1WithSHA256        = tls.PKCS1WithSHA256
	PKCS1WithSHA384        = tls.PKCS1WithSHA384
	PKCS1WithSHA512        = tls.PKCS1WithSHA512
	PSSWithSHA256          = tls.PSSWithSHA256
	PSSWithSHA384          = tls.PSSWithSHA384
	PSSWithSHA512          = tls.PSSWithSHA512
	ECDSAWithSHA1          = tls.ECDSAWithSHA1
	ECDSAWithP256AndSHA256 = tls.ECDSAWithP256AndSHA256
	ECDSAWithP384AndSHA384 = tls.ECDSAWithP384AndSHA384
	ECDSAWithP521AndSHA512 = tls.ECDSAWithP521AndSHA512
	Ed25519                = tls.Ed25519
)

type keyFamily uint8

const (
	familyRSAKey keyFamily = iota
	familyECDSAKey
	familyEd25519Key
	familyUnsupportedKey
)

type schemeKind uint8

const (
	kindRSAPKCS1 schemeKind = iota
	kindRSAPSSRSAE
	kindRSAPSSPSS
	kindECDSA
	kindEd25519
	kindUnsupported
)

type schemeInfo struct {
	id    SignatureScheme
	name  string
	kind  schemeKind
	hash  crypto.Hash
	curve elliptic.Curve
}

var schemeTable = map[SignatureScheme]schemeInfo{
	tls.PKCS1WithSHA1: {
		id: tls.PKCS1WithSHA1, name: "rsa_pkcs1_sha1", kind: kindRSAPKCS1, hash: crypto.SHA1,
	},
	tls.ECDSAWithSHA1: {
		id: tls.ECDSAWithSHA1, name: "ecdsa_sha1", kind: kindECDSA, hash: crypto.SHA1,
	},
	tls.PKCS1WithSHA256: {
		id: tls.PKCS1WithSHA256, name: "rsa_pkcs1_sha256", kind: kindRSAPKCS1, hash: crypto.SHA256,
	},
	tls.PKCS1WithSHA384: {
		id: tls.PKCS1WithSHA384, name: "rsa_pkcs1_sha384", kind: kindRSAPKCS1, hash: crypto.SHA384,
	},
	tls.PKCS1WithSHA512: {
		id: tls.PKCS1WithSHA512, name: "rsa_pkcs1_sha512", kind: kindRSAPKCS1, hash: crypto.SHA512,
	},
	tls.PSSWithSHA256: {
		id: tls.PSSWithSHA256, name: "rsa_pss_rsae_sha256", kind: kindRSAPSSRSAE, hash: crypto.SHA256,
	},
	tls.PSSWithSHA384: {
		id: tls.PSSWithSHA384, name: "rsa_pss_rsae_sha384", kind: kindRSAPSSRSAE, hash: crypto.SHA384,
	},
	tls.PSSWithSHA512: {
		id: tls.PSSWithSHA512, name: "rsa_pss_rsae_sha512", kind: kindRSAPSSRSAE, hash: crypto.SHA512,
	},
	RSA_PSS_PSS_SHA256: {
		id: RSA_PSS_PSS_SHA256, name: "rsa_pss_pss_sha256", kind: kindRSAPSSPSS, hash: crypto.SHA256,
	},
	RSA_PSS_PSS_SHA384: {
		id: RSA_PSS_PSS_SHA384, name: "rsa_pss_pss_sha384", kind: kindRSAPSSPSS, hash: crypto.SHA384,
	},
	RSA_PSS_PSS_SHA512: {
		id: RSA_PSS_PSS_SHA512, name: "rsa_pss_pss_sha512", kind: kindRSAPSSPSS, hash: crypto.SHA512,
	},
	tls.ECDSAWithP256AndSHA256: {
		id: tls.ECDSAWithP256AndSHA256, name: "ecdsa_secp256r1_sha256",
		kind: kindECDSA, hash: crypto.SHA256, curve: elliptic.P256(),
	},
	tls.ECDSAWithP384AndSHA384: {
		id: tls.ECDSAWithP384AndSHA384, name: "ecdsa_secp384r1_sha384",
		kind: kindECDSA, hash: crypto.SHA384, curve: elliptic.P384(),
	},
	tls.ECDSAWithP521AndSHA512: {
		id: tls.ECDSAWithP521AndSHA512, name: "ecdsa_secp521r1_sha512",
		kind: kindECDSA, hash: crypto.SHA512, curve: elliptic.P521(),
	},
	tls.Ed25519: {
		id: tls.Ed25519, name: "ed25519", kind: kindEd25519,
	},
}

var schemeNames = func() map[string]SignatureScheme {
	m := make(map[string]SignatureScheme, len(schemeTable)*4)
	for id, info := range schemeTable {
		m[normalizeSchemeName(info.name)] = id
		m[normalizeSchemeName(info.nameInGo())] = id
	}
	// Common spellings that are not the exact registry name or Go name.
	m[normalizeSchemeName("rsa_pss_rsae_sha256")] = tls.PSSWithSHA256
	m[normalizeSchemeName("rsa_pss_pss_sha256")] = RSA_PSS_PSS_SHA256
	m[normalizeSchemeName("ecdsa_secp256r1_sha256")] = tls.ECDSAWithP256AndSHA256
	m[normalizeSchemeName("ecdsa_secp384r1_sha384")] = tls.ECDSAWithP384AndSHA384
	m[normalizeSchemeName("ecdsa_secp521r1_sha512")] = tls.ECDSAWithP521AndSHA512
	return m
}()

func (info schemeInfo) nameInGo() string {
	switch info.id {
	case tls.PKCS1WithSHA1:
		return "PKCS1WithSHA1"
	case tls.PKCS1WithSHA256:
		return "PKCS1WithSHA256"
	case tls.PKCS1WithSHA384:
		return "PKCS1WithSHA384"
	case tls.PKCS1WithSHA512:
		return "PKCS1WithSHA512"
	case tls.PSSWithSHA256:
		return "PSSWithSHA256"
	case tls.PSSWithSHA384:
		return "PSSWithSHA384"
	case tls.PSSWithSHA512:
		return "PSSWithSHA512"
	case tls.ECDSAWithSHA1:
		return "ECDSAWithSHA1"
	case tls.ECDSAWithP256AndSHA256:
		return "ECDSAWithP256AndSHA256"
	case tls.ECDSAWithP384AndSHA384:
		return "ECDSAWithP384AndSHA384"
	case tls.ECDSAWithP521AndSHA512:
		return "ECDSAWithP521AndSHA512"
	case tls.Ed25519:
		return "Ed25519"
	default:
		return "SignatureScheme(0x" + fmt.Sprintf("%04x", uint16(info.id)) + ")"
	}
}

func normalizeSchemeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case ' ', '\t', '-', '_', '/', '.':
			continue
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func parseSignatureScheme(v any) (SignatureScheme, error) {
	switch s := v.(type) {
	case nil:
		return 0, fmt.Errorf("%w: nil", ErrUnsupportedSignatureScheme)
	case SignatureScheme:
		return s, nil
	case uint16:
		return SignatureScheme(s), nil
	case uint8:
		return SignatureScheme(s), nil
	case uint32:
		return SignatureScheme(s), nil
	case uint64:
		if s > 0xffff {
			return 0, fmt.Errorf("%w: 0x%x", ErrUnsupportedSignatureScheme, s)
		}
		return SignatureScheme(s), nil
	case uint:
		if s > 0xffff {
			return 0, fmt.Errorf("%w: 0x%x", ErrUnsupportedSignatureScheme, s)
		}
		return SignatureScheme(s), nil
	case int:
		return intSignatureScheme(int64(s))
	case int8:
		return intSignatureScheme(int64(s))
	case int16:
		return intSignatureScheme(int64(s))
	case int32:
		return intSignatureScheme(int64(s))
	case int64:
		return intSignatureScheme(s)
	case string:
		if id, ok := schemeNames[normalizeSchemeName(s)]; ok {
			return id, nil
		}
		return 0, fmt.Errorf("%w: %q", ErrUnsupportedSignatureScheme, s)
	case []byte:
		text := strings.TrimSpace(string(s))
		if id, ok := schemeNames[normalizeSchemeName(text)]; ok {
			return id, nil
		}
		return 0, fmt.Errorf("%w: %q", ErrUnsupportedSignatureScheme, text)
	case fmt.Stringer:
		text := s.String()
		if id, ok := schemeNames[normalizeSchemeName(text)]; ok {
			return id, nil
		}
		return 0, fmt.Errorf("%w: %q", ErrUnsupportedSignatureScheme, text)
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		u := rv.Uint()
		if u > 0xffff {
			return 0, fmt.Errorf("%w: 0x%x", ErrUnsupportedSignatureScheme, u)
		}
		return SignatureScheme(u), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return intSignatureScheme(rv.Int())
	default:
		return 0, fmt.Errorf("%w: %T", ErrUnsupportedSignatureScheme, v)
	}
}

func intSignatureScheme(v int64) (SignatureScheme, error) {
	if v < 0 || v > 0xffff {
		return 0, fmt.Errorf("%w: %d", ErrUnsupportedSignatureScheme, v)
	}
	return SignatureScheme(v), nil
}

func schemeInfoFor(s SignatureScheme) (schemeInfo, error) {
	info, ok := schemeTable[s]
	if !ok {
		return schemeInfo{}, fmt.Errorf("%w: 0x%04x", ErrUnsupportedSignatureScheme, uint16(s))
	}
	if info.kind == kindEd25519 {
		return info, nil
	}
	if info.hash == 0 || !info.hash.Available() {
		return schemeInfo{}, fmt.Errorf("%w: hash unavailable for 0x%04x", ErrUnsupportedSignatureScheme, uint16(s))
	}
	return info, nil
}

func keyFamilyOf(pub any) (keyFamily, error) {
	switch k := pub.(type) {
	case *rsa.PublicKey:
		if k == nil || k.N == nil || k.N.Sign() <= 0 || k.E < 2 {
			return familyUnsupportedKey, ErrInvalidPublicKey
		}
		return familyRSAKey, nil
	case rsa.PublicKey:
		return keyFamilyOf(&k)
	case *ecdsa.PublicKey:
		if k == nil || k.Curve == nil || k.X == nil || k.Y == nil {
			return familyUnsupportedKey, ErrInvalidPublicKey
		}
		if !supportedCurve(k.Curve) {
			return familyUnsupportedKey, fmt.Errorf("%w: unsupported ECDSA curve", ErrUnsupportedPublicKey)
		}
		return familyECDSAKey, nil
	case ecdsa.PublicKey:
		return keyFamilyOf(&k)
	case ed25519.PublicKey:
		if len(k) != ed25519.PublicKeySize {
			return familyUnsupportedKey, fmt.Errorf("%w: invalid Ed25519 public key", ErrInvalidPublicKey)
		}
		return familyEd25519Key, nil
	case *ed25519.PublicKey:
		if k == nil {
			return familyUnsupportedKey, ErrInvalidPublicKey
		}
		return keyFamilyOf(*k)
	default:
		return familyUnsupportedKey, fmt.Errorf("%w: %T", ErrUnsupportedPublicKey, pub)
	}
}

func supportedCurve(c elliptic.Curve) bool {
	if c == nil {
		return false
	}
	for _, standard := range []elliptic.Curve{elliptic.P256(), elliptic.P384(), elliptic.P521()} {
		if curvesEqual(c, standard) {
			return true
		}
	}
	return false
}

func curvesEqual(a, b elliptic.Curve) bool {
	if a == nil || b == nil {
		return false
	}
	ap, bp := a.Params(), b.Params()
	if ap == nil || bp == nil {
		return false
	}
	if ap.Name != bp.Name || ap.BitSize != bp.BitSize {
		return false
	}
	return bigIntsEqual(ap.P, bp.P) &&
		bigIntsEqual(ap.N, bp.N) &&
		bigIntsEqual(ap.B, bp.B) &&
		bigIntsEqual(ap.Gx, bp.Gx) &&
		bigIntsEqual(ap.Gy, bp.Gy)
}

func bigIntsEqual(a, b *big.Int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Cmp(b) == 0
}

func checkSignatureScheme(pub any, scheme SignatureScheme) error {
	info, err := schemeInfoFor(scheme)
	if err != nil {
		return err
	}
	family, err := keyFamilyOf(pub)
	if err != nil {
		return err
	}

	switch family {
	case familyRSAKey:
		switch info.kind {
		case kindRSAPKCS1, kindRSAPSSRSAE, kindRSAPSSPSS:
			return nil
		default:
			return mismatchError(pub, info)
		}
	case familyECDSAKey:
		if info.kind != kindECDSA {
			return mismatchError(pub, info)
		}
		if info.curve != nil {
			key, _ := pub.(*ecdsa.PublicKey)
			if key == nil {
				value, ok := pub.(ecdsa.PublicKey)
				if !ok {
					return fmt.Errorf("%w: %T", ErrUnsupportedPublicKey, pub)
				}
				key = &value
			}
			if !curvesEqual(key.Curve, info.curve) {
				return fmt.Errorf("%w: scheme %s requires curve %s, key uses %s",
					ErrSignatureSchemeMismatch, info.name, info.curve.Params().Name, key.Curve.Params().Name)
			}
		}
		return nil
	case familyEd25519Key:
		if info.kind != kindEd25519 {
			return mismatchError(pub, info)
		}
		return nil
	default:
		return fmt.Errorf("%w: %T", ErrUnsupportedPublicKey, pub)
	}
}

func mismatchError(pub any, info schemeInfo) error {
	var keyName string
	switch k := pub.(type) {
	case *rsa.PublicKey, rsa.PublicKey:
		keyName = "RSA"
	case *ecdsa.PublicKey, ecdsa.PublicKey:
		keyName = "ECDSA"
		if key, ok := k.(*ecdsa.PublicKey); ok && key.Curve != nil && key.Curve.Params() != nil {
			keyName += "/" + key.Curve.Params().Name
		}
	case ed25519.PublicKey, *ed25519.PublicKey:
		keyName = "Ed25519"
	default:
		keyName = fmt.Sprintf("%T", pub)
	}
	return fmt.Errorf("%w: scheme %s is incompatible with %s public key",
		ErrSignatureSchemeMismatch, info.name, keyName)
}
