package diffdisclose

// 证明记录类型标签。每条记录都是规范前序序列中的一个节点。
const (
	recSplit          byte = 1  // 内部下降点：随后是左、右子树序列
	recEmpty          byte = 2  // 授权范围内两侧皆空的叶子
	recSharedInternal byte = 3  // 旧新相等且同址的内部子树，单个不可解释摘要
	recLeafShared     byte = 4  // 旧新相等的叶子，只给摘要不重复给值
	recLeafUpdate     byte = 5  // 新增/删除/改变的叶子，携带存在侧载荷
	recCachedInternal byte = 6  // 由已认证缓存省略的内部子树
	recCachedLeaf     byte = 7  // 由已认证缓存省略的叶子
	recBlindRevoked   byte = 8  // 撤权子树：整子树摘要，禁止展开
	recBlindOutside   byte = 9  // 授权区间外：整子树摘要，禁止展开
	recEmptySubtree   byte = 10 // 授权范围内两侧皆空的内部子树
)

// 叶子存在侧标记。
const (
	sideNeither byte = 0
	sideOld     byte = 1
	sideNew     byte = 2
	sideBoth    byte = 3
)

// record 是构造与验证共用的规范证明记录的内存表示。
type record struct {
	kind byte

	// 叶子更新/共用字段。
	key       uint64
	oldDigest Digest
	newDigest Digest
	oldLeaf   *leafPayload
	newLeaf   *leafPayload
	present   byte

	// 缓存字段。
	cacheIndex int

	// 整子树摘要字段（共用内部、撤回、区间外）。
	blindOld Digest
	blindNew Digest
}

// leafPayload 是线上披露的叶子可解释内容。
type leafPayload struct {
	Key   uint64
	Value []byte
	Salt  []byte
}

// Proof 是自包含的差分披露证明信封。
type Proof struct {
	OldManifest Manifest
	NewManifest Manifest
	TreeHeight  int
	Authorized  IntervalSet
	Revoked     IntervalSet
	Cache       []CacheEntry
	Records     []record
}

// ProofStats 汇总证明构造过程中的关键规模指标，供测试验证稀疏性与最简性。
type ProofStats struct {
	Records          int
	Splits           int
	LeafUpdates      int
	LeafShared       int
	SharedInternals  int
	CachedInternals  int
	CachedLeaves     int
	BlindRevoked     int
	BlindOutside     int
	EmptyLeaves      int
	CacheHits        int
	VisitedNodes     int
	RevealedPayloads int
}

// BuildRequest 是构造差分证明的输入。
type BuildRequest struct {
	OldTree    *Tree // 创世版本允许为 nil
	NewTree    *Tree
	Old        Manifest
	New        Manifest
	Authorized IntervalSet
	Revoked    IntervalSet
	Cache      []CacheEntry
}

// builder 持有一次证明构造的共享状态。
type builder struct {
	req     BuildRequest
	cache   []authenticatedCache
	records []record
	stats   ProofStats
	height  int
}

// BuildProof 构造唯一最简的规范差分证明。
func BuildProof(req BuildRequest) (*Proof, ProofStats, error) {
	if req.NewTree == nil {
		return nil, ProofStats{}, errBadHeight
	}
	height := req.NewTree.Height()
	if req.OldTree != nil && req.OldTree.Height() != height {
		return nil, ProofStats{}, errBadHeight
	}
	if req.Old.Root != rootOf(req.OldTree) || req.New.Root != req.NewTree.Root() {
		return nil, ProofStats{}, errRootMismatch
	}
	if err := validateRevocation(req.Authorized, req.Revoked, height); err != nil {
		return nil, ProofStats{}, err
	}
	revSet, err := newRevocationSet(req.Revoked.items)
	if err != nil {
		return nil, ProofStats{}, err
	}
	auth, err := authenticateCache(req.Cache, req.Old.Root, req.New.Root, height)
	if err != nil {
		return nil, ProofStats{}, err
	}
	b := &builder{
		req:     req,
		cache:   auth,
		height:  height,
		records: make([]record, 0),
	}
	b.build(height, rootAddr())
	proof := &Proof{
		OldManifest: req.Old,
		NewManifest: req.New,
		TreeHeight:  height,
		Authorized:  req.Authorized,
		Revoked:     revSet,
		Cache:       canonicalRawCache(auth),
		Records:     b.records,
	}
	return proof, b.stats, nil
}

