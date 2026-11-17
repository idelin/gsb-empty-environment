package netsec

import "fmt"

// Zeroizer scrubs a page before it may be returned to the page pool.
//
// Zero must not retain the supplied slice after it returns. The pool invokes
// Zero without holding the pool lock, so a test zeroizer may deliberately
// pause in the middle of scrubbing a page. Calls are serialized by the pool;
// one paused scrub only delays later scrubs, not allocation or accounting.
type Zeroizer interface {
	Zero(page []byte) error
}

// ZeroizerFunc adapts a function to Zeroizer.
type ZeroizerFunc func(page []byte) error

// Zero calls f(page).
func (f ZeroizerFunc) Zero(page []byte) error {
	return f(page)
}

// ZeroBytes clears page in place. It is the default Zeroizer.
func ZeroBytes(page []byte) error {
	clear(page)
	return nil
}

func scrub(z Zeroizer, page []byte) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%w: %v", ErrZeroizePanic, recovered)
		}
	}()
	return z.Zero(page)
}

func allZero(page []byte) bool {
	for _, b := range page {
		if b != 0 {
			return false
		}
	}
	return true
}
