package main

import "fmt"

func main() {
	numNodes, suspicionTimeout, injections, cycles := DefaultScenario()
	fmt.Printf("Simulating SWIM cluster: %d nodes, suspicion timeout %d cycles, %d cycles\n\n",
		numNodes, suspicionTimeout, cycles)

	cluster := NewCluster(numNodes, suspicionTimeout)
	observations := cluster.Run(injections, cycles)

	records := Analyze(injections, observations)
	report := BuildReport(records)
	fmt.Print(report.String())
}
