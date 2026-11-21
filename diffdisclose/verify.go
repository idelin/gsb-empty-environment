package diffdisclose

// EventKind 描述授权范围内单个键的差分结果。
type EventKind uint8

const (
	EventAdded   EventKind = 1 // 新树新增
	EventRemoved EventKind = 2 // 新树删除（旧有新无）
	EventChanged EventKind = 3 // 同键值或盐改变
)

// Event 是验证者产出的一条可解释差分。
type Event struct {
	Kind EventKind
	Key  uint64
}

// VerifyResult 汇总验证结果与可审计的披露规模。
type VerifyResult struct {
	Events           []Event
	OldRoot          Digest
	NewRoot          Digest
	RevealedPayloads int
	CacheHits        int
	BlindRevoked     int
	BlindOutside     int
	Visited          int
}

// VerifyOptions 注入验签器；其余信任根来自证明内的签名清单。
type VerifyOptions struct {
	Verifier Verifier
}

// verifier 持有一次验证的递归解析状态。
type verifier struct {
	p         *Proof
	cache     []authenticatedCache
	height    int
	pos       int
	res       VerifyResult
	eventSeen map[uint64]bool
}

// Verify 完整验证证明：签名、规范唯一性、必要性、双根重算与差分完整性。
func (p *Proof) Verify(opts VerifyOptions) (VerifyResult, error) {
	if opts.Verifier == nil {
		return VerifyResult{}, errBadSignature
	}
	if err := VerifyManifest(p.OldManifest, opts.Verifier); err != nil {
		return VerifyResult{}, err
	}
	if err := VerifyManifest(p.NewManifest, opts.Verifier); err != nil {
		return VerifyResult{}, err
	}
	if p.NewManifest.Parent != p.OldManifest.digest() {
		return VerifyResult{}, errBadManifest
	}
	height := p.TreeHeight
	if height <= 0 || height > MaxHeight {
		return VerifyResult{}, errBadHeight
	}
	auth, err := authenticateCache(p.Cache, p.OldManifest.Root, p.NewManifest.Root, height)
	if err != nil {
		return VerifyResult{}, err
	}
	if err := validateRevocation(p.Authorized, p.Revoked, height); err != nil {
		return VerifyResult{}, err
	}
	if err := checkRootHeight(p.OldManifest.Root, height); err != nil {
		return VerifyResult{}, err
	}
	if err := checkRootHeight(p.NewManifest.Root, height); err != nil {
		return VerifyResult{}, err
	}

	v := &verifier{
		p:         p,
		cache:     auth,
		height:    height,
		eventSeen: make(map[uint64]bool),
	}
	oldRoot, newRoot, err := v.parse(height, rootAddr())
	if err != nil {
		return VerifyResult{}, err
	}
	if v.pos != len(p.Records) {
		return VerifyResult{}, errNonCanonical
	}
	if oldRoot != p.OldManifest.Root || newRoot != p.NewManifest.Root {
		return VerifyResult{}, errRootMismatch
	}
	// 规范唯一性：重新编码必须与规范构造逐字节一致。
	if err := p.checkCanonicalEncoding(auth, height); err != nil {
		return VerifyResult{}, err
	}
	v.res.OldRoot = oldRoot
	v.res.NewRoot = newRoot
	return v.res, nil
}

// checkRootHeight 拒绝把某高度的空摘要挪用到另一高度的树根。
func checkRootHeight(root Digest, height int) error {
	if _, h, ok := findEmptyDigest(root); ok && h != height {
		return errDigestHeight
	}
	return nil
}

