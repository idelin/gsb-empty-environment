package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	finding, err := Analyze(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "framingcheck:", err)
		os.Exit(2)
	}
	if finding == nil {
		fmt.Println("OK")
		return
	}
	encoded, err := json.Marshal(finding)
	if err != nil {
		fmt.Fprintln(os.Stderr, "framingcheck:", err)
		os.Exit(2)
	}
	fmt.Println(string(encoded))
}
