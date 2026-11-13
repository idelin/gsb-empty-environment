package netsec

import (
	"fmt"
	"strconv"
	"strings"
)

type ledger struct {
	balance  int
	cash     int
	received int
}

type executor struct {
	program      Program
	cfg          Config
	state        ledger
	transfers    []Transfer
	steps        int
	decisions    []Decision
	nextDecision int
	amounts      []int
	fatal        error
}

func (x *executor) snapshot() Snapshot {
	return Snapshot{
		Balance:       x.state.balance,
		Cash:          x.state.cash,
		Received:      x.state.received,
		TransferIndex: len(x.transfers),
	}
}

func (x *executor) restore(s Snapshot) {
	x.state = ledger{
		balance:  s.Balance,
		cash:     s.Cash,
		received: s.Received,
	}
}

func (x *executor) rollbackFrame(frame *FrameTrace, reason string) {
	before := x.snapshot()
	var erased []Transfer
	for i := frame.Entry.TransferIndex; i < len(x.transfers); i++ {
		if x.transfers[i].Status != TransferCommitted {
			continue
		}
		x.transfers[i].Status = TransferRolledBack
		x.transfers[i].RollbackCause = frame.Path + ": " + reason
		erased = append(erased, x.transfers[i])
	}
	x.restore(frame.Entry)
	after := x.snapshot()
	frame.BeforeRollback = &before
	frame.AfterRollback = &after
	frame.Outcome = OutcomeReverted
	frame.Reason = reason
	frame.Exit = nil
	frame.Rollback = &RollbackEvidence{
		FramePath:      frame.Path,
		Depth:          frame.Depth,
		Reason:         reason,
		Entry:          frame.Entry,
		BeforeRollback: before,
		AfterRollback:  after,
		Transfers:      erased,
	}
}

func (x *executor) failPlan(format string, args ...any) *FrameTrace {
	x.fatal = fmt.Errorf(format, args...)
	return nil
}

func (x *executor) runFrame(path string, depth, amount int) *FrameTrace {
	frame := &FrameTrace{
		Path:   path,
		Depth:  depth,
		Amount: amount,
		Entry:  x.snapshot(),
	}
	local := x.state.balance
	pc := 0

	for {
		if x.fatal != nil {
			return frame
		}
		if pc >= len(x.program) {
			frame.Outcome = OutcomeReturned
			now := x.snapshot()
			frame.Exit = &now
			return frame
		}
		if x.steps >= x.cfg.MaxSteps {
			frame.Outcome = OutcomeUnknown
			frame.Reason = fmt.Sprintf("instruction budget of %d steps exhausted", x.cfg.MaxSteps)
			return frame
		}

		in := x.program[pc]
		x.steps++
		frame.Instructions++

		switch in.Op {
		case OpBalanceGE:
			if local >= amount {
				pc = in.Target
			} else {
				pc++
			}

		case OpBalanceAdd:
			next := local + amount
			if next < 0 || next > MaxAmount {
				x.rollbackFrame(frame, fmt.Sprintf("balance add overflow: %d + %d", local, amount))
				return frame
			}
			local = next
			x.state.balance = local
			pc++

		case OpBalanceSub:
			next := local - amount
			if next < 0 || next > MaxAmount {
				x.rollbackFrame(frame, fmt.Sprintf("balance subtract underflow: %d - %d", local, amount))
				return frame
			}
			local = next
			x.state.balance = local
			pc++

		case OpCashSend:
			if x.state.cash < amount {
				x.rollbackFrame(frame, fmt.Sprintf("insufficient contract cash: have %d, need %d", x.state.cash, amount))
				return frame
			}
			x.state.cash -= amount
			x.state.received += amount
			x.transfers = append(x.transfers, Transfer{
				Index:     len(x.transfers),
				FramePath: path,
				Depth:     depth,
				PC:        pc,
				Amount:    amount,
				Status:    TransferCommitted,
			})
			pc++

		case OpExternalCallback:
			callbackPC := pc
			if x.nextDecision >= len(x.decisions) {
				return x.failPlan("plan is missing a decision at frame %s pc %d", path, callbackPC)
			}
			decision := x.decisions[x.nextDecision]
			x.nextDecision++
			if !decision.Reenter {
				frame.Callbacks = append(frame.Callbacks, CallbackTrace{
					PC:       callbackPC,
					Decision: decision,
				})
				pc++
				continue
			}
			if depth >= x.cfg.MaxDepth {
				return x.failPlan("frame %s at depth %d cannot re-enter: maximum depth is %d", path, depth, x.cfg.MaxDepth)
			}
			if decision.Amount < 0 || decision.Amount > MaxAmount {
				return x.failPlan("callback amount %d outside [0,%d]", decision.Amount, MaxAmount)
			}

			childIndex := len(frame.Callbacks)
			childPath := path + "/" + strconv.Itoa(childIndex)
			x.amounts = append(x.amounts, decision.Amount)
			pc++
			child := x.runFrame(childPath, depth+1, decision.Amount)
			frame.Callbacks = append(frame.Callbacks, CallbackTrace{
				PC:       callbackPC,
				Decision: decision,
				Child:    child,
			})
			if x.fatal != nil {
				return frame
			}
			if child.Outcome == OutcomeUnknown {
				frame.Outcome = OutcomeUnknown
				frame.Reason = "child call outcome is unknown"
				return frame
			}
			if child.Outcome == OutcomeReverted && in.OnChildFailure == RevertOnChildFailure {
				x.rollbackFrame(frame, "child call reverted; static policy is revert-parent")
				return frame
			}
			continue

		case OpReturn:
			frame.Outcome = OutcomeReturned
			now := x.snapshot()
			frame.Exit = &now
			return frame

		case OpRevert:
			x.rollbackFrame(frame, "explicit revert")
			return frame

		default:
			x.rollbackFrame(frame, fmt.Sprintf("unknown opcode %d", int(in.Op)))
			return frame
		}
	}
}

