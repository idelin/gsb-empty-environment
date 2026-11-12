package audioalign_test

import (
	"fmt"

	"example.org/domainreissue/q057/audioalign"
)

// ExampleAlign shows how an audio analysis host consumes the alignment
// package without having its PCM modified.
func ExampleAlign() {
	const n = 4096
	ref := make([]float64, n)
	delayed := make([]float64, n+64)

	// A simple decaying impulse train standing in for real content.
	for i := 0; i < n; i += 193 {
		ref[i] = 1
	}
	// The second track is a fixed 32-sample delayed copy.
	for i := 32; i < n; i++ {
		delayed[i] = ref[i-32]
	}

	res, err := audioalign.Align(ref, delayed, audioalign.Config{
		WindowSize:   512,
		HopSize:      256,
		MinLag:       0,
		MaxLag:       64,
		SmoothWeight: 0.2,
		MaxDrift:     1,
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	first := res.Windows[0]
	fmt.Printf("window center=%s optimal paths=%d skipped=%d\n",
		first.Center, len(res.Paths), len(res.Skipped))
	fmt.Printf("first offset=%s (%.2f samples)\n",
		res.Paths[0].Lags[0], res.Paths[0].Lags[0].Float())
	// Output:
	// window center=256 optimal paths=1 skipped=0
	// first offset=32 (32.00 samples)
}
