package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Input script format (one command per line, '#' starts a comment):
//
//	total 5            total permit capacity (first command)
//	group a 2          declare a group with reserved quota 2
//	req a              enqueue one request for group a (current round)
//	cancel a           release group a's oldest permit (or drop its oldest pending request)
//	alloc              run one allocation round, print the granted permits
//	status             print per-group active/pending state to stderr
//
// If the script ends with events not yet followed by "alloc", one final
// allocation round is run automatically.
func main() {
	if err := run(os.Stdin); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(in *os.File) error {
	var sim *Simulator
	dirty := false // events seen since last allocation round

	scanner := bufio.NewScanner(in)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		switch fields[0] {
		case "total":
			if sim != nil {
				return fmt.Errorf("line %d: total already set", lineNo)
			}
			if len(fields) != 2 {
				return fmt.Errorf("line %d: usage: total N", lineNo)
			}
			total, err := strconv.Atoi(fields[1])
			if err != nil {
				return fmt.Errorf("line %d: bad total: %v", lineNo, err)
			}
			sim = &Simulator{total: total, groups: map[string]*groupState{}}
		case "group":
			if sim == nil {
				return fmt.Errorf("line %d: total must come first", lineNo)
			}
			if len(fields) != 3 {
				return fmt.Errorf("line %d: usage: group NAME RESERVE", lineNo)
			}
			reserve, err := strconv.Atoi(fields[2])
			if err != nil {
				return fmt.Errorf("line %d: bad reserve: %v", lineNo, err)
			}
			if err := addGroup(sim, fields[1], reserve); err != nil {
				return fmt.Errorf("line %d: %v", lineNo, err)
			}
		case "req", "request":
			if err := needSim(sim, lineNo, fields, 2); err != nil {
				return err
			}
			if err := sim.Request(fields[1]); err != nil {
				return fmt.Errorf("line %d: %v", lineNo, err)
			}
			dirty = true
		case "cancel":
			if err := needSim(sim, lineNo, fields, 2); err != nil {
				return err
			}
			if err := sim.Cancel(fields[1]); err != nil {
				return fmt.Errorf("line %d: %v", lineNo, err)
			}
			dirty = true
		case "alloc":
			if sim == nil {
				return fmt.Errorf("line %d: total must come first", lineNo)
			}
			for _, g := range sim.Allocate() {
				fmt.Println(g)
			}
			dirty = false
		case "status":
			if sim == nil {
				return fmt.Errorf("line %d: total must come first", lineNo)
			}
			for _, name := range sim.order {
				fmt.Fprintf(os.Stderr, "status round=%d group=%s active=%d pending=%d\n",
					sim.Round(), name, sim.Active(name), sim.Pending(name))
			}
		default:
			return fmt.Errorf("line %d: unknown command %q", lineNo, fields[0])
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if sim != nil && dirty {
		for _, g := range sim.Allocate() {
			fmt.Println(g)
		}
	}
	return nil
}

// addGroup incrementally builds a Simulator from script commands, keeping the
// sum-of-reserves invariant of NewSimulator.
func addGroup(sim *Simulator, name string, reserve int) error {
	if reserve < 0 {
		return fmt.Errorf("group %q: reserve must be non-negative", name)
	}
	if _, dup := sim.groups[name]; dup {
		return fmt.Errorf("group %q: declared twice", name)
	}
	sum := reserve
	for _, g := range sim.groups {
		sum += g.reserve
	}
	if sum > sim.total {
		return fmt.Errorf("sum of reserves (%d) exceeds total capacity (%d)", sum, sim.total)
	}
	sim.groups[name] = &groupState{name: name, reserve: reserve}
	sim.order = append(sim.order, name)
	for i := len(sim.order) - 1; i > 0 && sim.order[i] < sim.order[i-1]; i-- {
		sim.order[i], sim.order[i-1] = sim.order[i-1], sim.order[i]
	}
	return nil
}

func needSim(sim *Simulator, lineNo int, fields []string, n int) error {
	if sim == nil {
		return fmt.Errorf("line %d: total must come first", lineNo)
	}
	if len(fields) != n {
		return fmt.Errorf("line %d: bad arguments for %q", lineNo, fields[0])
	}
	return nil
}
