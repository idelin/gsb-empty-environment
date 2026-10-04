// Command strsolver solves a string constraint problem given as JSON, or
// independently verifies an assignment.
//
//	strsolver solve  < problem.json
//	strsolver verify problem.json assignment.json
//
// The problem JSON adds one optional field to strsolver.Problem:
//
//	{"alphabet":"ab","vars":[{"min":0,"max":2}],"forbidden":["aa"],
//	 "concat":[{"left":0,"right":1,"result":2}],"budget":1000000}
//
// solve prints {"status":"sat|unsat|unknown","assignment":[...],"states":N}.
// verify exits 0 and prints "ok" iff the assignment satisfies the problem.
package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"

	"strsolver"
)

type problemFile struct {
	strsolver.Problem
	Budget int64 `json:"budget,omitempty"`
}

type solveOutput struct {
	Status     string   `json:"status"`
	Assignment []string `json:"assignment,omitempty"`
	States     int64    `json:"states"`
}

func die(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "strsolver: "+format+"\n", args...)
	os.Exit(2)
}

func readJSON(path string, v interface{}) {
	var data []byte
	var err error
	if path == "-" {
		data, err = ioutil.ReadAll(os.Stdin)
	} else {
		data, err = ioutil.ReadFile(path)
	}
	if err != nil {
		die("reading %s: %v", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		die("parsing %s: %v", path, err)
	}
}

func main() {
	if len(os.Args) < 2 {
		die("usage: strsolver solve [problem.json] | strsolver verify problem.json assignment.json")
	}
	switch os.Args[1] {
	case "solve":
		path := "-"
		if len(os.Args) > 2 {
			path = os.Args[2]
		}
		var pf problemFile
		readJSON(path, &pf)
		if err := pf.Problem.Validate(); err != nil {
			die("invalid problem: %v", err)
		}
		res := strsolver.Solve(&pf.Problem, pf.Budget)
		out := solveOutput{Status: res.Status.String(), Assignment: res.Assignment, States: res.States}
		enc, err := json.Marshal(out)
		if err != nil {
			die("encoding result: %v", err)
		}
		fmt.Println(string(enc))
		if res.Status == strsolver.Unknown {
			os.Exit(3)
		}
	case "verify":
		if len(os.Args) != 4 {
			die("usage: strsolver verify problem.json assignment.json")
		}
		var pf problemFile
		readJSON(os.Args[2], &pf)
		if err := pf.Problem.Validate(); err != nil {
			die("invalid problem: %v", err)
		}
		var assignment []string
		readJSON(os.Args[3], &assignment)
		if err := strsolver.Verify(&pf.Problem, assignment); err != nil {
			fmt.Printf("invalid: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("ok")
	default:
		die("unknown command %q", os.Args[1])
	}
}