// ExecutePlan runs a scripted attacker plan from a clean initial state. It is
// the independent replay primitive used to audit a search result.
func ExecutePlan(program Program, cfg Config, plan Plan) (*Execution, error) {
	normalized, err := cfg.normalizedForExecution()
	if err != nil {
		return nil, err
	}
	if err := program.Validate(); err != nil {
		return nil, err
	}
	if plan.RootAmount < 0 || plan.RootAmount > MaxAmount {
		return nil, fmt.Errorf("root amount %d outside [0,%d]", plan.RootAmount, MaxAmount)
	}
	for i, decision := range plan.Decisions {
		if decision.Reenter && (decision.Amount < 0 || decision.Amount > MaxAmount) {
			return nil, fmt.Errorf("decision %d: callback amount %d outside [0,%d]", i, decision.Amount, MaxAmount)
		}
	}

	x := &executor{
		program:   program,
		cfg:       normalized,
		state:     ledger{balance: normalized.InitialBalance, cash: normalized.ContractCash},
		decisions: append([]Decision(nil), plan.Decisions...),
		amounts:   []int{plan.RootAmount},
	}
	initial := x.snapshot()
	root := x.runFrame("0", 1, plan.RootAmount)
	if x.fatal != nil {
		return nil, x.fatal
	}
	if root.Outcome != OutcomeUnknown && x.nextDecision != len(x.decisions) {
		return nil, fmt.Errorf("plan has %d unused callback decisions", len(x.decisions)-x.nextDecision)
	}

	final := x.snapshot()
	depth := 0
	var maxFrameDepth func(*FrameTrace)
	maxFrameDepth = func(frame *FrameTrace) {
		if frame == nil {
			return
		}
		if frame.Depth > depth {
			depth = frame.Depth
		}
		for i := range frame.Callbacks {
			maxFrameDepth(frame.Callbacks[i].Child)
		}
	}
	maxFrameDepth(root)
	execution := &Execution{
		Outcome:        root.Outcome,
		Steps:          x.steps,
		Calls:          len(x.amounts),
		Depth:          depth,
		Root:           root,
		Initial:        initial,
		Final:          final,
		Received:       x.state.received,
		Attack:         root.Outcome == OutcomeReturned && x.state.received > normalized.InitialBalance,
		Plan:           Plan{RootAmount: plan.RootAmount, Decisions: append([]Decision(nil), plan.Decisions...)},
		AmountSequence: append([]int(nil), x.amounts...),
		Transfers:      append([]Transfer(nil), x.transfers...),
	}
	return execution, nil
}

// Replay is an explicit alias for ExecutePlan, provided for callers that want
// to make an independent audit step obvious in their code.
func Replay(program Program, cfg Config, plan Plan) (*Execution, error) {
	return ExecutePlan(program, cfg, plan)
}

// ReplayReport is the result of checking a search result with a fresh replay.
type ReplayReport struct {
	Matches   bool       `json:"matches"`
	Issues    []string   `json:"issues,omitempty"`
	Execution *Execution `json:"execution,omitempty"`
}

// VerifyReplay re-executes an attack's plan from the initial state and checks
// the committed outcome, amount sequence, step count and journal against the
// search result. It is independent of the search's internal BFS state.
func VerifyReplay(program Program, cfg Config, attack *Attack) (*ReplayReport, error) {
	if attack == nil {
		return &ReplayReport{Matches: false, Issues: []string{"nil attack"}}, nil
	}
	execution, err := ExecutePlan(program, cfg, attack.Plan)
	if err != nil {
		return nil, err
	}
	report := &ReplayReport{Matches: true, Execution: execution}
	addIssue := func(format string, args ...any) {
		report.Matches = false
		report.Issues = append(report.Issues, fmt.Sprintf(format, args...))
	}
	if !execution.Attack {
		addIssue("replay is not a committed over-transfer")
	}
	if !equalIntSlices(execution.AmountSequence, attack.AmountSequence) {
		addIssue("amount sequence replay %v != reported %v", execution.AmountSequence, attack.AmountSequence)
	}
	if execution.Calls != attack.Calls {
		addIssue("calls replay %d != reported %d", execution.Calls, attack.Calls)
	}
	if execution.Depth != attack.Depth {
		addIssue("depth replay %d != reported %d", execution.Depth, attack.Depth)
	}
	if execution.Steps != attack.Steps {
		addIssue("steps replay %d != reported %d", execution.Steps, attack.Steps)
	}
	if execution.Received != attack.Received {
		addIssue("received replay %d != reported %d", execution.Received, attack.Received)
	}
	if execution.Final != attack.Final {
		addIssue("final snapshot replay %+v != reported %+v", execution.Final, attack.Final)
	}
	if attack.Execution != nil {
		if len(execution.Transfers) != len(attack.Execution.Transfers) {
			addIssue("transfer journal length replay %d != reported %d",
				len(execution.Transfers), len(attack.Execution.Transfers))
		} else {
			for i := range execution.Transfers {
				if execution.Transfers[i] != attack.Execution.Transfers[i] {
					addIssue("transfer %d replay %+v != reported %+v",
						i, execution.Transfers[i], attack.Execution.Transfers[i])
				}
			}
		}
	}
	return report, nil
}

func equalIntSlices(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// FormatAmountSequence renders an amount sequence in the canonical tie-break
// order used by Search.
func FormatAmountSequence(amounts []int) string {
	parts := make([]string, len(amounts))
	for i, amount := range amounts {
		parts[i] = strconv.Itoa(amount)
	}
	return strings.Join(parts, ",")
}
