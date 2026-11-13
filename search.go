package netsec

import (
	"container/heap"
	"fmt"
	"strconv"
	"strings"
)

// SearchStatus is the conclusion of an attack search.
type SearchStatus string

const (
	// StatusAttack means a call tree committed more than the initial balance.
	StatusAttack SearchStatus = "attack"
	// StatusSafe means every reachable tree terminated within the step budget
	// and none committed more than the initial balance.
	StatusSafe SearchStatus = "safe"
	// StatusUnknown means at least one reachable execution exceeded the step
	// budget, so safety was not proven.
	StatusUnknown SearchStatus = "unknown"
)

// Attack is the shortest transfer-breaking call tree found by Search. The
// Execution field is a fresh independent replay of Plan.
type Attack struct {
	Plan           Plan       `json:"plan"`
	AmountSequence []int      `json:"amountSequence"`
	Calls          int        `json:"calls"`
	Depth          int        `json:"depth"`
	Steps          int        `json:"steps"`
	Received       int        `json:"received"`
	Final          Snapshot   `json:"final"`
	Execution      *Execution `json:"execution"`
}

// SearchResult is the result of Search.
type SearchResult struct {
	Status       SearchStatus `json:"status"`
	Attack       *Attack      `json:"attack,omitempty"`
	Reason       string       `json:"reason,omitempty"`
	Explorations int          `json:"explorations"`
}

type searchLedger struct {
	balance  int
	cash     int
	received int
}

type searchFrame struct {
	depth         int
	amount        int
	pc            int
	local         int
	entryBalance  int
	entryCash     int
	entryReceived int
	pending       bool
	pendingMode   ChildFailureMode
}

type searchNode struct {
	state     searchLedger
	stack     []searchFrame
	steps     int
	calls     int
	amounts   []int
	decisions []Decision
	order     int64
}

func (n *searchNode) clone() *searchNode {
	clone := &searchNode{
		state: n.state,
		steps: n.steps,
		calls: n.calls,
	}
	clone.stack = append([]searchFrame(nil), n.stack...)
	clone.amounts = append([]int(nil), n.amounts...)
	clone.decisions = append([]Decision(nil), n.decisions...)
	return clone
}

type searchFrameKey struct {
	depth         int
	amount        int
	pc            int
	local         int
	entryBalance  int
	entryCash     int
	entryReceived int
	pending       bool
	pendingMode   ChildFailureMode
}

type searchStateKey struct {
	balance  int
	cash     int
	received int
	depth    int
	frames   [MaxCallDepth]searchFrameKey
}

type searchVisitKey struct {
	state searchStateKey
	steps int
	seq   string
}

func (n *searchNode) stateKey() searchStateKey {
	key := searchStateKey{
		balance:  n.state.balance,
		cash:     n.state.cash,
		received: n.state.received,
		depth:    len(n.stack),
	}
	for i, frame := range n.stack {
		key.frames[i] = searchFrameKey{
			depth:         frame.depth,
			amount:        frame.amount,
			pc:            frame.pc,
			local:         frame.local,
			entryBalance:  frame.entryBalance,
			entryCash:     frame.entryCash,
			entryReceived: frame.entryReceived,
			pending:       frame.pending,
			pendingMode:   frame.pendingMode,
		}
	}
	return key
}

func sequenceKey(amounts []int) string {
	var b strings.Builder
	for i, amount := range amounts {
		if i > 0 {
			b.WriteByte('/')
		}
		b.WriteString(strconv.Itoa(amount))
	}
	return b.String()
}

type searchQueue []*searchNode

func (q searchQueue) Len() int { return len(q) }
func (q searchQueue) Less(i, j int) bool {
	a, b := q[i], q[j]
	if a.calls != b.calls {
		return a.calls < b.calls
	}
	for k := 0; k < len(a.amounts) && k < len(b.amounts); k++ {
		if a.amounts[k] != b.amounts[k] {
			return a.amounts[k] < b.amounts[k]
		}
	}
	if len(a.amounts) != len(b.amounts) {
		return len(a.amounts) < len(b.amounts)
	}
	if a.steps != b.steps {
		return a.steps < b.steps
	}
	return a.order < b.order
}
func (q searchQueue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q *searchQueue) Push(x any)   { *q = append(*q, x.(*searchNode)) }
func (q *searchQueue) Pop() any {
	old := *q
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	*q = old[:n-1]
	return item
}

