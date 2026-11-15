package netsec

import (
	"bytes"
	"errors"
	"math"
	"sort"
	"strconv"
)

// ErrBudgetExceeded is returned when the redacted output would exceed
// the configured byte budget. No partial output is produced.
var ErrBudgetExceeded = errors.New("netsec: redaction output exceeds budget")

// ErrInvalidNumber is returned for NaN or infinite numbers, which have
// no stable canonical encoding.
var ErrInvalidNumber = errors.New("netsec: NaN or infinite number cannot be encoded")

// redactedPlaceholder is the fixed stand-in for every confidential
// node. It is independent of the node's kind, size, and contents.
const redactedPlaceholder = `"[REDACTED]"`

type encoder struct {
	tainted map[*Value]bool
	count   map[*Value]int
	ids     map[*Value]int
	emitted map[*Value]bool
	nextID  int
	buf     bytes.Buffer
	budget  int
	err     error
}

// countPublic records, for every publicly reachable concrete node, how
// many public incoming edges it has (the root counts as one edge).
// Nodes with more than one incoming edge are shared or sit on a cycle
// and therefore need a stable reference id. Tainted subtrees are never
// entered: taint is forward-closed, so nothing public lives below them.
func (e *encoder) countPublic(root *Value) {
	r := resolve(root)
	if r == nil || e.tainted[r] {
		return
	}
	visited := make(map[*Value]bool)
	e.count[r]++
	stack := []*Value{r}
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if visited[v] {
			continue
		}
		visited[v] = true
		for _, child := range children(v) {
			rc := resolve(child)
			if rc == nil || e.tainted[rc] {
				continue
			}
			e.count[rc]++
			if !visited[rc] {
				stack = append(stack, rc)
			}
		}
	}
}

type task struct {
	node *Value
	lit  string
}

func (e *encoder) write(s string) {
	if e.err != nil {
		return
	}
	if e.buf.Len()+len(s) > e.budget {
		e.err = ErrBudgetExceeded
		return
	}
	e.buf.WriteString(s)
}

// emit serializes the public subgraph with an explicit task stack.
// Reference ids are assigned lazily, in public traversal order only;
// tainted nodes collapse to the placeholder and never receive ids, so
// the numbering cannot leak how many confidential nodes exist.
func (e *encoder) emit(root *Value) {
	stack := []task{{node: root}}
	for len(stack) > 0 && e.err == nil {
		t := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if t.node == nil {
			e.write(t.lit)
			continue
		}
		r := resolve(t.node)
		switch {
		case r == nil:
			e.write("null")
		case e.tainted[r]:
			e.write(redactedPlaceholder)
		case e.count[r] > 1 && e.emitted[r]:
			e.write(`{"$ref":` + strconv.Itoa(e.ids[r]) + `}`)
		case e.count[r] > 1:
			e.emitted[r] = true
			e.ids[r] = e.nextID
			e.nextID++
			e.write(`{"$id":` + strconv.Itoa(e.ids[r]) + `,"value":`)
			stack = append(stack, task{lit: "}"})
			e.pushInline(&stack, r)
		default:
			e.pushInline(&stack, r)
		}
	}
}

func (e *encoder) pushInline(stack *[]task, v *Value) {
	switch v.Kind {
	case KindString:
		e.write(strconv.Quote(v.Str))
	case KindNumber:
		if math.IsNaN(v.Num) || math.IsInf(v.Num, 0) {
			e.err = ErrInvalidNumber
			return
		}
		e.write(strconv.FormatFloat(v.Num, 'g', -1, 64))
	case KindArray:
		*stack = append(*stack, task{lit: "]"})
		for i := len(v.Elems) - 1; i >= 0; i-- {
			*stack = append(*stack, task{node: v.Elems[i]})
			if i > 0 {
				*stack = append(*stack, task{lit: ","})
			}
		}
		*stack = append(*stack, task{lit: "["})
	case KindDict:
		keys := make([]string, 0, len(v.Dict))
		for k := range v.Dict {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		*stack = append(*stack, task{lit: "}"})
		for i := len(keys) - 1; i >= 0; i-- {
			*stack = append(*stack, task{node: v.Dict[keys[i]]})
			*stack = append(*stack, task{lit: strconv.Quote(keys[i]) + ":"})
			if i > 0 {
				*stack = append(*stack, task{lit: ","})
			}
		}
		*stack = append(*stack, task{lit: "{"})
	default:
		e.write("null")
	}
}
