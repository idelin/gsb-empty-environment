// Command sheet is an interactive front-end for the cellengine package.
// It reads one command per line from stdin. See README.md for the grammar
// of formulas and the list of commands, or type "help".
package main

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"cellengine"
)

func main() {
	eng := cellengine.New()
	var pending []cellengine.Op
	inBatch := false

	commit := func(ops []cellengine.Op) {
		if len(ops) == 0 {
			fmt.Println("empty batch, no new version")
			return
		}
		rec, err := eng.Apply(ops...)
		if err != nil {
			fmt.Println("error:", err)
			return
		}
		if len(rec) == 0 {
			fmt.Printf("version %d recomputed: (none)\n", eng.Version())
		} else {
			fmt.Printf("version %d recomputed: %s\n", eng.Version(), strings.Join(rec, " "))
		}
	}

	printValue := func(id string, v cellengine.Value) {
		fmt.Printf("%s = %s\n", id, v)
	}

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		cmd := strings.ToLower(fields[0])
		args := fields[1:]
		switch cmd {
		case "set":
			if len(args) < 2 {
				fmt.Println("usage: set <CELL> <formula>")
				continue
			}
			f := strings.TrimSpace(line[len(fields[0])+len(args[0])+1:])
			op := cellengine.SetOp(args[0], f)
			if inBatch {
				pending = append(pending, op)
			} else {
				commit([]cellengine.Op{op})
			}
		case "del":
			if len(args) != 1 {
				fmt.Println("usage: del <CELL>")
				continue
			}
			op := cellengine.DelOp(args[0])
			if inBatch {
				pending = append(pending, op)
			} else {
				commit([]cellengine.Op{op})
			}
		case "begin":
			if inBatch {
				fmt.Println("already in a batch")
				continue
			}
			inBatch = true
			pending = nil
		case "commit":
			if !inBatch {
				fmt.Println("not in a batch")
				continue
			}
			inBatch = false
			commit(pending)
			pending = nil
		case "get":
			if len(args) != 1 && len(args) != 2 {
				fmt.Println("usage: get <CELL> [@version]")
				continue
			}
			id := args[0]
			if len(args) == 1 {
				printValue(strings.ToUpper(id), eng.Get(id))
				continue
			}
			vs := strings.TrimPrefix(args[1], "@")
			n, err := strconv.Atoi(vs)
			if err != nil {
				fmt.Println("bad version:", args[1])
				continue
			}
			printValue(strings.ToUpper(id), eng.GetAt(n, id))
		case "cells":
			snap, ok := eng.Snapshot(eng.Version())
			if !ok || len(snap.Cells) == 0 {
				fmt.Println("(empty sheet)")
				continue
			}
			ids := make([]string, 0, len(snap.Cells))
			for id := range snap.Cells {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			for _, id := range ids {
				fmt.Printf("%s = %-24s -> %s\n", id, "="+snap.Cells[id], snap.Values[id])
			}
		case "versions":
			if eng.Version() == 0 {
				fmt.Println("(no versions yet)")
				continue
			}
			for v := 1; v <= eng.Version(); v++ {
				snap, _ := eng.Snapshot(v)
				fmt.Printf("version %d recomputed: %s\n", v, strings.Join(snap.Recomputed, " "))
			}
		case "help":
			fmt.Print(`commands:
  set <CELL> <formula>   assign a formula ('=' optional), one version per set
  del <CELL>             delete a cell
  begin ... commit       group sets/dels into a single version
  get <CELL> [@version]  read a value, current or historical version
  cells                  dump all cells of the current version
  versions               list versions and what each one recomputed
  quit                   exit
formula grammar:
  expr    := term { ('+'|'-') term }
  term    := factor { '*' factor }
  factor  := '-' factor | primary
  primary := INT | REF | #ERR! | IF(expr, expr, expr) | '(' expr ')'
  REF     := letters followed by digits, e.g. A1, BC12
`)
		case "quit", "exit":
			return
		default:
			fmt.Println("unknown command:", fields[0], "(try 'help')")
		}
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "read error:", err)
		os.Exit(1)
	}
}
