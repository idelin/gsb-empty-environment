package main

import (
	"fmt"
	"os"
)

func main() {
	allOK := true
	for _, sc := range scenarios() {
		if !runScenario(sc) {
			allOK = false
		}
	}
	if !allOK {
		fmt.Println("RESULT: FAIL")
		os.Exit(1)
	}
	fmt.Println("RESULT: all scenarios PASS")
}
