package hexgrid

import "testing"

var benchCell = LatLngToCell(39.9042, 116.4074, 8)
var benchSink uint64

func BenchmarkLatLngToCell(b *testing.B) {
	var acc uint64
	for i := 0; i < b.N; i++ {
		acc += uint64(LatLngToCell(39.9042, 116.4074, 8))
	}
	benchSink = acc
}

func BenchmarkGridDiskBuffered(b *testing.B) {
	p := NewBufferPool()
	buf := p.Get()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		disk, ok := GridDiskBuffered(benchCell, 3, buf)
		if !ok {
			b.Fatal("overflow")
		}
		benchSink = uint64(disk[0])
	}
	b.StopTimer()
	p.Put(buf)
}

func BenchmarkGridDiskSlice(b *testing.B) {
	dst := make([]Cell, 0, 64)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dst = GridDisk(benchCell, 3, dst[:0])
	}
	benchSink = uint64(dst[0])
}

func BenchmarkCacheGetHit(b *testing.B) {
	var c Cache
	key := CacheKey(39.9042, 116.4074, 8)
	c.Put(key, uint64(benchCell))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v, hit := c.Get(key)
		if !hit {
			b.Fatal("miss")
		}
		benchSink = v
	}
}

func BenchmarkCachePut(b *testing.B) {
	var c Cache
	key := CacheKey(39.9042, 116.4074, 8)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Put(key, uint64(benchCell))
	}
}

func BenchmarkPoolGetPut(b *testing.B) {
	p := NewBufferPool()
	warm := p.Get()
	p.Put(warm)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf := p.Get()
		p.Put(buf)
	}
}
