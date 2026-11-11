package gifcompose

import (
	"math/rand"
	"sync"
	"testing"
)

// TestConcurrentFrameAccess exercises the Compositor mutex by hammering the
// same animation from multiple goroutines; -race turns data races into
// failures.
func TestConcurrentFrameAccess(t *testing.T) {
	g := randomGIF(rand.New(rand.NewSource(seed+2)), 7, 7, 12)
	want := oracleRender(g)
	comp, err := NewCompositor(g)
	if err != nil {
		t.Fatalf("NewCompositor: %v", err)
	}

	const goroutines = 16
	const iterations = 50
	var wg sync.WaitGroup
	for worker := 0; worker < goroutines; worker++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for k := 0; k < iterations; k++ {
				n := rng.Intn(comp.Len())
				frame, err := comp.Frame(n)
				if err != nil {
					t.Errorf("Frame: %v", err)
					return
				}
				assertNRGBAEqual(t, "concurrent "+itoa(n), want[n], frame)
			}
		}(int64(worker) + 100)
	}
	wg.Wait()
}
