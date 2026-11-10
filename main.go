package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) == 1 {
		fmt.Print(Search(false))
		return
	}

	switch os.Args[1] {
	case "check":
		fmt.Print(Search(false))

	case "replay":
		if len(os.Args) == 2 {
			usage()
			os.Exit(2)
		}
		events := make([]Event, 0, len(os.Args)-2)
		for _, text := range os.Args[2:] {
			event, err := ParseEvent(text)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(2)
			}
			events = append(events, event)
		}
		if err := Replay(events); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

	case "vars":
		printVariables()

	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: go run . [check|replay <event>...|vars]")
}

func printVariables() {
	fmt.Print(`persistent variables survive power loss:
  image[A], image[B]   empty | v1 | v2
  boot_slot            A | B
  confirmed            v1 | v2
  fuse                 v1 | v2, monotonic and only incremented while running

volatile variables and their reset values after power loss:
  running_slot          A | B while running; reset to none
  running_version       v1 | v2 while running; reset to empty

initial persistent state:
  image[A]=v1, image[B]=empty, boot_slot=A, confirmed=v1, fuse=v1
`)
}
