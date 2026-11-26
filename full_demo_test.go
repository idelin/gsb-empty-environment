package stlf

import (
	"testing"
	"time"
)

func TestFullDomainDemo(t *testing.T) {
	sc := ExampleScenario()
	start := time.Now()
	res := FindWitness(sc.Host, sc.Code, SearchOptions{Timeout: 30 * time.Second})
	t.Logf("search: status=%s witness=%+v native=%d elapsed=%s", res.Status, res.Witness != nil, res.NativeCount, time.Since(start))
	if res.Status != StatusWitness || res.Witness == nil {
		t.Fatalf("search failed: %s", res.Message)
	}
	start = time.Now()
	patch, report := FindMinimalBarrierPatch(sc.Host, sc.Code, PatchSearchOptions{
		VerifyOptions: VerifyOptions{Timeout: 60 * time.Second},
	})
	t.Logf("patch: %s valid=%v leakfree=%v semantics=%v spec=%d native=%d elapsed=%s message=%s",
		patch, report.Valid, report.LeakFree, report.SemanticsPreserved,
		report.SpecLeakCount, report.NativeCount, time.Since(start), report.Message)
	if !report.Valid || !report.LeakFree || !report.SemanticsPreserved {
		t.Fatalf("full patch verification failed: %+v", report)
	}
}
