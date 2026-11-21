package diffdisclose

import "sort"

// CacheSide 标识缓存节点引用的是哪一棵清单的树。
type CacheSide uint8

const (
	// CacheOld 引用旧清单树根。
	CacheOld CacheSide = 1
	// CacheNew 引用新清单树根。
	CacheNew CacheSide = 2
)

// CacheEntry 是客户端持有的未经信任的局部节点缓存。
// 每条记录声称某清单根下、某地址处的子树摘要为 Digest；
// 仅当清单摘要、地址与摘要全部匹配时才可被认证使用。
type CacheEntry struct {
	Manifest Digest
	Side     CacheSide
	Address  addr
	Height   int
	Digest   Digest
}

// cacheKey 唯一标识一条缓存，重复条目按摘要一致性去重。
type cacheKey struct {
	manifest  Digest
	prefixLen int
	prefix    uint64
}

// authenticateCache 对原始缓存做认证与去重：
//   - 仅接受引用旧或新可信清单根的条目；
//   - 地址与高度必须自洽；
//   - 同一 (清单, 地址) 的重复条目摘要必须一致，否则拒绝；
//   - 结果按规范顺序排序，保证证明序列对缓存子集确定且唯一。
func authenticateCache(raw []CacheEntry, oldRoot, newRoot Digest, treeHeight int) ([]authenticatedCache, error) {
	byKey := make(map[cacheKey]authenticatedCache)
	for _, entry := range raw {
		switch entry.Side {
		case CacheOld:
			if entry.Manifest != oldRoot {
				return nil, errBadCacheEntry
			}
		case CacheNew:
			if entry.Manifest != newRoot {
				return nil, errBadCacheEntry
			}
		default:
			return nil, errBadCacheEntry
		}
		if entry.Height < 0 || entry.Height > MaxHeight ||
			entry.Address.len() != treeHeight-entry.Height {
			return nil, errBadCacheEntry
		}
		key := cacheKey{manifest: entry.Manifest, prefixLen: entry.Address.len()}
		for i, bit := range entry.Address.bits {
			if bit == 1 {
				key.prefix |= uint64(1) << uint(i)
			}
		}
		auth := authenticatedCache{entry: entry, key: key}
		if existing, ok := byKey[key]; ok {
			if existing.entry.Digest != entry.Digest {
				return nil, errBadCacheEntry
			}
			continue
		}
		byKey[key] = auth
	}
	out := make([]authenticatedCache, 0, len(byKey))
	for _, auth := range byKey {
		out = append(out, auth)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].less(out[j]) })
	return out, nil
}

// authenticatedCache 是通过结构认证、等待根哈希链确认的缓存条目。
type authenticatedCache struct {
	entry CacheEntry
	key   cacheKey
}

// less 定义缓存条目的规范全序：先清单摘要，再前缀长度，再前缀值，再高度。
func (a authenticatedCache) less(b authenticatedCache) bool {
	if a.key.manifest != b.key.manifest {
		return digestLess(a.key.manifest, b.key.manifest)
	}
	if a.key.prefixLen != b.key.prefixLen {
		return a.key.prefixLen < b.key.prefixLen
	}
	if a.key.prefix != b.key.prefix {
		return a.key.prefix < b.key.prefix
	}
	return a.entry.Height < b.entry.Height
}

// lookup 在已认证缓存中查找指定清单根与地址的条目。
func lookupCache(auth []authenticatedCache, manifestRoot Digest, address addr) (authenticatedCache, bool) {
	for _, item := range auth {
		if item.entry.Manifest == manifestRoot && equalAddr(item.entry.Address, address) {
			return item, true
		}
	}
	return authenticatedCache{}, false
}

// indexOf 返回条目在规范缓存切片中的下标。
func indexOf(auth []authenticatedCache, target authenticatedCache) int {
	for i := range auth {
		if auth[i].key == target.key {
			return i
		}
	}
	return -1
}

func equalAddr(a, b addr) bool {
	if a.len() != b.len() {
		return false
	}
	for i := range a.bits {
		if a.bits[i] != b.bits[i] {
			return false
		}
	}
	return true
}

// digestLess 给出摘要的确定性字典序，用于规范排序。
func digestLess(a, b Digest) bool {
	for i := 0; i < DigestSize; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
