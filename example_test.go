package netsec_test

import (
	"fmt"

	netsec "example.org/netsec/q0072"
)

func ExampleAnalyze() {
	b := netsec.NewBuilder()
	b.FrameTemplate("tenant-A", "tenant-A",
		netsec.FrameSlot{Tenant: "tenant-A", Data: "A-SECRET"})

	b.FrameAlloc("tenant-A", 1, netsec.WithID("alloc"))
	ref := b.CaptureByRefSlot("tenant-A", 0, true, netsec.WithID("capture"))
	closure := b.MakeClosureWith([]netsec.Capture{{
		Kind:   netsec.CaptureByRef,
		Source: ref.Result,
		Frame:  "tenant-A",
		Slot:   0,
	}}, netsec.WithID("closure"))
	b.PassAsync(closure.Result, netsec.WithID("async"))
	b.Return("", "tenant-A", netsec.WithID("return"))

	report, err := netsec.Analyze(b.Program)
	if err != nil || len(report.Findings) == 0 {
		fmt.Println("no finding")
		return
	}
	check, err := netsec.VerifyFinding(b.Program, report.Findings[0],
		netsec.ExecuteOptions{AttackerTenant: "tenant-B", AttackerData: "B-SECRET"})
	if err != nil {
		fmt.Println("verify error")
		return
	}
	fmt.Println(report.Status, check.Leaked, check.ObservedTenant)
	// Output: vulnerable true tenant-B
}
