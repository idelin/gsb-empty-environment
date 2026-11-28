package pathverify

import (
	"bytes"
	"crypto/x509"
)

// defaultMaxPathLen 限制单条链的最大证书数量，用于防御畸形图导致的爆炸搜索。
const defaultMaxPathLen = 16

// issuerIndex 维护 (Subject, SubjectKeyID) -> 可承担签发者的证书集合。
// 同一 Subject 下可能同时存在自签名根、跨签桥接证书与多密钥证书，
// 因此索引同时按 Subject DN 与 SKI 建桶，回溯时逐张尝试。
type issuerIndex struct {
	bySubject map[string][]*x509.Certificate
	bySKI     map[string][]*x509.Certificate
}

func newIssuerIndex() *issuerIndex {
	return &issuerIndex{
		bySubject: make(map[string][]*x509.Certificate),
		bySKI:     make(map[string][]*x509.Certificate),
	}
}

func (idx *issuerIndex) add(c *x509.Certificate) {
	key := c.Subject.String()
	idx.bySubject[key] = append(idx.bySubject[key], c)
	if len(c.SubjectKeyId) > 0 {
		idx.bySKI[string(c.SubjectKeyId)] = append(idx.bySKI[string(c.SubjectKeyId)], c)
	}
}

// candidates 返回 child 的结构候选签发者：
//  1. child 携带 AKI 时，仅考虑 SKI 与 AKI 相等的证书，再以 Subject DN 兜底；
//  2. 无 AKI 时，退化为 Subject DN 匹配。
//
// 返回结果按指纹去重。
func (idx *issuerIndex) candidates(child *x509.Certificate) []*x509.Certificate {
	seen := make(map[string]struct{})
	var out []*x509.Certificate
	consider := func(list []*x509.Certificate, requireSubject bool) {
		for _, c := range list {
			if requireSubject && c.Subject.String() != child.Issuer.String() {
				continue
			}
			fp := fingerprint(c)
			if _, ok := seen[fp]; ok {
				continue
			}
			seen[fp] = struct{}{}
			out = append(out, c)
		}
	}
	if len(child.AuthorityKeyId) > 0 {
		consider(idx.bySKI[string(child.AuthorityKeyId)], true)
		// AKI 无法命中（签发者未携带 SKI 等）时按 Subject 兜底。
		consider(idx.bySubject[child.Issuer.String()], false)
	} else {
		consider(idx.bySubject[child.Issuer.String()], false)
	}
	return out
}

// buildChains 自叶子向上做带环检测的回溯搜索，产出所有根在前、叶子在后、
// 且每条父子边签名可验证的候选链。有效期、吊销、pathLen、NameConstraints
// 等策略性失败由验证阶段处理，从而保证一条链失败后可继续探索其他可达信任锚。
func buildChains(leaf *x509.Certificate, roots, intermediates *CertPool, maxChain int) [][]*x509.Certificate {
	if maxChain <= 0 {
		maxChain = defaultMaxPathLen
	}
	idx := newIssuerIndex()
	for _, c := range intermediates.certs {
		idx.add(c)
	}
	for _, c := range roots.certs {
		idx.add(c)
	}

	var results [][]*x509.Certificate

	// current 为自叶子向上的反向链（current[0] == leaf）。
	// onPath 记录当前 DFS 路径上的证书指纹，实现环检测。
	var dfs func(current []*x509.Certificate, onPath map[string]struct{})
	dfs = func(current []*x509.Certificate, onPath map[string]struct{}) {
		if len(results) >= 64 || len(current) > maxChain {
			return
		}
		node := current[len(current)-1]

		// 终止条件 1：当前证书本身是受信任根，且与节点实体匹配。
		if anchor, ok := matchRoot(node, roots); ok {
			chain := reverseAndPrepend(current, anchor)
			results = append(results, chain)
			// 命中信任锚后不再继续向上（根之上的证书对本链无意义）。
			return
		}

		// 终止条件 2：达到长度上限仍未触达信任锚。
		if len(current) >= maxChain {
			return
		}

		for _, parent := range idx.candidates(node) {
			fp := fingerprint(parent)
			if _, onStack := onPath[fp]; onStack {
				continue // 环检测：跳过已在当前路径上的证书。
			}
			// 仅做密码学验签（不施加 BasicConstraints/KeyUsage 策略）：
			// 图构建关心“能否回溯到锚点”，CA 合规性由验证阶段统一裁决，
			// 从而保证缺 keyCertSign 等问题以本引擎的结构化 Reason 暴露。
			if err := checkSignedBy(node, parent); err != nil {
				continue // 边不成立：签名回溯失败，尝试下一个候选签发者。
			}
			onPath[fp] = struct{}{}
			dfs(append(current, parent), onPath)
			delete(onPath, fp)
		}
	}

	leafFP := fingerprint(leaf)
	dfs([]*x509.Certificate{leaf}, map[string]struct{}{leafFP: {}})
	return results
}

// checkSignedBy 仅验证 child 的签名可由 parent 公钥验过（纯密码学判断），
// 不检查 BasicConstraints、KeyUsage 等路径策略。
func checkSignedBy(child, parent *x509.Certificate) error {
	return parent.CheckSignature(child.SignatureAlgorithm,
		child.RawTBSCertificate, child.Signature)
}

// matchRoot 判断节点是否就是受信任根（DER 相等），或由池中某张根
// 实体签发（用于根本身不出现在中间池、仅以其下属跨签证书入图的场景）。
// 命中后统一以根池中的锚点证书作为链首。
func matchRoot(node *x509.Certificate, roots *CertPool) (*x509.Certificate, bool) {
	if c, ok := roots.get(fingerprint(node)); ok {
		return c, true
	}
	for _, anchor := range roots.certs {
		if node.Subject.String() != anchor.Subject.String() {
			continue
		}
		if len(anchor.SubjectKeyId) > 0 && len(node.AuthorityKeyId) > 0 &&
			!bytes.Equal(anchor.SubjectKeyId, node.AuthorityKeyId) {
			continue
		}
		if err := checkSignedBy(node, anchor); err == nil {
			return anchor, true
		}
	}
	return nil, false
}

// reverseAndPrepend 将自叶子向上的路径翻转为根在前的顺序；
// 当路径末端节点本身即锚点时不重复追加。
func reverseAndPrepend(upward []*x509.Certificate, anchor *x509.Certificate) []*x509.Certificate {
	chain := make([]*x509.Certificate, 0, len(upward)+1)
	top := upward[len(upward)-1]
	if fingerprint(top) != fingerprint(anchor) {
		chain = append(chain, anchor)
	}
	for i := len(upward) - 1; i >= 0; i-- {
		chain = append(chain, upward[i])
	}
	return chain
}
