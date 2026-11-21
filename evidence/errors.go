package evidence

import "errors"

var (
	// ErrBadDigest 摘要长度或编码非法。
	ErrBadDigest = errors.New("evidence: bad digest encoding")
	// ErrRootSignature 受签根签名不通过。
	ErrRootSignature = errors.New("evidence: invalid root signature")
	// ErrPatchSignature 补丁信封签名不通过。
	ErrPatchSignature = errors.New("evidence: invalid patch envelope signature")
	// ErrPatchSequence 补丁序号不连续或已应用。
	ErrPatchSequence = errors.New("evidence: patch sequence gap or replay")
	// ErrUnknownAuthority 补丁/根签名者不是登记的根权威。
	ErrUnknownAuthority = errors.New("evidence: unknown authority key")
	// ErrUnknownDomain 治理补丁引用了未登记的信任域。
	ErrUnknownDomain = errors.New("evidence: unknown trust domain")
	// ErrDomainExists 新建信任域的身份已存在。
	ErrDomainExists = errors.New("evidence: domain identity already exists")
	// ErrRegistryHash 治理补丁的前像注册表摘要不匹配。
	ErrRegistryHash = errors.New("evidence: registry predecessor hash mismatch")
	// ErrRootAnchor 旧根与其袋证书/注册表锚点不一致。
	ErrRootAnchor = errors.New("evidence: old root does not anchor supplied certificates")
	// ErrBagMissing 引用的袋证书缺失。
	ErrBagMissing = errors.New("evidence: missing bag certificate")
	// ErrNodeMissing 引用的节点缺失。
	ErrNodeMissing = errors.New("evidence: missing node state")
	// ErrReuseBoundary 复用袋的新边界指纹与旧证书不完全相同。
	ErrReuseBoundary = errors.New("evidence: reuse boundary fingerprint mismatch")
	// ErrRootMismatch 补丁确定性应用的结果与声称的新根不一致。
	ErrRootMismatch = errors.New("evidence: computed new root differs from claimed root")
	// ErrWitnessSignature 见证签名验证失败。
	ErrWitnessSignature = errors.New("evidence: witness signature invalid")
	// ErrNoSource 节点没有任何当前有效的来源见证。
	ErrNoSource = errors.New("evidence: no valid source witness")
	// ErrEnvMismatch 同摘要对象被跨部署环境互换。
	ErrEnvMismatch = errors.New("evidence: same digest object used across environments")
	// ErrDelta 差分独立重验失败。
	ErrDelta = errors.New("evidence: delta verification failed")
	// ErrCostLedger 披露的成本账本与按固定权重重算的结果不符。
	ErrCostLedger = errors.New("evidence: cost ledger not reproducible")
)
