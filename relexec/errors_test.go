package relexec

import (
	"errors"
	"testing"
)

func TestRunNilAndEdgePrograms(t *testing.T) {
	if _, err := Run(nil, nil, 4, UnlimitedBudget); err == nil {
		t.Error("nil program must error")
	}

	// An empty program falls off the end immediately: normal completion with
	// r0 taken from the first input byte and no writes.
	out, err := Run(&Program{Code: nil}, []byte{23}, 4, UnlimitedBudget)
	if err != nil {
		t.Fatalf("empty program: %v", err)
	}
	if out.OOB || out.Halted || out.Return != 23 || out.Steps != 0 || len(out.Writes) != 0 {
		t.Fatalf("empty program outcome: %+v", out)
	}

	if _, err := Run(&Program{Code: []Instr{halt()}}, nil, 4, -2); err == nil {
		t.Error("budget below UnlimitedBudget must error")
	}
}

func TestVerifyPropagatesProgramErrors(t *testing.T) {
	good := &Program{Code: []Instr{halt()}}
	bad := &Program{Code: []Instr{{Op: OpStore, Src1: 9, Src2: 0}}}
	cfg := Config{InputLen: 1, ArrayLen: 4, Budget: UnlimitedBudget}

	if _, err := Verify(bad, good, cfg); err == nil {
		t.Error("invalid old program must surface as error")
	}
	if _, err := Verify(good, bad, cfg); err == nil {
		t.Error("invalid new program must surface as error")
	}
	if _, err := Verify(nil, good, cfg); err == nil {
		t.Error("nil old program must surface as error")
	}

	for _, badCfg := range []Config{
		{InputLen: -1, ArrayLen: 4, Budget: UnlimitedBudget},
		{InputLen: MaxInputLen + 1, ArrayLen: 4, Budget: UnlimitedBudget},
		{InputLen: 1, ArrayLen: 0, Budget: UnlimitedBudget},
		{InputLen: 1, ArrayLen: MaxArrayLen + 1, Budget: UnlimitedBudget},
		{InputLen: 1, ArrayLen: 4, Budget: -2},
		{InputLen: 1, ArrayLen: 4, Budget: 0},
	} {
		if _, err := Verify(good, good, badCfg); err == nil {
			t.Errorf("invalid config %+v must error", badCfg)
		}
	}
}

func TestRunBudgetSentinelErrorIdentity(t *testing.T) {
	prog := &Program{Code: []Instr{{Op: OpConst, Dst: 0, Imm: 1}, halt()}}
	_, err := Run(prog, nil, 4, 1)
	if !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("got %v want ErrBudgetExhausted", err)
	}
}

func TestStringRenderers(t *testing.T) {
	if Op(255).String() == "" {
		t.Error("unknown op must still render")
	}
	for op := OpHalt; op <= OpStore; op++ {
		if op.String() == "" {
			t.Errorf("opcode %d renders empty", op)
		}
	}
	for v := Verdict(0); v <= VerdictUnknown; v++ {
		if v.String() == "" {
			t.Errorf("verdict %d renders empty", v)
		}
	}
	for r := ReasonNone; r <= ReasonWriteMismatch; r++ {
		if r.String() == "" {
			t.Errorf("reason %d renders empty", r)
		}
	}
	if Verdict(99).String() == "" || CounterexampleReason(99).String() == "" {
		t.Error("unknown enum values must still render")
	}
}