type searcher struct {
	program      Program
	cfg          Config
	queue        searchQueue
	visited      map[searchVisitKey]struct{}
	unknown      bool
	explorations int
	order        int64
}

func (s *searcher) enqueue(node *searchNode) {
	key := searchVisitKey{
		state: node.stateKey(),
		steps: node.steps,
		seq:   sequenceKey(node.amounts),
	}
	if _, seen := s.visited[key]; seen {
		return
	}
	s.visited[key] = struct{}{}
	node.order = s.order
	s.order++
	heap.Push(&s.queue, node)
}

func (s *searcher) restoreFrame(node *searchNode, frame searchFrame) {
	node.state = searchLedger{
		balance:  frame.entryBalance,
		cash:     frame.entryCash,
		received: frame.entryReceived,
	}
}

// revertTopFrame pops the top frame, restores it, and applies the parent's
// static child-failure policy. This mirrors the recursive executor.
func (s *searcher) revertTopFrame(node *searchNode) {
	frame := node.stack[len(node.stack)-1]
	s.restoreFrame(node, frame)
	node.stack = node.stack[:len(node.stack)-1]

	for len(node.stack) > 0 {
		parent := &node.stack[len(node.stack)-1]
		if !parent.pending {
			break
		}
		parent.pending = false
		if parent.pendingMode != RevertOnChildFailure {
			break
		}
		s.restoreFrame(node, *parent)
		node.stack = node.stack[:len(node.stack)-1]
	}
}

func (s *searcher) finishTopFrame(node *searchNode) {
	node.stack = node.stack[:len(node.stack)-1]
	if len(node.stack) > 0 {
		parent := &node.stack[len(node.stack)-1]
		parent.pending = false
	}
}

func (s *searcher) expand(node *searchNode) ([]*searchNode, bool) {
	if len(node.stack) == 0 {
		return nil, false
	}

	top := node.stack[len(node.stack)-1]
	if top.pc >= len(s.program) {
		next := node.clone()
		s.finishTopFrame(next)
		return []*searchNode{next}, false
	}
	if node.steps >= s.cfg.MaxSteps {
		return nil, true
	}

	in := s.program[top.pc]
	if in.Op == OpExternalCallback {
		var out []*searchNode

		immediate := node.clone()
		immediate.steps++
		immediate.stack[len(immediate.stack)-1].pc++
		immediate.decisions = append(immediate.decisions, ReturnNow())
		out = append(out, immediate)

		if top.depth < s.cfg.MaxDepth {
			for _, amount := range s.cfg.WithdrawalAmounts {
				reenter := node.clone()
				reenter.steps++
				reenter.stack[len(reenter.stack)-1].pc++
				reenter.stack[len(reenter.stack)-1].pending = true
				reenter.stack[len(reenter.stack)-1].pendingMode = in.OnChildFailure
				reenter.stack = append(reenter.stack, searchFrame{
					depth:         top.depth + 1,
					amount:        amount,
					pc:            0,
					local:         reenter.state.balance,
					entryBalance:  reenter.state.balance,
					entryCash:     reenter.state.cash,
					entryReceived: reenter.state.received,
				})
				reenter.calls++
				reenter.amounts = append(reenter.amounts, amount)
				reenter.decisions = append(reenter.decisions, Enter(amount))
				out = append(out, reenter)
			}
		}
		return out, false
	}

	next := node.clone()
	next.steps++
	top = next.stack[len(next.stack)-1]

	switch in.Op {
	case OpBalanceGE:
		if top.local >= top.amount {
			next.stack[len(next.stack)-1].pc = in.Target
		} else {
			next.stack[len(next.stack)-1].pc++
		}

	case OpBalanceAdd:
		value := top.local + top.amount
		if value < 0 || value > MaxAmount {
			s.revertTopFrame(next)
			return []*searchNode{next}, false
		}
		next.stack[len(next.stack)-1].local = value
		next.state.balance = value
		next.stack[len(next.stack)-1].pc++

	case OpBalanceSub:
		value := top.local - top.amount
		if value < 0 || value > MaxAmount {
			s.revertTopFrame(next)
			return []*searchNode{next}, false
		}
		next.stack[len(next.stack)-1].local = value
		next.state.balance = value
		next.stack[len(next.stack)-1].pc++

	case OpCashSend:
		if next.state.cash < top.amount {
			s.revertTopFrame(next)
			return []*searchNode{next}, false
		}
		next.state.cash -= top.amount
		next.state.received += top.amount
		next.stack[len(next.stack)-1].pc++

	case OpReturn:
		s.finishTopFrame(next)

	case OpRevert:
		s.revertTopFrame(next)

	default:
		s.revertTopFrame(next)
	}
	return []*searchNode{next}, false
}

