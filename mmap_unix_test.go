// +build darwin linux

package roaring

import (
	"io/ioutil"
	"os"
	"sync"
	"testing"
)

// TestConcurrentMappedReaders shares one mmap'd bitmap across goroutines.
func TestConcurrentMappedReaders(t *testing.T) {
	bm := buildMixed(randSource(31), 40)
	f, err := ioutil.TempFile("", "roaring-test-*.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(bm.Serialize()); err != nil {
		t.Fatal(err)
	}
	f.Close()

	mb, err := OpenMapped(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer mb.Close()
	if !mb.Equals(bm) {
		t.Fatal("mmap'd bitmap differs from original")
	}
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				_ = mb.Contains(uint32(g*100000 + i))
				_ = mb.Cardinality()
			}
		}(g)
	}
	wg.Wait()
}
