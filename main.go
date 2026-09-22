package main

import (
	"fmt"

	"atleastonce/sim"
)

func main() {
	scenarios := []sim.ScenarioConfig{
		{Name: "no-fault"},
		{Name: "crash-before-order-commit", Faults: []sim.Fault{{Point: sim.FaultBeforeOrderCommit, MessageID: "cmd-1"}}},
		{Name: "commit-then-response-lost", Faults: []sim.Fault{{Point: sim.FaultAfterOrderAck, MessageID: "cmd-1"}}},
		{Name: "outbox-send-before-mark", Faults: []sim.Fault{{Point: sim.FaultAfterOutboxSend, MessageID: "evt-cmd-1"}}},
		{Name: "downstream-crash-before-commit", Faults: []sim.Fault{{Point: sim.FaultBeforeEventCommit, MessageID: "evt-cmd-1"}}},
	}

	for _, config := range scenarios {
		report, err := sim.RunScenario(config)
		if err != nil {
			panic(err)
		}
		fmt.Print(report.String())
		fmt.Println()
	}
}