func (s *searcher) betterAttack(candidate, best *searchNode) bool {
	if best == nil {
		return true
	}
	if candidate.calls != best.calls {
		return candidate.calls < best.calls
	}
	for i := 0; i < len(candidate.amounts) && i < len(best.amounts); i++ {
		if candidate.amounts[i] != best.amounts[i] {
			return candidate.amounts[i] < best.amounts[i]
		}
	}
	if len(candidate.amounts) != len(best.amounts) {
		return len(candidate.amounts) < len(best.amounts)
	}
	return candidate.steps < best.steps
}

// Search exhaustively explores attacker call trees in increasing call-count
// order. At each call count it orders amount sequences lexicographically, so
// the first committed over-transfer is the shortest tree with the canonical
// tie-break. An execution that exhausts MaxSteps is reported as unknown and
// never as an attack; only state committed after the root returns counts.
func Search(program Program, cfg Config) (*SearchResult, error) {
	normalized, err := cfg.normalized()
	if err != nil {
		return nil, err
	}
	if err := program.Validate(); err != nil {
		return nil, err
	}

	s := &searcher{
		program: program,
		cfg:     normalized,
		visited: make(map[searchVisitKey]struct{}),
	}
	heap.Init(&s.queue)

	for _, amount := range normalized.WithdrawalAmounts {
		root := &searchNode{
			state: searchLedger{
				balance: normalized.InitialBalance,
				cash:    normalized.ContractCash,
			},
			stack: []searchFrame{{
				depth:         1,
				amount:        amount,
				pc:            0,
				local:         normalized.InitialBalance,
				entryBalance:  normalized.InitialBalance,
				entryCash:     normalized.ContractCash,
				entryReceived: 0,
			}},
			calls:   1,
			amounts: []int{amount},
		}
		s.enqueue(root)
	}

	var best *searchNode
	for s.queue.Len() > 0 {
		node := heap.Pop(&s.queue).(*searchNode)
		s.explorations++

		if best != nil {
			if node.calls > best.calls {
				break
			}
			if node.calls == best.calls && compareSequences(node.amounts, best.amounts) > 0 {
				break
			}
		}

		if len(node.stack) == 0 {
			if node.state.received > normalized.InitialBalance {
				if s.betterAttack(node, best) {
					best = node
				}
			}
			continue
		}

		children, unknown := s.expand(node)
		if unknown {
			s.unknown = true
		}
		for _, child := range children {
			s.enqueue(child)
		}
	}

	if best != nil {
		plan := Plan{
			RootAmount: best.amounts[0],
			Decisions:  append([]Decision(nil), best.decisions...),
		}
		execution, err := ExecutePlan(program, normalized, plan)
		if err != nil {
			return nil, fmt.Errorf("replay of found plan failed: %w", err)
		}
		if !execution.Attack {
			return nil, fmt.Errorf("internal error: search plan did not replay as a committed over-transfer")
		}
		return &SearchResult{
			Status: StatusAttack,
			Attack: &Attack{
				Plan:           plan,
				AmountSequence: append([]int(nil), execution.AmountSequence...),
				Calls:          execution.Calls,
				Depth:          execution.Depth,
				Steps:          execution.Steps,
				Received:       execution.Received,
				Final:          execution.Final,
				Execution:      execution,
			},
			Reason:       "committed received amount exceeds the initial account balance",
			Explorations: s.explorations,
		}, nil
	}

	if s.unknown {
		return &SearchResult{
			Status:       StatusUnknown,
			Reason:       "at least one reachable execution exceeded the instruction budget",
			Explorations: s.explorations,
		}, nil
	}
	return &SearchResult{
		Status:       StatusSafe,
		Reason:       "no call tree committed more than the initial account balance",
		Explorations: s.explorations,
	}, nil
}

func compareSequences(a, b []int) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}
