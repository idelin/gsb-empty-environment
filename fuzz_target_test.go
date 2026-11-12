package netsec

import (
	"math/rand"
	"testing"
)

// FuzzCheck keeps the checked histories small so the exhaustive permutation
// oracle remains a practical independent reference.
func FuzzCheck(f *testing.F) {
	f.Add(int64(1), 5)
	f.Fuzz(func(t *testing.T, seed int64, n int) {
		if n < 1 || n > 7 {
			n = 4
		}
		rng := rand.New(rand.NewSource(seed))
		h := randomHistory(rng, n)
		if err := h.Validate(); err != nil {
			return
		}
		want := bruteForceLinearizable(h)
		report, err := Check(h)
		if err != nil {
			t.Fatal(err)
		}
		if report.Linearizable != want {
			t.Fatalf("checker=%v brute=%v history=%+v", report.Linearizable, want, h.Operations)
		}
		if report.Linearizable && VerifyOrder(h, report.Order) != nil {
			t.Fatal("returned order fails verification")
		}
	})
}
