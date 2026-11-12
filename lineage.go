package netsec

import (
	"crypto/sha256"
	"errors"
	"fmt"
)

// FlagShouldLinkInLineage 是 APK 签名方案 v3 谱系节点 flags 的 0x1 位。
// 置位表示该节点允许更早（旧）证书签署的已安装应用数据沿用到该节点，
// 即旧证书到新证书之间的更新链路可以保留用户数据。
const FlagShouldLinkInLineage uint32 = 0x1

// LineageNode 是 v3 轮换谱系中的一个节点，按时间顺序从旧证书排列到新证书。
// 第 0 个节点是谱系的信任锚（最初的签名者），其自身的 Signature 不参与验证；
// 第 i（i>=1）个节点的 Signature 是第 i-1 个节点对本节点（含证书、flags、
// minSdk、maxSdk）的签名。
type LineageNode struct {
	// Certificate 是该节点签名者的 DER 编码证书。
	Certificate []byte
	// Signature 是上一节点证书对应的私钥对本节点做出的签名。
	Signature []byte
	// Flags 是节点标志位，FlagShouldLinkInLineage（0x1）控制数据沿用。
	Flags uint32
	// MinSDK/MaxSDK 是该签名者承诺生效的 SDK 区间（闭区间）。
	MinSDK uint32
	MaxSDK uint32
}

// SignatureVerifier 由调用方注入：验证 cert 证书的公钥是否认可
// message 上的 signature。例如调用方可以用 x509.ParseCertificate 后
// 按 v3 规范的签名算法完成校验。返回 nil 表示签名通过。
type SignatureVerifier func(cert, message, signature []byte) error

// UpdateParams 是一次保留数据更新的安装前检查输入。
type UpdateParams struct {
	// Lineage 是更新包 v3 签名块携带的完整轮换谱系，旧证书在前。
	Lineage []LineageNode
	// InstalledCertificate 是设备上已安装应用当前签名者的证书。
	InstalledCertificate []byte
	// Verifier 由调用方注入的签名验证函数，不得为 nil。
	Verifier SignatureVerifier
	// DeviceSDK 是设备当前的 SDK 级别。
	DeviceSDK uint32
}

// LineageError 描述谱系结构或内容本身的错误。
type LineageError struct{ reason string }

func (e *LineageError) Error() string { return "lineage: " + e.reason }

// 谱系自身的结构性错误。
var (
	ErrEmptyLineage         = &LineageError{"lineage is empty"}
	ErrNilVerifier          = &LineageError{"nil signature verifier"}
	ErrEmptyCertificate     = &LineageError{"node with empty certificate"}
	ErrDuplicateFingerprint = &LineageError{"duplicate certificate fingerprint in lineage"}
	ErrBrokenChain          = &LineageError{"node signature not verifiable by previous node certificate"}
	ErrSDKRangeReversed     = &LineageError{"node minSdk is greater than maxSdk"}
	ErrSDKRangeOverlap      = &LineageError{"signer sdk ranges overlap"}
	ErrSDKRangeGap          = &LineageError{"signer sdk ranges are not contiguous"}
	ErrDeviceSDKNotCovered  = &LineageError{"device sdk is not covered by signer sdk ranges"}
)

// 更新语义错误。
var (
	ErrInstalledCertNotInLineage = errors.New("installed certificate is not present in the lineage")
	ErrDataRetentionNotAllowed   = errors.New("a node on the rotation path does not carry the data-retention flag (0x1)")
	ErrDowngradedSignatureScheme = errors.New("v3 lineage stripped while v2 signature is retained")
)

// Fingerprint 返回证书的 SHA-256 指纹。v3 谱系以证书摘要判定是否同一签名者。
func Fingerprint(certificate []byte) [sha256.Size]byte {
	return sha256.Sum256(certificate)
}

// VerifyLineage 校验 v3 轮换谱系自身的完整性：
//   - 谱系非空，每个节点证书非空，指纹不得重复；
//   - 第 i 个节点的签名必须能被第 i-1 个节点的证书验证（verifier 注入）；
//   - 各签名者 minSdk/maxSdk 区间不重叠、首尾相接，并覆盖整个谱系跨度。
//
// 设备 SDK 覆盖情况由 VerifyUpdate 结合 DeviceSDK 检查。
func VerifyLineage(lineage []LineageNode, verifier SignatureVerifier) error {
	if len(lineage) == 0 {
		return ErrEmptyLineage
	}
	if verifier == nil {
		return ErrNilVerifier
	}

	seen := make(map[[sha256.Size]byte]struct{}, len(lineage))
	for i := range lineage {
		node := &lineage[i]
		if len(node.Certificate) == 0 {
			return fmt.Errorf("%w: node %d", ErrEmptyCertificate, i)
		}

		fp := Fingerprint(node.Certificate)
		if _, ok := seen[fp]; ok {
			return fmt.Errorf("%w: node %d", ErrDuplicateFingerprint, i)
		}
		seen[fp] = struct{}{}

		if node.MinSDK > node.MaxSDK {
			return fmt.Errorf("%w: node %d", ErrSDKRangeReversed, i)
		}

		if i == 0 {
			// 信任锚没有前驱节点，无需验签。
			continue
		}

		prev := &lineage[i-1]
		message := signedData(node)
		if err := verifier(prev.Certificate, message, node.Signature); err != nil {
			return fmt.Errorf("%w: node %d: %v", ErrBrokenChain, i, err)
		}
	}

	return verifySDKRanges(lineage)
}

