package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// Replay independently re-simulates a plan against the problem definition.
// It verifies every step's preconditions in the current state, applies the
// add/delete effects, and finally checks that the goal holds. It shares no
// state with the solver, so it acts as an external plan validator.
func Replay(p *Problem, plan []string, out io.Writer) error {
	byName := make(map[string]Action, len(p.Actions))
	for _, a := range p.Actions {
		if _, dup := byName[a.Name]; dup {
			return fmt.Errorf("problem defines action %q twice", a.Name)
		}
		byName[a.Name] = a
	}

	state := p.Init
	total := 0
	fmt.Fprintf(out, "start: %s\n", p.StateString(state))
	for i, name := range plan {
		a, ok := byName[name]
		if !ok {
			return fmt.Errorf("step %d: unknown action %q", i+1, name)
		}
		if state&a.Pre != a.Pre {
			return fmt.Errorf("step %d: preconditions of action %q not satisfied in state %s",
				i+1, name, p.StateString(state))
		}
		state = apply(state, a)
		total += a.Cost
		fmt.Fprintf(out, "step %d: %-12s -> %s (cost %d, total %d)\n",
			i+1, name, p.StateString(state), a.Cost, total)
	}
	if !p.GoalSatisfied(state) {
		return fmt.Errorf("plan ends in state %s but the goal is not satisfied", p.StateString(state))
	}
	fmt.Fprintf(out, "goal reached; total cost %d\n", total)
	return nil
}

// ReadPlan loads a plan file: one action name per line, '#' comments and
// blank lines allowed.
func ReadPlan(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var plan []string
	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 1 {
			return nil, fmt.Errorf("line %d: expected a single action name", lineNo)
		}
		plan = append(plan, fields[0])
	}
	return plan, scanner.Err()
}
