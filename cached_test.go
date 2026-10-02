package hexgrid

import (
	"sync"
	"testing"
)

func TestDiskCacheMatchesDirect(t *testing.T) {
	dc := NewDiskCache(4, 4)
	defer dc.Close()
	c := faceCenterCell(t, 2, 8)
	for k := 0; k <= 4; k++ {
		want := GridDisk(c, k)
		got := dc.GridDisk(c, k)
		if len(got) != len(want) {
			t.Fatalf("k=%d: cached %d cells, direct %d", k, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("k=%d idx=%d: %s != %s", k, i, got[i], want[i])
			}
		}
		// Second read must be a cache hit with identical results.
		got2 := dc.GridDisk(c, k)
		if len(got2) != len(want) {
			t.Fatalf("k=%d: second read %d cells", k, len(got2))
		}
	}
}

func TestDiskCacheZeroAllocHit(t *testing.T) {
	dc := NewDiskCache(4, 3)
	defer dc.Close()
	c := faceCenterCell(t, 9, 8)
	var buf [64]Cell
	dc.GridDiskInto(c, 3, buf[:]) // populate
	allocs := testing.AllocsPerRun(500, func() {
		if dc.GridDiskInto(c, 3, buf[:]) != 37 {
			t.Fatal("unexpected cell count")
		}
	})
	if allocs != 0 {
		t.Fatalf("cached GridDiskInto allocated %v times per run, want 0", allocs)
	}
}

func TestDiskCacheConcurrentNoLeak(t *testing.T) {
	dc := NewDiskCache(8, 3)
	cells := make([]Cell, 64)
	for i := range cells {
		lat, lng := FaceCenterLatLng(i % 20)
		cells[i] = CellFromLatLng(lat+float64(i%7)*0.01, lng, 8)
	}
	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			var buf [64]Cell
			for i := 0; i < 2000; i++ {
				c := cells[(seed*16+i)%len(cells)]
				n := dc.GridDiskInto(c, 3, buf[:])
				if n != 37 {
					t.Errorf("cell %s: got %d cells, want 37", c, n)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	dc.Close()
	if inUse := dc.PoolExposed().InUse(); inUse != 0 {
		t.Fatalf("pool InUse = %d after Close, want 0 (leak)", inUse)
	}
}

func BenchmarkCellFromLatLng(b *testing.B) {
	for i := 0; i < b.N; i++ {
		CellFromLatLng(39.9042, 116.4074, 8)
	}
}

func BenchmarkGridDiskIntoK3(b *testing.B) {
	lat, lng := FaceCenterLatLng(5)
	c := CellFromLatLng(lat, lng, 8)
	var buf [64]Cell
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		GridDiskInto(c, 3, buf[:])
	}
}

func BenchmarkGridDiskIntoK3Edge(b *testing.B) {
	// A cell adjacent to a face boundary forces the reprojection path.
	lng := 10.0
	prev := CellFromLatLng(-60, lng, 8)
	for lat := -59.9; lat < 80; lat += 0.001 {
		c := CellFromLatLng(lat, lng, 8)
		if c.Face() != prev.Face() {
			break
		}
		prev = c
	}
	var buf [64]Cell
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		GridDiskInto(prev, 3, buf[:])
	}
}

func BenchmarkDiskCacheHit(b *testing.B) {
	dc := NewDiskCache(4, 3)
	defer dc.Close()
	lat, lng := FaceCenterLatLng(5)
	c := CellFromLatLng(lat, lng, 8)
	var buf [64]Cell
	dc.GridDiskInto(c, 3, buf[:])
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dc.GridDiskInto(c, 3, buf[:])
	}
}