// parse 以前序方式消费记录并重算 (旧摘要, 新摘要)。
func (v *verifier) parse(height int, address addr) (Digest, Digest, error) {
	if v.pos >= len(v.p.Records) {
		return Digest{}, Digest{}, errUnderreport
	}
	rec := v.p.Records[v.pos]
	v.pos++
	v.res.Visited++
	start, span := address.keyPrefix(height)
	lo, hi := start, start+span

	switch rec.kind {
	case recBlindRevoked:
		if v.p.Revoked.cover(lo, hi) != coverFull {
			return Digest{}, Digest{}, errRecordUnnecessary
		}
		return v.blindTerminal(rec, true, height, address)
	case recBlindOutside:
		if v.p.Authorized.cover(lo, hi) != coverNone {
			return Digest{}, Digest{}, errRecordUnnecessary
		}
		return v.blindTerminal(rec, false, height, address)
	case recSplit:
		return v.parseSplit(rec, height, address)
	case recEmpty:
		return v.parseEmpty(rec, height, address)
	case recEmptySubtree:
		return v.parseEmptySubtree(rec, height, address)
	case recSharedInternal:
		return v.parseSharedInternal(rec, height, address)
	case recLeafShared:
		return v.parseLeafShared(rec, height, address)
	case recLeafUpdate:
		return v.parseLeafUpdate(rec, height, address)
	case recCachedInternal:
		return v.parseCached(rec, height, address, false)
	case recCachedLeaf:
		return v.parseCached(rec, height, address, true)
	default:
		return Digest{}, Digest{}, errMalformedProof
	}
}

// parseEmptySubtree 校验授权范围内两侧皆空的内部子树，空摘要必须匹配当前高度。
func (v *verifier) parseEmptySubtree(rec record, height int, address addr) (Digest, Digest, error) {
	if height == 0 {
		return Digest{}, Digest{}, errRecordUnnecessary
	}
	start, span := address.keyPrefix(height)
	if v.p.Revoked.cover(start, start+span) == coverFull ||
		v.p.Authorized.cover(start, start+span) == coverNone {
		return Digest{}, Digest{}, errRecordUnnecessary
	}
	empty := mustEmptyDigest(height)
	return empty, empty, nil
}

// blindTerminal 校验撤回/区间外终端：只接受不可解释摘要，且必须与空摘要高度绑定一致。
func (v *verifier) blindTerminal(rec record, revoked bool, height int, address addr) (Digest, Digest, error) {
	if height == 0 {
		// 撤权以整子树为粒度，叶子级撤回仍合法，但不得携带可解释负载。
	}
	// 空摘要高度挪用攻击：若摘要等于某高度的空摘要，其高度必须等于当前高度。
	if _, h, ok := findEmptyDigest(rec.blindOld); ok && h != height {
		return Digest{}, Digest{}, errDigestHeight
	}
	if _, h, ok := findEmptyDigest(rec.blindNew); ok && h != height {
		return Digest{}, Digest{}, errDigestHeight
	}
	if revoked {
		v.res.BlindRevoked++
	} else {
		v.res.BlindOutside++
	}
	return rec.blindOld, rec.blindNew, nil
}

// findEmptyDigest 在预定义空摘要表中查找摘要所在高度。
func findEmptyDigest(d Digest) (Digest, int, bool) {
	for h := 0; h <= MaxHeight; h++ {
		if emptyDigest[h] == d {
			return d, h, true
		}
	}
	return Digest{}, -1, false
}

// parseSplit 强制内部下降并重算内部摘要，同时检查必要性。
func (v *verifier) parseSplit(rec record, height int, address addr) (Digest, Digest, error) {
	if height == 0 {
		return Digest{}, Digest{}, errRecordUnnecessary
	}
	// 撤权与区间外节点必须以盲终端结束，不得借 split 渗透。
	start, span := address.keyPrefix(height)
	if v.p.Revoked.cover(start, start+span) == coverFull ||
		v.p.Authorized.cover(start, start+span) == coverNone {
		return Digest{}, Digest{}, errRecordUnnecessary
	}
	leftOld, leftNew, err := v.parse(height-1, address.left())
	if err != nil {
		return Digest{}, Digest{}, err
	}
	rightOld, rightNew, err := v.parse(height-1, address.right())
	if err != nil {
		return Digest{}, Digest{}, err
	}
	oldDigest := internalDigest(height, address, leftOld, rightOld)
	newDigest := internalDigest(height, address, leftNew, rightNew)
	// 必要性：两侧皆空的内部节点必须用空终端，借 split 展开即非最简。
	emptyHere := mustEmptyDigest(height)
	if oldDigest == emptyHere && newDigest == emptyHere {
		return Digest{}, Digest{}, errRecordUnnecessary
	}
	return oldDigest, newDigest, nil
}