// signedData 构造第 i 个节点被第 i-1 个节点签名时覆盖的数据。
// 证书、flags 与 SDK 区间均属于被授权内容，防止攻击者只拼接证书而
// 替换区间或标志位。
func signedData(node *LineageNode) []byte {
	buf := make([]byte, 0, len(node.Certificate)+4+4+4)
	buf = append(buf, node.Certificate...)
	buf = appendUint32(buf, node.Flags)
	buf = appendUint32(buf, node.MinSDK)
	buf = appendUint32(buf, node.MaxSDK)
	return buf
}

func appendUint32(buf []byte, v uint32) []byte {
	return append(buf, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

// verifySDKRanges 保证签名者区间两两不重叠且首尾相接：
// 相邻节点满足 next.MinSDK == prev.MaxSDK+1，从而无重叠、无缺口。
func verifySDKRanges(lineage []LineageNode) error {
	for i := 1; i < len(lineage); i++ {
		prev := &lineage[i-1]
		cur := &lineage[i]

		if cur.MinSDK <= prev.MaxSDK {
			return fmt.Errorf("%w: node %d overlaps node %d", ErrSDKRangeOverlap, i, i-1)
		}
		// uint64 防止 MaxSDK 为 math.MaxUint32 时加一回绕。
		if uint64(cur.MinSDK) != uint64(prev.MaxSDK)+1 {
			return fmt.Errorf("%w: between node %d and node %d", ErrSDKRangeGap, i-1, i)
		}
	}
	return nil
}

// VerifyUpdate 执行保留数据更新的安装前检查：
//  1. 谱系自身完整（链签名、指纹唯一、SDK 区间不重叠且连续）；
//  2. 谱系区间连续覆盖到设备 SDK；
//  3. 已安装应用的证书必须出现在谱系中；
//  4. 从已安装证书到最新证书的轮换路径上，每个后继节点都置 0x1 位，
//     否则旧证书的已安装数据不得沿用。
func VerifyUpdate(params UpdateParams) error {
	lineage := params.Lineage
	if err := VerifyLineage(lineage, params.Verifier); err != nil {
		return err
	}

	first := &lineage[0]
	last := &lineage[len(lineage)-1]
	if params.DeviceSDK < first.MinSDK || params.DeviceSDK > last.MaxSDK {
		return fmt.Errorf("%w: device sdk %d", ErrDeviceSDKNotCovered, params.DeviceSDK)
	}

	installedFP := Fingerprint(params.InstalledCertificate)
	installedIdx := -1
	for i := range lineage {
		if Fingerprint(lineage[i].Certificate) == installedFP {
			installedIdx = i
			break
		}
	}
	if installedIdx < 0 {
		return ErrInstalledCertNotInLineage
	}

	// 已安装证书之后的每个节点都是轮换路径上的“新证书”，必须全部置位，
	// 链条上任何一个节点拒绝沿用都会阻断整条数据保留更新。
	for i := installedIdx + 1; i < len(lineage); i++ {
		if lineage[i].Flags&FlagShouldLinkInLineage == 0 {
			return fmt.Errorf("%w: node %d", ErrDataRetentionNotAllowed, i)
		}
	}
	return nil
}

// SignatureSchemes 汇总更新包实际存在的签名方案。
type SignatureSchemes struct {
	V2Present bool
	V3Present bool
}

// CheckDowngrade 防止剥谱系降级：攻击者移除 v3 签名块（连同轮换谱系），
// 仅保留 v2 签名以绕过谱系检查时必须拒绝。v3 存在即通过；v3 缺失时，
// 只要 v2 仍在就按降级处理。
func CheckDowngrade(schemes SignatureSchemes) error {
	if !schemes.V3Present && schemes.V2Present {
		return ErrDowngradedSignatureScheme
	}
	return nil
}

// VerifyInstall 是安装前检查的组合入口：先排除剥谱系降级，再执行
// 保留数据更新的谱系校验。
func VerifyInstall(params UpdateParams, schemes SignatureSchemes) error {
	if err := CheckDowngrade(schemes); err != nil {
		return err
	}
	return VerifyUpdate(params)
}
