package ringpool_test

import (
	"fmt"

	"ringpool"
)

// Example shows a producer streaming frames through a small ring pool
// while a consumer cuts zero-copy read-only slices.
func Example() {
	pool := ringpool.New(8, 16) // 128-byte resident ring

	go func() {
		pool.Write([]byte("frame:hello,"))
		pool.Write([]byte("frame:world!"))
		pool.Close()
	}()

	var out []byte
	for {
		s, err := pool.Slice(7) // cut at most 7 bytes per slice
		if err == ringpool.ErrClosed {
			break
		}
		if err != nil {
			panic(err)
		}
		out = append(out, s.Bytes()...) // consumer-side copy of the view
		s.Release()                     // refcount drops; block is recycled
	}
	fmt.Printf("received: %s\n", out)
	fmt.Printf("leased blocks after drain: %d\n", pool.Leased())
	// Output:
	// received: frame:hello,frame:world!
	// leased blocks after drain: 0
}
