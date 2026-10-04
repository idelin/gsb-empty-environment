package wsample_test

import (
	"crypto/rand"
	"fmt"
	"math/big"

	"github.com/example/wsample"
)

// Draw 3 distinct items from a weighted set, using crypto/rand as the
// caller-supplied random byte stream.
func Example() {
	s := wsample.New(wsample.ReaderSource{R: rand.Reader})
	for _, w := range []int64{10, 0, 30, 60} { // item 1 has zero weight: never picked
		if _, err := s.Add(big.NewInt(w)); err != nil {
			panic(err)
		}
	}
	picks, err := s.Draw(3)
	if err != nil {
		panic(err)
	}
	fmt.Println(len(picks), "items drawn, total weight", s.Total())
	// Output: 3 items drawn, total weight 100
}
