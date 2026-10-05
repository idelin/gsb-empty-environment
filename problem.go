package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// MaxProps is the hard limit on the number of boolean propositions,
// so a state always fits in a uint32 bitmask.
const MaxProps = 20

// Action is a STRIPS-style operator with precondition, add/delete
// effects and a non-negative integer cost, all encoded as bitmasks.
type Action struct {
	Name string
	Pre  uint32
	Add  uint32
	Del  uint32
	Cost int
}

// Problem is a fully parsed planning task.
type Problem struct {
	PropNames []string
	propIndex map[string]uint32
	Init      uint32
	Goal      uint32
	Actions   []Action
}

// GoalSatisfied reports whether every goal proposition holds in s.
func (p *Problem) GoalSatisfied(s uint32) bool {
	return s&p.Goal == p.Goal
}

// StateString renders a state bitmask as a readable set of propositions.
func (p *Problem) StateString(s uint32) string {
	var names []string
	for i, n := range p.PropNames {
		if s&(1<<uint(i)) != 0 {
			names = append(names, n)
		}
	}
	return "{" + strings.Join(names, " ") + "}"
}

// apply deletes then adds effects; delete-first keeps add/del overlaps sane.
func apply(s uint32, a Action) uint32 {
	s &^= a.Del
	s |= a.Add
	return s
}

// ParseFile loads a problem definition from disk.
func ParseFile(path string) (*Problem, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f)
}

// Parse reads the line-based problem format:
//
//	props p1 p2 ...        (at most 20, declared once or in several lines)
//	init  p1 ...           (propositions true in the initial state)
//	goal  p3 ...           (propositions that must hold at the end)
//	action <name>          (starts an action block)
//	  pre  ...             (preconditions)
//	  add  ...             (add effects)
//	  del  ...             (delete effects)
//	  cost <n>             (non-negative integer, default 0)
//
// Blank lines and text after '#' are ignored.
func Parse(r io.Reader) (*Problem, error) {
	p := &Problem{propIndex: make(map[string]uint32)}
	var cur *Action
	flush := func() {
		if cur != nil {
			p.Actions = append(p.Actions, *cur)
			cur = nil
		}
	}
	bits := func(names []string, lineNo int) (uint32, error) {
		var m uint32
		for _, n := range names {
			idx, ok := p.propIndex[n]
			if !ok {
				return 0, fmt.Errorf("line %d: unknown proposition %q", lineNo, n)
			}
			m |= 1 << idx
		}
		return m, nil
	}

	scanner := bufio.NewScanner(r)
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
		kw, args := fields[0], fields[1:]
		switch kw {
		case "props":
			for _, n := range args {
				if _, dup := p.propIndex[n]; dup {
					return nil, fmt.Errorf("line %d: duplicate proposition %q", lineNo, n)
				}
				if len(p.PropNames) >= MaxProps {
					return nil, fmt.Errorf("line %d: too many propositions (max %d)", lineNo, MaxProps)
				}
				p.propIndex[n] = uint32(len(p.PropNames))
				p.PropNames = append(p.PropNames, n)
			}
		case "init", "goal":
			m, err := bits(args, lineNo)
			if err != nil {
				return nil, err
			}
			if kw == "init" {
				p.Init |= m
			} else {
				p.Goal |= m
			}
		case "action":
			flush()
			if len(args) != 1 {
				return nil, fmt.Errorf("line %d: action expects exactly one name", lineNo)
			}
			cur = &Action{Name: args[0]}
		case "pre", "add", "del":
			if cur == nil {
				return nil, fmt.Errorf("line %d: %q outside of an action block", lineNo, kw)
			}
			m, err := bits(args, lineNo)
			if err != nil {
				return nil, err
			}
			switch kw {
			case "pre":
				cur.Pre |= m
			case "add":
				cur.Add |= m
			case "del":
				cur.Del |= m
			}
		case "cost":
			if cur == nil {
				return nil, fmt.Errorf("line %d: cost outside of an action block", lineNo)
			}
			if len(args) != 1 {
				return nil, fmt.Errorf("line %d: cost expects one integer", lineNo)
			}
			c, err := strconv.Atoi(args[0])
			if err != nil || c < 0 {
				return nil, fmt.Errorf("line %d: cost must be a non-negative integer", lineNo)
			}
			cur.Cost = c
		default:
			return nil, fmt.Errorf("line %d: unknown directive %q", lineNo, kw)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	flush()
	return p, nil
}
