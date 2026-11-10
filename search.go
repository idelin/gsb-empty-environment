package main

import (
	"fmt"
	"strings"
)

type SearchResult struct {
	Initial          State
	Visited          int
	ValidTransitions int
	Violations       int
	Shortest         []Event
	ShortestState    State
	Exhausted        bool
}

type queueItem struct {
	state State
	path  []Event
}

func Search(stopAtFirst bool) SearchResult {
	initial := InitialState()
	visited := map[stateKey]struct{}{
		initial.key(): {},
	}
	queue := []queueItem{{state: initial}}
	result := SearchResult{Initial: initial}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		for _, event := range PossibleEvents() {
			next, enabled := current.state.Apply(event)
			if !enabled {
				continue
			}
			result.ValidTransitions++

			if event.Kind == Boot && next.Volatile.RunningVersion < next.Persistent.Confirmed {
				result.Violations++
				if result.Shortest == nil {
					result.Shortest = append(append([]Event(nil), current.path...), event)
					result.ShortestState = next
				}
				if stopAtFirst {
					result.Visited = len(visited)
					return result
				}
			}

			key := next.key()
			if _, seen := visited[key]; seen {
				continue
			}
			visited[key] = struct{}{}
			path := append(append([]Event(nil), current.path...), event)
			queue = append(queue, queueItem{state: next, path: path})
		}
	}

	result.Visited = len(visited)
	result.Exhausted = true
	return result
}

func (result SearchResult) String() string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "finite bound: versions={empty,v1,v2}, slots={A,B}\n")
	fmt.Fprintf(&builder, "reachable states visited: %d\n", result.Visited)
	fmt.Fprintf(&builder, "valid transitions explored: %d\n", result.ValidTransitions)
	fmt.Fprintf(&builder, "violating boot transitions: %d\n", result.Violations)
	fmt.Fprintf(&builder, "exhausted all reachable states: %t\n", result.Exhausted)

	if result.Shortest == nil {
		builder.WriteString("result: no counterexample found\n")
		return builder.String()
	}

	fmt.Fprintf(&builder, "result: counterexample found with %d events\n", len(result.Shortest))
	for index, event := range result.Shortest {
		fmt.Fprintf(&builder, "%d. %s\n", index+1, event)
	}
	fmt.Fprintf(&builder, "final state: %s\n", result.ShortestState)
	builder.WriteString("replay: go run . replay " + eventsCommand(result.Shortest) + "\n")
	return builder.String()
}

func eventsCommand(events []Event) string {
	parts := make([]string, len(events))
	for index, event := range events {
		parts[index] = event.String()
	}
	return strings.Join(parts, " ")
}

func Replay(events []Event) error {
	state := InitialState()
	fmt.Printf("initial: %s\n", state)

	for index, event := range events {
		next, enabled := state.Apply(event)
		if !enabled {
			return fmt.Errorf("event %d (%s) is not enabled in state %s", index+1, event, state)
		}

		status := "ok"
		if event.Kind == Boot && next.Volatile.RunningVersion < next.Persistent.Confirmed {
			status = fmt.Sprintf(
				"VIOLATION: running=%s < confirmed=%s (fuse=%s)",
				next.Volatile.RunningVersion,
				next.Persistent.Confirmed,
				next.Persistent.Fuse,
			)
		}

		state = next
		fmt.Printf("%d. %-18s %s [%s]\n", index+1, event, state, status)
	}
	return nil
}
