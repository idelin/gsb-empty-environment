package netsec

import "fmt"

// Hard limits of the in-memory model described by the regression task.
const (
	MaxInstructions     = 30
	MaxCallDepth        = 4
	MaxInstructionSteps = 100
	MaxAmount           = 255
	MaxWithdrawalKinds  = 8
)

// Op identifies one primitive of the transaction interpreter. There is no
// byte/word arithmetic beyond the 8-bit amounts described by the task.
type Op int

const (
	// OpBalanceGE is a conditional branch. When the current frame's cached
	// balance is at least the frame's withdrawal amount, execution jumps to
	// Target; otherwise execution falls through to the next instruction.
	OpBalanceGE Op = iota
	// OpBalanceAdd adds the frame's withdrawal amount to its cached balance
	// and publishes the new value to committed account state.
	OpBalanceAdd
	// OpBalanceSub subtracts the frame's withdrawal amount from its cached
	// balance and publishes the new value to committed account state.
	OpBalanceSub
	// OpCashSend pays the frame's withdrawal amount out of contract cash,
	// appends a committed transfer record, and continues.
	OpCashSend
	// OpExternalCallback lets the attacker either re-enter the entry program
	// in a child frame or return immediately. OnChildFailure controls what
	// this frame does if the child reverts.
	OpExternalCallback
	// OpReturn terminates the current frame successfully.
	OpReturn
	// OpRevert terminates the current frame with an explicit revert.
	OpRevert
)

// Common short aliases make programs easier to read.
const (
	OpCheck = OpBalanceGE
	OpAdd   = OpBalanceAdd
	OpSub   = OpBalanceSub
	OpCall  = OpExternalCallback
	OpSend  = OpCashSend
	OpExit  = OpReturn
	OpFail  = OpRevert
)

func (op Op) String() string {
	switch op {
	case OpBalanceGE:
		return "balance-ge"
	case OpBalanceAdd:
		return "balance-add"
	case OpBalanceSub:
		return "balance-sub"
	case OpCashSend:
		return "cash-send"
	case OpExternalCallback:
		return "external-callback"
	case OpReturn:
		return "return"
	case OpRevert:
		return "revert"
	default:
		return fmt.Sprintf("op(%d)", int(op))
	}
}

// ChildFailureMode is the static policy carried by OpExternalCallback.
type ChildFailureMode int

const (
	// ContinueOnChildFailure lets the parent continue after a re-entered child
	// frame reverts. The child's state has already been rolled back.
	ContinueOnChildFailure ChildFailureMode = iota
	// RevertOnChildFailure makes the parent revert together with the failed
	// child. A successful parent also rolls back every child success committed
	// earlier in that parent frame when the parent itself reverts.
	RevertOnChildFailure
)

// Short aliases for the two static callback policies.
const (
	ContinueCallback = ContinueOnChildFailure
	RevertCallback   = RevertOnChildFailure
)

func (m ChildFailureMode) String() string {
	switch m {
	case ContinueOnChildFailure:
		return "continue"
	case RevertOnChildFailure:
		return "revert-parent"
	default:
		return fmt.Sprintf("child-failure-mode(%d)", int(m))
	}
}

// Instruction is one program instruction. Target is only used by
// OpBalanceGE. OnChildFailure is only used by OpExternalCallback.
type Instruction struct {
	Op             Op
	Target         int
	OnChildFailure ChildFailureMode
}

// Program is the entry program. It is deliberately capped at
// MaxInstructions.
type Program []Instruction

// Small constructors keep hand-written regression programs unambiguous.
func Check(target int) Instruction { return Instruction{Op: OpBalanceGE, Target: target} }
func AddBalance() Instruction      { return Instruction{Op: OpBalanceAdd} }
func SubBalance() Instruction      { return Instruction{Op: OpBalanceSub} }
func CashSend() Instruction        { return Instruction{Op: OpCashSend} }
func ExternalCallback(mode ChildFailureMode) Instruction {
	return Instruction{Op: OpExternalCallback, OnChildFailure: mode}
}
func Return() Instruction { return Instruction{Op: OpReturn} }
func Revert() Instruction { return Instruction{Op: OpRevert} }

// Validate checks the static limits and branch targets of a program. A
// Target equal to len(p) is allowed and means "fall off the end", which the
// interpreter treats as a successful return.
func (p Program) Validate() error {
	if len(p) == 0 {
		return fmt.Errorf("program must contain at least one instruction")
	}
	if len(p) > MaxInstructions {
		return fmt.Errorf("program contains %d instructions; maximum is %d", len(p), MaxInstructions)
	}
	for pc, in := range p {
		switch in.Op {
		case OpBalanceGE:
			if in.Target < 0 || in.Target > len(p) {
				return fmt.Errorf("instruction %d: branch target %d outside [0,%d]", pc, in.Target, len(p))
			}
		case OpBalanceAdd, OpBalanceSub, OpCashSend, OpExternalCallback, OpReturn, OpRevert:
			if in.OnChildFailure < ContinueOnChildFailure || in.OnChildFailure > RevertOnChildFailure {
				return fmt.Errorf("instruction %d: invalid child failure mode %d", pc, int(in.OnChildFailure))
			}
		default:
			return fmt.Errorf("instruction %d: unknown opcode %d", pc, int(in.Op))
		}
	}
	return nil
}
