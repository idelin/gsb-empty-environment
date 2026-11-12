package netsec

import (
	"math/rand"
	"testing"
)

// TestSkewVersusEnumerationDifferential runs the interval analysis and the
// exhaustive engine over many random small-domain credentials and asserts
// identical verdicts and identical feasible client-offset sets.
func TestSkewVersusEnumerationDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20261112))

	for iteration := 0; iteration < 2000; iteration++ {
		clientMin := int64(rng.Intn(7) - 3)
		clientMax := clientMin + int64(rng.Intn(5))
		serverMin := int64(rng.Intn(7) - 3)
		serverMax := serverMin + int64(rng.Intn(5))

		now := int64(rng.Intn(41) - 20)
		notBefore := int64(rng.Intn(41) - 20)
		notAfter := notBefore + 1 + int64(rng.Intn(10))

		cred := Credential{
			ClientClock: clockPtr("c", clientMin, clientMax),
			ServerClock: clockPtr("s", serverMin, serverMax),
			NotBefore:   notBefore,
			NotAfter:    notAfter,
			ServerNow:   now,
		}

		enumReport, err := CheckCredential(cred)
		if err != nil {
			t.Fatalf("iteration %d: enumeration error: %v", iteration, err)
		}
		skewReport, err := AnalyzeSkew(cred)
		if err != nil {
			t.Fatalf("iteration %d: skew error: %v", iteration, err)
		}

		if enumReport.Verdict != skewReport.Verdict {
			t.Fatalf("iteration %d: verdict mismatch enum=%s skew=%s cred=%+v",
				iteration, enumReport.Verdict, skewReport.Verdict, cred)
		}

		// For accept the whole declared domain is feasible (and no
		// calibration is attached); for resync the feasible offsets are
		// exactly the calibration intersection; reject carries none.
		var expectedRange IntRange
		switch enumReport.Verdict {
		case VerdictAccept:
			if enumReport.Calibration != nil {
				t.Fatalf("iteration %d: accept unexpectedly carried calibration", iteration)
			}
			expectedRange = skewReport.ClientDomain
		case VerdictResync:
			expectedRange = skewReport.Calibration
		case VerdictReject:
			if enumReport.Calibration != nil {
				t.Fatalf("iteration %d: reject unexpectedly carried calibration", iteration)
			}
			continue
		}

		var skewOffsets []int64
		for offset := expectedRange.Min; offset <= expectedRange.Max; offset++ {
			skewOffsets = append(skewOffsets, offset)
		}

		if enumReport.Verdict == VerdictResync {
			enumOffsets := calibrationFor(t, enumReport, cred.ClientClock).FeasibleOffsets
			if len(enumOffsets) != len(skewOffsets) {
				t.Fatalf("iteration %d: offset sets enum=%v skew=%v",
					iteration, enumOffsets, skewOffsets)
			}
			for i := range enumOffsets {
				if enumOffsets[i] != skewOffsets[i] {
					t.Fatalf("iteration %d: offset sets enum=%v skew=%v",
						iteration, enumOffsets, skewOffsets)
				}
			}
		}
	}
}