func rootOf(t *Tree) Digest {
	if t == nil {
		return Digest{}
	}
	return t.Root()
}

func canonicalRawCache(auth []authenticatedCache) []CacheEntry {
	out := make([]CacheEntry, len(auth))
	for i := range auth {
		out[i] = auth[i].entry
	}
	return out
}

// build 是规范前序递归，地址与高度唯一确定每一步的强制选择。
func (b *builder) build(height int, address addr) {
	b.stats.VisitedNodes++
	start, span := address.keyPrefix(height)
	lo, hi := start, start+span

	// 1. 撤权子树：只给整子树摘要，绝不展开，不透露键是否仍存在。
	if b.req.Revoked.cover(lo, hi) == coverFull {
		b.emitBlind(recBlindRevoked, height, address)
		return
	}
	// 2. 授权区间外：同样只给不可解释的整子树摘要。
	if b.req.Authorized.cover(lo, hi) == coverNone {
		b.emitBlind(recBlindOutside, height, address)
		return
	}

	oldDigest := b.digestOf(b.req.OldTree, height, address)
	newDigest := b.digestOf(b.req.NewTree, height, address)

	// 授权范围内两侧皆空的内部子树：单个空终端即最简，禁止继续展开。
	emptyHere := mustEmptyDigest(height)
	if height > 0 && oldDigest == emptyHere && newDigest == emptyHere {
		b.records = append(b.records, record{kind: recEmptySubtree})
		b.stats.EmptyLeaves++
		return
	}

	// 3. 叶子层级：产出新增/删除/改变/共用事件。
	if height == 0 {
		b.buildLeaf(address, oldDigest, newDigest)
		return
	}

	// 与撤权集合或授权边界相交的内部节点必须下降，使撤权部分在其
	// 子树根以盲终端结束，绝不能在更高层被相等短路整体吞掉。
	if b.req.Revoked.cover(lo, hi) == coverPartial ||
		b.req.Authorized.cover(lo, hi) == coverPartial {
		b.records = append(b.records, record{kind: recSplit})
		b.stats.Splits++
		b.build(height-1, address.left())
		b.build(height-1, address.right())
		return
	}

	// 4. 内部节点只有在两侧摘要相等时才允许缓存省略，
	// 单条缓存只认证一侧，无法独立支撑双侧根重算。
	if oldDigest == newDigest {
		if entry, ok := b.cachedDigest(b.req.New.Root, address, newDigest); ok {
			b.emitCached(entry)
			return
		}
		if entry, ok := b.cachedDigest(b.req.Old.Root, address, oldDigest); ok {
			b.emitCached(entry)
			return
		}
		// 5. 旧新相等且同址：共用一个摘要，不展开任何子节点。
		b.records = append(b.records, record{
			kind:     recSharedInternal,
			blindOld: oldDigest,
			blindNew: newDigest,
		})
		b.stats.SharedInternals++
		return
	}

	// 6. 摘要不同且不可省略：强制下降，先左后右的规范顺序。
	b.records = append(b.records, record{kind: recSplit})
	b.stats.Splits++
	b.build(height-1, address.left())
	b.build(height-1, address.right())
}

