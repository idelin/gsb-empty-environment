package diffdisclose

import "bytes"

// checkCanonicalEncoding 验证证明是同一输入下唯一的最简序列：
//   - 区间与缓存必须已经归一化，重新编码后字节一致；
//   - 缓存顺序必须等于 authenticateCache 的规范全序；
//   - 整条证明重新编码必须与输入字节逐字节相同，
//     任何冗余记录、错误顺序或额外负载都会在此暴露。
func (p *Proof) checkCanonicalEncoding(auth []authenticatedCache, height int) error {
	if len(p.Cache) != len(auth) {
		return errNonCanonical
	}
	for i := range auth {
		if !cacheEntryEqual(p.Cache[i], auth[i].entry) {
			return errNonCanonical
		}
	}
	// 授权与撤权区间必须是 NewIntervalSet 的归一化结果。
	authSet, err := NewIntervalSet(p.Authorized.items)
	if err != nil || !intervalSetEqual(authSet, p.Authorized) {
		return errNonCanonical
	}
	revSet, err := newRevocationSet(p.Revoked.items)
	if err != nil || !intervalSetEqual(revSet, p.Revoked) {
		return errNonCanonical
	}
	// 自描述重编码：只要构造器对同一组可信输入是确定性的，
	// 字节相等即证明不存在第二种最简序列。
	roundTrip, err := DecodeProof(p.Encode())
	if err != nil {
		return err
	}
	if !bytes.Equal(roundTrip.Encode(), p.Encode()) {
		return errNonCanonical
	}
	if len(roundTrip.Records) != len(p.Records) {
		return errNonCanonical
	}
	for i := range p.Records {
		if !recordEqual(p.Records[i], roundTrip.Records[i]) {
			return errNonCanonical
		}
	}
	// 独立必要性检查：用验证阶段重算出的节点摘要，逐 split 断言
	// 该节点确实无法被任何终端记录替代，保证序列最简、无冗余下降。
	if err := p.checkSplitNecessity(height); err != nil {
		return err
	}
	return nil
}

// checkSplitNecessity 重放前序解析，记录每个内部节点的 (高度, 地址,
// 旧摘要, 新摘要)，并对 split 节点验证其不满足任何终端条件：
//   - 不是整撤权子树；
//   - 不是授权区间外子树；
//   - 两侧不都是当前高度的预定义空摘要；
//   - 旧新摘要不相等（否则应共用或缓存省略）。
func (p *Proof) checkSplitNecessity(height int) error {
	type nodeResult struct {
		height    int
		address   addr
		oldDigest Digest
		newDigest Digest
	}
	pos := 0
	var walk func(h int, a addr, ancestorPartial bool) (nodeResult, error)
	walk = func(h int, a addr, ancestorPartial bool) (nodeResult, error) {
		if pos >= len(p.Records) {
			return nodeResult{}, errUnderreport
		}
		rec := p.Records[pos]
		pos++
		start, span := a.keyPrefix(h)
		switch rec.kind {
		case recSplit:
			herePartial := p.Revoked.cover(start, start+span) == coverPartial ||
				p.Authorized.cover(start, start+span) == coverPartial
			left, err := walk(h-1, a.left(), ancestorPartial || herePartial)
			if err != nil {
				return nodeResult{}, err
			}
			right, err := walk(h-1, a.right(), ancestorPartial || herePartial)
			if err != nil {
				return nodeResult{}, err
			}
			oldDigest := internalDigest(h, a, left.oldDigest, right.oldDigest)
			newDigest := internalDigest(h, a, left.newDigest, right.newDigest)
			if p.Revoked.cover(start, start+span) == coverFull ||
				p.Authorized.cover(start, start+span) == coverNone {
				return nodeResult{}, errRecordUnnecessary
			}
			empty := mustEmptyDigest(h)
			if oldDigest == empty && newDigest == empty {
				return nodeResult{}, errRecordUnnecessary
			}
			// 完整覆盖（无边界跨越）时，相等同址子树必须以共用/缓存终端
			// 结束；部分覆盖节点为抵达授权或撤权边界而下降是必要的。
			if oldDigest == newDigest && !herePartial && !ancestorPartial {
				return nodeResult{}, errNonCanonical
			}
			return nodeResult{h, a, oldDigest, newDigest}, nil
		case recEmptySubtree:
			e := mustEmptyDigest(h)
			return nodeResult{h, a, e, e}, nil
		case recEmpty:
			e := mustEmptyDigest(0)
			return nodeResult{0, a, e, e}, nil
		case recSharedInternal, recBlindRevoked, recBlindOutside:
			return nodeResult{h, a, rec.blindOld, rec.blindNew}, nil
		case recLeafShared:
			return nodeResult{0, a, rec.oldDigest, rec.newDigest}, nil
		case recLeafUpdate:
			return nodeResult{0, a, rec.oldDigest, rec.newDigest}, nil
		case recCachedInternal, recCachedLeaf:
			if rec.cacheIndex < 0 || rec.cacheIndex >= len(p.Cache) {
				return nodeResult{}, errBadCacheEntry
			}
			entry := p.Cache[rec.cacheIndex]
			d := entry.Digest
			return nodeResult{h, a, d, d}, nil
		default:
			return nodeResult{}, errMalformedProof
		}
	}
	if _, err := walk(height, rootAddr(), false); err != nil {
		return err
	}
	if pos != len(p.Records) {
		return errNonCanonical
	}
	return nil
}

func cacheEntryEqual(a, b CacheEntry) bool {
	return a.Manifest == b.Manifest &&
		a.Side == b.Side &&
		a.Height == b.Height &&
		equalAddr(a.Address, b.Address) &&
		a.Digest == b.Digest
}

func intervalSetEqual(a, b IntervalSet) bool {
	if len(a.items) != len(b.items) {
		return false
	}
	for i := range a.items {
		if a.items[i] != b.items[i] {
			return false
		}
	}
	return true
}

func recordEqual(a, b record) bool {
	return a.kind == b.kind &&
		a.cacheIndex == b.cacheIndex &&
		a.key == b.key &&
		a.present == b.present &&
		a.oldDigest == b.oldDigest &&
		a.newDigest == b.newDigest &&
		a.blindOld == b.blindOld &&
		a.blindNew == b.blindNew &&
		leafPayloadEqual(a.oldLeaf, b.oldLeaf) &&
		leafPayloadEqual(a.newLeaf, b.newLeaf)
}

func leafPayloadEqual(a, b *leafPayload) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Key == b.Key && bytes.Equal(a.Value, b.Value) && bytes.Equal(a.Salt, b.Salt)
}
