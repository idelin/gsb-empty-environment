package netsec

import (
	"strconv"
	"strings"
)

type dfaState struct {
	set    []int
	accept bool
	next   []int
}

type dfa struct {
	states []dfaState
	start  int
}

func determinize(n *nfa, alphabet []byte) *dfa {
	result := &dfa{}
	index := make(map[string]int)
	queue := make([]int, 0)

	addState := func(set []int) int {
		key := stateSetKey(set)
		if id, ok := index[key]; ok {
			return id
		}
		id := len(result.states)
		index[key] = id
		result.states = append(result.states, dfaState{set: set})
		queue = append(queue, id)
		return id
	}

	result.start = addState(epsilonClosure(n, n.start))
	for cursor := 0; cursor < len(queue); cursor++ {
		id := queue[cursor]
		currentSet := result.states[id].set
		accept := false
		for _, state := range currentSet {
			if state == n.accept {
				accept = true
				break
			}
		}

		next := make([]int, len(alphabet))
		for classIndex, representative := range alphabet {
			var targets []int
			for _, state := range currentSet {
				for _, out := range n.edges[state] {
					switch out.kind {
					case edgeLiteral:
						if out.ch == representative {
							targets = append(targets, out.to)
						}
					case edgeNonSlash:
						if representative != '/' {
							targets = append(targets, out.to)
						}
					}
				}
			}
			closed := epsilonClosure(n, targets...)
			next[classIndex] = addState(closed)
		}
		result.states[id].accept = accept
		result.states[id].next = next
	}

	return result
}

func epsilonClosure(n *nfa, seeds ...int) []int {
	seen := make([]bool, len(n.edges))
	stack := append([]int(nil), seeds...)
	for len(stack) > 0 {
		state := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[state] {
			continue
		}
		seen[state] = true
		for _, out := range n.edges[state] {
			if out.kind == edgeEpsilon && !seen[out.to] {
				stack = append(stack, out.to)
			}
		}
	}

	result := make([]int, 0, len(seeds))
	for state, ok := range seen {
		if ok {
			result = append(result, state)
		}
	}
	return result
}

func stateSetKey(states []int) string {
	if len(states) == 0 {
		return ""
	}
	var key strings.Builder
	for i, state := range states {
		if i > 0 {
			key.WriteByte(',')
		}
		key.WriteString(strconv.Itoa(state))
	}
	return key.String()
}

func containsExpression(container, candidate string) bool {
	left, err := parseExpression(container)
	if err != nil {
		return false
	}
	right, err := parseExpression(candidate)
	if err != nil {
		return false
	}
	return expressionContains(left, right)
}

func expressionContains(container, candidate *expression) bool {
	alphabet := alphabetFor(container, candidate)
	containerDFA := determinize(container.nfa, alphabet)
	candidateDFA := determinize(candidate.nfa, alphabet)

	type productState struct {
		left  int
		right int
	}
	start := productState{left: containerDFA.start, right: candidateDFA.start}
	visited := map[productState]struct{}{start: {}}
	queue := []productState{start}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		leftState := containerDFA.states[current.left]
		rightState := candidateDFA.states[current.right]

		// Reaching a candidate key outside the container disproves inclusion.
		if rightState.accept && !leftState.accept {
			return false
		}

		for classIndex := range alphabet {
			next := productState{
				left:  leftState.next[classIndex],
				right: rightState.next[classIndex],
			}
			if _, seen := visited[next]; seen {
				continue
			}
			visited[next] = struct{}{}
			queue = append(queue, next)
		}
	}
	return true
}

func expressionsIntersect(left, right *expression) bool {
	alphabet := alphabetFor(left, right)
	leftDFA := determinize(left.nfa, alphabet)
	rightDFA := determinize(right.nfa, alphabet)

	type productState struct {
		left  int
		right int
	}
	start := productState{left: leftDFA.start, right: rightDFA.start}
	visited := map[productState]struct{}{start: {}}
	queue := []productState{start}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		leftState := leftDFA.states[current.left]
		rightState := rightDFA.states[current.right]

		if leftState.accept && rightState.accept {
			return true
		}

		for classIndex := range alphabet {
			next := productState{
				left:  leftState.next[classIndex],
				right: rightState.next[classIndex],
			}
			if _, seen := visited[next]; seen {
				continue
			}
			visited[next] = struct{}{}
			queue = append(queue, next)
		}
	}
	return false
}

// ExpressionContains reports whether every key matched by candidate is also
// matched by container. Invalid expressions return false.
func ExpressionContains(container, candidate string) bool {
	return containsExpression(container, candidate)
}

// ExpressionsIntersect reports whether container and candidate match at least
// one common key. Invalid expressions return false.
func ExpressionsIntersect(left, right string) bool {
	leftExpression, err := parseExpression(left)
	if err != nil {
		return false
	}
	rightExpression, err := parseExpression(right)
	if err != nil {
		return false
	}
	return expressionsIntersect(leftExpression, rightExpression)
}