// parseEmpty 校验两侧皆空叶子，并检查授权范围与空摘要高度。
func (v *verifier) parseEmpty(rec record, height int, address addr) (Digest, Digest, error) {
	if height != 0 {
		return Digest{}, Digest{}, errRecordUnnecessary
	}
	key, _ := address.keyPrefix(0)
	if !v.inAuthorizedScope(key) {
		return Digest{}, Digest{}, errUnauthorizedEvent
	}
	empty := mustEmptyDigest(0)
	return empty, empty, nil
}

// parseSharedInternal 校验旧新相等的内部共用子树，只接受单个摘要。
func (v *verifier) parseSharedInternal(rec record, height int, address addr) (Digest, Digest, error) {
	if height == 0 || rec.blindOld != rec.blindNew {
		return Digest{}, Digest{}, errRecordUnnecessary
	}
	if _, h, ok := findEmptyDigest(rec.blindOld); ok {
		if h != height {
			return Digest{}, Digest{}, errDigestHeight
		}
		// 空内部子树在构造侧已于更高层终止，展开到此处不最简。
		return Digest{}, Digest{}, errNonCanonical
	}
	return rec.blindOld, rec.blindNew, nil
}

// parseLeafShared 校验未变化叶子：只披露摘要，不允许重复携带值。
func (v *verifier) parseLeafShared(rec record, height int, address addr) (Digest, Digest, error) {
	if height != 0 || rec.oldDigest != rec.newDigest || rec.oldLeaf != nil || rec.newLeaf != nil {
		return Digest{}, Digest{}, errRecordUnnecessary
	}
	key, _ := address.keyPrefix(0)
	if rec.key != key || !v.inAuthorizedScope(key) {
		return Digest{}, Digest{}, errUnauthorizedEvent
	}
	if _, _, empty := findEmptyDigest(rec.oldDigest); empty {
		return Digest{}, Digest{}, errNonCanonical
	}
	return rec.oldDigest, rec.newDigest, nil
}

// parseLeafUpdate 校验新增/删除/改变叶子并重算叶摘要，产出差分事件。
func (v *verifier) parseLeafUpdate(rec record, height int, address addr) (Digest, Digest, error) {
	if height != 0 {
		return Digest{}, Digest{}, errRecordUnnecessary
	}
	key, _ := address.keyPrefix(0)
	if rec.key != key || !v.inAuthorizedScope(key) {
		return Digest{}, Digest{}, errUnauthorizedEvent
	}
	if v.eventSeen[key] {
		return Digest{}, Digest{}, errNonCanonical
	}
	v.eventSeen[key] = true

	oldOK := rec.present == sideOld || rec.present == sideBoth
	newOK := rec.present == sideNew || rec.present == sideBoth
	if rec.present == sideNeither || (oldOK && rec.oldLeaf == nil) || (newOK && rec.newLeaf == nil) {
		return Digest{}, Digest{}, errMalformedProof
	}

	oldDigest := mustEmptyDigest(0)
	newDigest := mustEmptyDigest(0)
	if oldOK {
		payload := rec.oldLeaf
		if payload.Key != key || len(payload.Salt) != SaltSize {
			return Digest{}, Digest{}, errMalformedProof
		}
		oldDigest = leafDigest(key, payload.Value, payload.Salt)
		v.res.RevealedPayloads++
	}
	if newOK {
		payload := rec.newLeaf
		if payload.Key != key || len(payload.Salt) != SaltSize {
			return Digest{}, Digest{}, errMalformedProof
		}
		newDigest = leafDigest(key, payload.Value, payload.Salt)
		v.res.RevealedPayloads++
	}
	if oldDigest != rec.oldDigest || newDigest != rec.newDigest {
		return Digest{}, Digest{}, errRootMismatch
	}

	switch {
	case !oldOK && newOK:
		v.res.Events = append(v.res.Events, Event{Kind: EventAdded, Key: key})
	case oldOK && !newOK:
		// 删除事件：新侧为空摘要由重算链固定，少报删除会导致根不匹配。
		v.res.Events = append(v.res.Events, Event{Kind: EventRemoved, Key: key})
	case oldOK && newOK:
		if oldDigest == newDigest {
			return Digest{}, Digest{}, errNonCanonical
		}
		v.res.Events = append(v.res.Events, Event{Kind: EventChanged, Key: key})
	}
	return oldDigest, newDigest, nil
}

