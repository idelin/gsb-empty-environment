package main

import (
	"flag"
	"fmt"
)

func main() {
	scenario := flag.String("scenario", "all", "partition | recovery | duplicate | all")
	flag.Parse()
	switch *scenario {
	case "partition":
		scenarioPartition()
	case "recovery":
		scenarioRecovery()
	case "duplicate":
		scenarioDuplicate()
	case "all":
		scenarioPartition()
		scenarioRecovery()
		scenarioDuplicate()
	default:
		fmt.Println("unknown scenario:", *scenario)
	}
}
