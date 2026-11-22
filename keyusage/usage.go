package keyusage

// Usage 是密钥的声明用途。一个密钥可同时声明多个用途，
// 但签名用途与加解密用途分属互斥的用途族，不允许混用。
type Usage string

const (
	// UsageSign 用于生成签名/MAC。
	UsageSign Usage = "sign"
	// UsageVerify 用于验签/MAC 校验。
	UsageVerify Usage = "verify"
	// UsageEncrypt 用于加密业务数据。
	UsageEncrypt Usage = "encrypt"
	// UsageDecrypt 用于解密业务数据。
	UsageDecrypt Usage = "decrypt"
	// UsageWrapKey 用于包装（加密）其他密钥材料。
	UsageWrapKey Usage = "wrap_key"
	// UsageUnwrapKey 用于解包（解密）其他密钥材料。
	UsageUnwrapKey Usage = "unwrap_key"
	// UsageDerive 用于派生下级密钥。
	UsageDerive Usage = "derive"
)

// 用途族：同一密钥只能归属签名族或保密族，
// 包装用途与业务加解密用途同属保密族但有额外约束。
var signingUsages = map[Usage]bool{
	UsageSign:   true,
	UsageVerify: true,
}

var confidentialityUsages = map[Usage]bool{
	UsageEncrypt:   true,
	UsageDecrypt:   true,
	UsageWrapKey:   true,
	UsageUnwrapKey: true,
}

// IsSigningUsage 报告用途是否属于签名族。
func IsSigningUsage(u Usage) bool { return signingUsages[u] }

// IsConfidentialityUsage 报告用途是否属于保密（加解密/包装）族。
func IsConfidentialityUsage(u Usage) bool { return confidentialityUsages[u] }

// IsKnownUsage 报告用途是否为系统认识的用途。
func IsKnownUsage(u Usage) bool {
	return signingUsages[u] || confidentialityUsages[u] || u == UsageDerive
}
