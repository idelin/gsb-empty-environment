package docexec

import (
	"fmt"
	"strings"
)

// Validate checks structural rules before any block runs:
//   - duplicate block ids
//   - duplicate defined names across blocks (重名定义)
//   - needs referencing unknown block ids
//   - dependency cycles
//
// All problems are reported together as one error.
func Validate(doc *Document) error {
	var problems []string

	byID := map[string]*Block{}
	for _, b := range doc.Blocks {
		if prev, dup := byID[b.ID]; dup {
			problems = append(problems, fmt.Sprintf(
				"duplicate block id %q (lines %d and %d)", b.ID, prev.StartLine, b.StartLine))
		} else {
			byID[b.ID] = b
		}
	}

	owner := map[string]*Block{}
	for _, b := range doc.Blocks {
		for _, name := range b.Defines {
			if prev, dup := owner[name]; dup {
				problems = append(problems, fmt.Sprintf(
					"name %q defined by both block %q (line %d) and block %q (line %d)",
					name, prev.ID, prev.StartLine, b.ID, b.StartLine))
			} else {
				owner[name] = b
			}
		}
	}

	for _, b := range doc.Blocks {
		for _, dep := range b.Needs {
			if _, ok := byID[dep]; !ok {
				problems = append(problems, fmt.Sprintf(
					"block %q (line %d) needs unknown block %q", b.ID, b.StartLine, dep))
			}
			if dep == b.ID {
				problems = append(problems, fmt.Sprintf(
					"block %q (line %d) depends on itself", b.ID, b.StartLine))
			}
		}
	}

	if cycle := findCycle(doc.Blocks, byID); cycle != nil {
		problems = append(problems, "dependency cycle: "+strings.Join(cycle, " -> "))
	}

	if len(problems) > 0 {
		return fmt.Errorf("validation failed:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return nil
}

// findCycle returns one cycle as a list of block ids, or nil if acyclic.
func findCycle(blocks []*Block, byID map[string]*Block) []string {
	const (
		white = 0 // unvisited
		gray  = 1 // on stack
		black = 2 // done
	)
	color := map[string]int{}
	var stack []string
	var cycle []string

	var visit func(b *Block) bool
	visit = func(b *Block) bool {
		color[b.ID] = gray
		stack = append(stack, b.ID)
		for _, dep := range b.Needs {
			next, ok := byID[dep]
			if !ok {
				continue
			}
			switch color[next.ID] {
			case gray:
				// found cycle: slice stack from first occurrence of next.ID
				for i, id := range stack {
					if id == next.ID {
						cycle = append([]string{}, stack[i:]...)
						cycle = append(cycle, next.ID)
						return true
					}
				}
			case white:
				if visit(next) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[b.ID] = black
		return false
	}

	for _, b := range blocks {
		if color[b.ID] == white {
			if visit(b) {
				return cycle
			}
		}
	}
	return nil
}
