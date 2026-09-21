package bitlayout_test

import (
	"reflect"
	"sync"
	"testing"

	"bitlayout"
)

// TestConcurrentSharedPlan hammers one shared plan from many goroutines:
// encoders, one-shot decoders and incremental sessions. Run with -race.
func TestConcurrentSharedPlan(t *testing.T) {
	plan, buf, nbits, rec := encodeSensor(t)
	want, _, err := plan.Decode(buf, nbits, nil)
	if err != nil {
		t.Fatal(err)
	}
	const workers = 8
	const iters = 200
	var wg sync.WaitGroup
	errs := make(chan error, workers*iters)
	for g := 0; g < workers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				switch g % 3 {
				case 0: // encode into a private buffer
					out := make([]byte, 64)
					if _, err := plan.Encode(rec, out); err != nil {
						errs <- err
					}
				case 1: // one-shot decode
					got, _, err := plan.Decode(buf, nbits, nil)
					if err != nil {
						errs <- err
					} else if !reflect.DeepEqual(got, want) {
						errs <- &bitlayout.Error{Kind: bitlayout.ErrValue, Msg: "mismatch"}
					}
				case 2: // incremental session, byte at a time
					s := plan.NewSession(nil)
					for off := int64(0); off < nbits; off += 8 {
						n := int64(8)
						if off+n > nbits {
							n = nbits - off
						}
						chunk, cn := sliceBits(buf, plan.BitOrder, off, off+n)
						s.Feed(chunk, cn)
					}
					if _, done, err := s.Try(); !done || err != nil {
						if err != nil {
							errs <- err
						} else {
							errs <- &bitlayout.Error{Kind: bitlayout.ErrValue, Msg: "session not done"}
						}
					}
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}
