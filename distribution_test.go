package netsec

import (
	"encoding/hex"
	"reflect"
	"testing"
)

func TestSingleServerViewIsUniformAndIndependentOfIndex(t *testing.T) {
	t.Parallel()

	for n := 1; n <= 8; n++ {
		var reference *ViewDistribution

		for j := 0; j < n; j++ {
			report, err := ComputeViewDistribution(n, j)
			if err != nil {
				t.Fatalf("N=%d j=%d: %v", n, j, err)
			}
			if report.Total != 1<<uint(n) {
				t.Fatalf("N=%d j=%d total=%d, want %d", n, j, report.Total, 1<<uint(n))
			}
			if len(report.ServerA) != 1<<uint(n) || len(report.ServerB) != 1<<uint(n) {
				t.Fatalf("N=%d j=%d did not observe every vector at each server", n, j)
			}
			for vector, count := range report.ServerA {
				if count != 1 {
					t.Fatalf("N=%d j=%d server A vector %s appeared %d times", n, j, vector, count)
				}
				raw, err := hex.DecodeString(vector)
				if err != nil || !validQuery(raw, n) {
					t.Fatalf("N=%d server A observed an invalid vector %s", n, vector)
				}
			}
			for vector, count := range report.ServerB {
				if count != 1 {
					t.Fatalf("N=%d j=%d server B vector %s appeared %d times", n, j, vector, count)
				}
				raw, err := hex.DecodeString(vector)
				if err != nil || !validQuery(raw, n) {
					t.Fatalf("N=%d server B observed an invalid vector %s", n, vector)
				}
			}

			if reference == nil {
				copied := report
				reference = &copied
			} else if !reflect.DeepEqual(reference.ServerA, report.ServerA) ||
				!reflect.DeepEqual(reference.ServerB, report.ServerB) {
				t.Fatalf("N=%d index %d changed a single-server vector distribution", n, j)
			}

			views, err := EnumerateQueryViews(n, j)
			if err != nil {
				t.Fatal(err)
			}
			for _, view := range views {
				setBits := 0
				for i := 0; i < n; i++ {
					if (view.Q0[i/8]^view.Q1[i/8])&(1<<uint(i%8)) != 0 {
						setBits++
						if i != j {
							t.Fatalf("N=%d j=%d difference set at bit %d", n, j, i)
						}
					}
				}
				if setBits != 1 {
					t.Fatalf("N=%d j=%d q0 XOR q1 has %d set bits", n, j, setBits)
				}
			}

			t.Logf("N=%d j=%d: each server sees all %d vectors exactly once (P=1/%d)",
				n, j, 1<<uint(n), 1<<uint(n))
		}
	}
}
