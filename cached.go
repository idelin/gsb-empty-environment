package hexgrid

import (
	"unsafe"

	"hexgrid/zerocache"
)

// diskCacheShards must match the shard count inside zerocache.Cache.
const diskCacheShards = 64

// DiskCache memoizes GridDisk results in a zero-allocation sharded cache
// backed by an object pool. It is safe for concurrent use.
type DiskCache struct {
	cache *zerocache.Cache
	pool  *zerocache.SlicePool
}

// NewDiskCache creates a cache holding up to 64*perShard disk results, each
// covering radii up to maxK (buffer size 1+3*maxK*(maxK+1)).
func NewDiskCache(perShard, maxK int) *DiskCache {
	bufCap := 1 + 3*maxK*(maxK+1)
	pool := zerocache.NewSlicePool(diskCacheShards*perShard, bufCap)
	return &DiskCache{
		cache: zerocache.NewCache(pool, perShard),
		pool:  pool,
	}
}

func diskKey(c Cell, k int) uint64 {
	return uint64(c) ^ (uint64(k)+1)*0x9E3779B97F4A7C15
}

// cellsAsU64 reinterprets a []Cell as []uint64 without copying. Cell's
// underlying type is uint64, so the two slice headers are layout-identical.
func cellsAsU64(buf []Cell) []uint64 {
	return *(*[]uint64)(unsafe.Pointer(&buf))
}

// GridDiskInto looks up the k-ring disk of center, writing into buf and
// returning the number of cells. On a cache hit no heap allocation and no
// grid math occurs; on a miss the disk is computed and stored.
func (d *DiskCache) GridDiskInto(center Cell, k int, buf []Cell) int {
	key := diskKey(center, k)
	raw := cellsAsU64(buf)
	if n := d.cache.Get(key, raw); n >= 0 {
		return n
	}
	n := GridDiskInto(center, k, buf)
	d.cache.Put(key, raw[:n])
	return n
}

// GridDisk returns the k-ring disk of center, using the cache.
func (d *DiskCache) GridDisk(center Cell, k int) []Cell {
	buf := make([]Cell, 1+3*k*(k+1))
	n := d.GridDiskInto(center, k, buf)
	return buf[:n]
}

// Close releases all cached buffers back to the pool.
func (d *DiskCache) Close() { d.cache.Close() }

// PoolExposed returns the backing pool, for leak assertions in tests.
func (d *DiskCache) PoolExposed() *zerocache.SlicePool { return d.pool }
