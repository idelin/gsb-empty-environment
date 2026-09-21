package main

import (
	"log"
	"os"

	"bridge"
)

func main() {
	if err := bridge.RunReplay(os.Stdout); err != nil {
		log.Fatal(err)
	}
}
