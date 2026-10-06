package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
)

type input struct {
	Horizon int    `json:"horizon"`
	Tasks   []Task `json:"tasks"`
}

type output struct {
	Cost        int64          `json:"cost"`
	Schedule    []int          `json:"schedule"`
	Completions map[string]int `json:"completions"`
}

func main() {
	data, err := ioutil.ReadAll(os.Stdin)
	if err != nil {
		fatal("read stdin: %v", err)
	}
	var in input
	if err := json.Unmarshal(data, &in); err != nil {
		fatal("parse input: %v", err)
	}
	if in.Horizon < 0 || in.Horizon > maxHorizon {
		fatal("horizon must be between 0 and %d, got %d", maxHorizon, in.Horizon)
	}
	seen := make(map[int]bool)
	for _, t := range in.Tasks {
		if t.ID < 0 {
			fatal("task id must be >= 0, got %d", t.ID)
		}
		if seen[t.ID] {
			fatal("duplicate task id %d", t.ID)
		}
		seen[t.ID] = true
		if t.Processing < 0 {
			fatal("task %d: negative processing %d", t.ID, t.Processing)
		}
	}

	cost, schedule := Solve(in.Tasks, in.Horizon)

	out := output{Cost: cost, Schedule: schedule, Completions: map[string]int{}}
	for _, t := range in.Tasks {
		c := t.Release // zero-processing tasks complete at release
		if t.Processing > 0 {
			c = -1
			for slot, id := range schedule {
				if id == t.ID {
					c = slot + 1
				}
			}
		}
		out.Completions[fmt.Sprintf("%d", t.ID)] = c
	}
	enc, err := json.Marshal(out)
	if err != nil {
		fatal("encode output: %v", err)
	}
	fmt.Println(string(enc))
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
