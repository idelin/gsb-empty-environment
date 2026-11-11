package knownhosts

import (
	"sync"
	"testing"
)

func TestConcurrentCallbacks(t *testing.T) {
	fx := newFixture(t)
	cb := fx.kh.Callback()

	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				if err := cb("plain.example.com", 22, fx.hostEdPub); err != nil {
					t.Errorf("concurrent callback: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