// buildLeaf 处理授权范围内的叶子，严格区分四种差分事件。
func (b *builder) buildLeaf(address addr, oldDigest, newDigest Digest) {
	key, _ := address.keyPrefix(0)
	oldData, oldOK := b.leafOf(b.req.OldTree, key)
	newData, newOK := b.leafOf(b.req.NewTree, key)

	switch {
	case !oldOK && !newOK:
		b.records = append(b.records, record{kind: recEmpty})
		b.stats.EmptyLeaves++
	case oldOK && newOK && oldDigest == newDigest:
		// 未变化叶子：优先用缓存省略，否则只给摘要，绝不重复给值。
		if entry, ok := b.cachedDigest(b.req.New.Root, address, newDigest); ok {
			b.emitCachedLeaf(entry, key, oldDigest, newDigest, sideBoth)
			return
		}
		if entry, ok := b.cachedDigest(b.req.Old.Root, address, oldDigest); ok {
			b.emitCachedLeaf(entry, key, oldDigest, newDigest, sideBoth)
			return
		}
		b.records = append(b.records, record{
			kind:      recLeafShared,
			key:       key,
			oldDigest: oldDigest,
			newDigest: newDigest,
			present:   sideBoth,
		})
		b.stats.LeafShared++
	default:
		// 新增、删除或改变：只有确实需要解释的一侧才携带叶子载荷。
		rec := record{
			kind:      recLeafUpdate,
			key:       key,
			oldDigest: oldDigest,
			newDigest: newDigest,
		}
		if oldOK && newOK {
			rec.present = sideBoth
		} else if oldOK {
			rec.present = sideOld
		} else {
			rec.present = sideNew
		}
		// 任一侧若已有认证缓存，则用缓存摘要替代载荷披露。
		if entry, ok := b.cachedDigest(b.req.New.Root, address, newDigest); ok && newOK {
			b.emitCachedLeaf(entry, key, oldDigest, newDigest, rec.present)
			return
		}
		if entry, ok := b.cachedDigest(b.req.Old.Root, address, oldDigest); ok && oldOK {
			b.emitCachedLeaf(entry, key, oldDigest, newDigest, rec.present)
			return
		}
		if oldOK {
			rec.oldLeaf = &leafPayload{Key: key, Value: oldData.value, Salt: oldData.salt}
		}
		if newOK {
			rec.newLeaf = &leafPayload{Key: key, Value: newData.value, Salt: newData.salt}
		}
		b.records = append(b.records, rec)
		b.stats.LeafUpdates++
		b.stats.RevealedPayloads += int(rec.present)
	}
}

// emitBlind 产出撤回或区间外的整子树摘要终端记录。
func (b *builder) emitBlind(kind byte, height int, address addr) {
	rec := record{
		kind:     kind,
		blindOld: b.digestOf(b.req.OldTree, height, address),
		blindNew: b.digestOf(b.req.NewTree, height, address),
	}
	b.records = append(b.records, rec)
	if kind == recBlindRevoked {
		b.stats.BlindRevoked++
	} else {
		b.stats.BlindOutside++
	}
}

// emitCached 产出内部缓存终端记录，cacheIndex 指向规范缓存切片。
func (b *builder) emitCached(entry authenticatedCache) {
	idx := indexOf(b.cache, entry)
	b.records = append(b.records, record{kind: recCachedInternal, cacheIndex: idx})
	b.stats.CachedInternals++
	b.stats.CacheHits++
}

// emitCachedLeaf 产出叶子缓存终端：携带双侧摘要与存在侧，验证者据此
// 重算双根并判定新增/删除/改变，但不披露任何值或盐。
func (b *builder) emitCachedLeaf(entry authenticatedCache, key uint64, oldDigest, newDigest Digest, present byte) {
	idx := indexOf(b.cache, entry)
	b.records = append(b.records, record{
		kind:       recCachedLeaf,
		cacheIndex: idx,
		key:        key,
		oldDigest:  oldDigest,
		newDigest:  newDigest,
		present:    present,
	})
	b.stats.CachedLeaves++
	b.stats.CacheHits++
}

// cachedDigest 查找摘要与实际子树一致的认证缓存条目。
func (b *builder) cachedDigest(manifestRoot Digest, address addr, expected Digest) (authenticatedCache, bool) {
	entry, ok := lookupCache(b.cache, manifestRoot, address)
	if !ok || entry.entry.Digest != expected {
		return authenticatedCache{}, false
	}
	return entry, true
}

func (b *builder) digestOf(t *Tree, height int, address addr) Digest {
	if t == nil {
		return mustEmptyDigest(height)
	}
	return t.digest(height, address)
}

func (b *builder) leafOf(t *Tree, key uint64) (leafData, bool) {
	if t == nil {
		return leafData{}, false
	}
	return t.leaf(key)
}
