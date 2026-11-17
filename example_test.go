package netsec_test

import (
	"fmt"

	netsec "example.org/netsec/q0401"
)

func ExampleMonitor_barrier() {
	monitor := netsec.NewMonitor()
	monitor.Start("scan-42", 1)
	monitor.CancelRequest("scan-42", 1)
	monitor.CancelConfirm("scan-42", 1)

	// A late callback may still be generated, but it must not be consumed.
	monitor.ResultGenerated("scan-42", 1)
	outcome := monitor.ResultConsumed("scan-42", 1)

	fmt.Println(outcome.Violation != nil)
	fmt.Println(outcome.Violation.Index)
	fmt.Println(outcome.Consumption.Order)
	// Output:
	// true
	// 4
	// after-cancel-confirmation
}

func ExampleCheck_barrier() {
	events := []netsec.Event{
		netsec.NewStartedEvent("scan-7", 3),
		netsec.NewCancelRequestedEvent("scan-7", 3),
		netsec.NewCancelConfirmedEvent("scan-7", 3),
		netsec.NewResultConsumedEvent("scan-7", 3),
	}
	fmt.Println(len(netsec.Check(events)))
	// Output:
	// 1
}
