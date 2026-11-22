package keyusage

// Algorithm 标识密钥支持的密码算法。
type Algorithm string

const (
	// 签名/MAC 算法。
	AlgRSAPSS     Algorithm = "RSA-PSS"
	AlgECDSAP256  Algorithm = "ECDSA-P256"
	AlgEd25519    Algorithm = "Ed25519"
	AlgHMACSHA256 Algorithm = "HMAC-SHA256"

	// 加密/解密算法。
	AlgRSAOAEP256   Algorithm = "RSA-OAEP-256"
	AlgAESGCM256    Algorithm = "AES-GCM-256"
	AlgChaCha20Poly Algorithm = "CHACHA20-POLY1305"

	// 密钥包装算法。
	AlgAESKeyWrap256 Algorithm = "AES-KW-256"

	// 密钥派生算法。
	AlgHKDFSHA256 Algorithm = "HKDF-SHA256"

	// 弱算法：已被淘汰，白名单外，任何调用一律拒绝。
	AlgRSA15     Algorithm = "RSA1_5"
	Alg3DESCBC   Algorithm = "3DES-CBC"
	AlgMD5       Algorithm = "MD5"
	AlgSHA1      Algorithm = "SHA1"
	AlgECDSAP192 Algorithm = "ECDSA-P192"
	AlgDESCBC    Algorithm = "DES-CBC"
	AlgRC4       Algorithm = "RC4"
)

// algorithmSpec 描述白名单算法允许的用途集合。
type algorithmSpec struct {
	allowed map[Usage]bool
}

// whitelist 是算法白名单：只有出现在表中的算法才允许投入使用，
// 且声明/请求的用途必须落在该算法能力集合内。
var whitelist = map[Algorithm]algorithmSpec{
	AlgRSAPSS:     {allowed: usages(UsageSign, UsageVerify)},
	AlgECDSAP256:  {allowed: usages(UsageSign, UsageVerify)},
	AlgEd25519:    {allowed: usages(UsageSign, UsageVerify)},
	AlgHMACSHA256: {allowed: usages(UsageSign, UsageVerify)},

	AlgRSAOAEP256:   {allowed: usages(UsageEncrypt, UsageDecrypt, UsageWrapKey, UsageUnwrapKey)},
	AlgAESGCM256:    {allowed: usages(UsageEncrypt, UsageDecrypt, UsageWrapKey, UsageUnwrapKey)},
	AlgChaCha20Poly: {allowed: usages(UsageEncrypt, UsageDecrypt)},

	AlgAESKeyWrap256: {allowed: usages(UsageWrapKey, UsageUnwrapKey)},

	// HKDF 用于派生下级密钥；派生出的子密钥可携带父级授权的保密用途，
	// 因此派生算法同时允许声明其下游可能继承的用途。
	AlgHKDFSHA256: {allowed: usages(
		UsageDerive,
		UsageEncrypt,
		UsageDecrypt,
		UsageWrapKey,
		UsageUnwrapKey,
	)},
}

// weakAlgorithms 记录明确被禁用的弱算法，便于给出精确拒绝原因与审计。
var weakAlgorithms = map[Algorithm]bool{
	AlgRSA15:     true,
	Alg3DESCBC:   true,
	AlgMD5:       true,
	AlgSHA1:      true,
	AlgECDSAP192: true,
	AlgDESCBC:    true,
	AlgRC4:       true,
}

func usages(us ...Usage) map[Usage]bool {
	m := make(map[Usage]bool, len(us))
	for _, u := range us {
		m[u] = true
	}
	return m
}

// IsWhitelisted 报告算法是否在白名单内。
func IsWhitelisted(a Algorithm) bool {
	_, ok := whitelist[a]
	return ok
}

// IsWeak 报告算法是否属于已知弱算法。
// 未知算法不算弱算法，但同样不在白名单内，会被拒绝。
func IsWeak(a Algorithm) bool { return weakAlgorithms[a] }

// algorithmAllows 报告白名单算法是否支持指定用途。
func algorithmAllows(a Algorithm, u Usage) bool {
	spec, ok := whitelist[a]
	return ok && spec.allowed[u]
}