// parseCached 校验内部缓存终端：仅当两侧摘要相等且缓存认证通过时才可省略。
func (v *verifier) parseCached(rec record, height int, address addr, leaf bool) (Digest, Digest, error) {
	if rec.cacheIndex < 0 || rec.cacheIndex >= len(v.cache) {
		return Digest{}, Digest{}, errBadCacheEntry
	}
	entry := v.cache[rec.cacheIndex].entry
	if !equalAddr(entry.Address, address) || entry.Height != height ||
		(leaf && rec.kind != recCachedLeaf) || (!leaf && rec.kind != recCachedInternal) {
		return Digest{}, Digest{}, errBadCacheEntry
	}
	if leaf {
		return v.parseCachedLeaf(rec, entry, height, address)
	}
	// 内部缓存只在两侧相等时使用；缓存认证一侧，另一侧取同一摘要。
	switch entry.Side {
	case CacheOld:
		if entry.Manifest != v.p.OldManifest.Root {
			return Digest{}, Digest{}, errBadCacheEntry
		}
	case CacheNew:
		if entry.Manifest != v.p.NewManifest.Root {
			return Digest{}, Digest{}, errBadCacheEntry
		}
	default:
		return Digest{}, Digest{}, errBadCacheEntry
	}
	if _, h, ok := findEmptyDigest(entry.Digest); ok {
		if h != height {
			return Digest{}, Digest{}, errDigestHeight
		}
		return Digest{}, Digest{}, errNonCanonical
	}
	v.res.CacheHits++
	return entry.Digest, entry.Digest, nil
}

// parseCachedLeaf 校验叶子缓存：双侧摘要与存在侧随记录携带，缓存仅用于
// 认证其中一侧，验证者不接触任何值或盐即可产出差分事件。
func (v *verifier) parseCachedLeaf(rec record, entry CacheEntry, height int, address addr) (Digest, Digest, error) {
	if height != 0 {
		return Digest{}, Digest{}, errRecordUnnecessary
	}
	key, _ := address.keyPrefix(0)
	if rec.key != key || !v.inAuthorizedScope(key) {
		return Digest{}, Digest{}, errUnauthorizedEvent
	}
	if v.eventSeen[key] {
		return Digest{}, Digest{}, errNonCanonical
	}
	v.eventSeen[key] = true
	oldOK := rec.present == sideOld || rec.present == sideBoth
	newOK := rec.present == sideNew || rec.present == sideBoth
	if rec.present == sideNeither {
		return Digest{}, Digest{}, errMalformedProof
	}
	cachedIsOld := entry.Side == CacheOld
	want := rec.newDigest
	if cachedIsOld {
		want = rec.oldDigest
	}
	if entry.Digest != want {
		return Digest{}, Digest{}, errBadCacheEntry
	}
	expectedEmpty := mustEmptyDigest(0)
	if !oldOK && rec.oldDigest != expectedEmpty {
		return Digest{}, Digest{}, errMalformedProof
	}
	if !newOK && rec.newDigest != expectedEmpty {
		return Digest{}, Digest{}, errMalformedProof
	}
	switch {
	case !oldOK && newOK:
		v.res.Events = append(v.res.Events, Event{Kind: EventAdded, Key: key})
	case oldOK && !newOK:
		v.res.Events = append(v.res.Events, Event{Kind: EventRemoved, Key: key})
	case oldOK && newOK:
		if rec.oldDigest == rec.newDigest {
			// 未变化叶子共用摘要，不产生变更事件。
			break
		}
		v.res.Events = append(v.res.Events, Event{Kind: EventChanged, Key: key})
	}
	v.res.CacheHits++
	return rec.oldDigest, rec.newDigest, nil
}

// inAuthorizedScope 判断键是否位于授权且未撤回的范围内。
func (v *verifier) inAuthorizedScope(key uint64) bool {
	if !v.p.Authorized.contains(key) {
		return false
	}
	revoked := v.p.Revoked
	for _, iv := range revoked.items {
		if key >= iv.Lo && key < iv.Hi {
			return false
		}
	}
	return true
}
