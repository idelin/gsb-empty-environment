package netsec

import "fmt"

func errLengthMismatch(got, want int) error {
	return fmt.Errorf("netsec: distribution length mismatch: %d != %d", got, want)
}

func errNilProbability(pos int) error {
	return fmt.Errorf("netsec: missing probability at position %d (zero-sample distribution has no EMD)", pos)
}

func errNonPositive(kind string, total int64) error {
	return fmt.Errorf("netsec: %s total must be positive, got %d", kind, total)
}
