package netsec

import (
	"bytes"
	"strings"
	"testing"
)

func TestPlanBasic(t *testing.T) {
	initial := defaultMemory()
	plan, err := Plan([]uintptr{12}, initial, map[int]byte{12: 0x41})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	wantFormat := append(bytes.Repeat([]byte{' '}, 0x41), []byte("%1$hhn")...)
	if !bytes.Equal(plan.Format, wantFormat) {
		t.Fatalf("Format = %q, want %q", plan.Format, wantFormat)
	}
	if plan.FinalCount != 0x41 {
		t.Fatalf("FinalCount = %d, want %d", plan.FinalCount, 0x41)
	}
	if len(plan.Writes) != 1 {
		t.Fatalf("len(Writes) = %d, want 1", len(plan.Writes))
	}
	wantWrite := Write{Arg: 1, Address: 12, Offset: 12, FormatOffset: 0x41, Count: 0x41, Value: 0x41}
	if plan.Writes[0] != wantWrite {
		t.Fatalf("Writes[0] = %+v, want %+v", plan.Writes[0], wantWrite)
	}
	for offset, value := range plan.FinalMemory {
		if offset == 12 {
			if value != 0x41 {
				t.Fatalf("target offset = 0x%02x, want 0x41", value)
			}
		} else if value != 0 {
			t.Fatalf("non-target offset %d changed to 0x%02x", offset, value)
		}
	}
}

func TestPlanOrdersByCountThenWritesThenFormat(t *testing.T) {
	initial := make([]byte, MaxMemoryBytes)
	for i := range initial {
		initial[i] = 0xee
	}

	targets := map[int]byte{
		0: 0,
		1: 1,
		2: 255,
		3: 5,
	}
	plan, err := Plan([]uintptr{0, 1, 2, 3}, initial, targets)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	wantFormat := "%1$hhn" + " " + "%2$hhn" + "    " + "%4$hhn" +
		strings.Repeat(" ", 250) + "%3$hhn"
	if string(plan.Format) != wantFormat {
		t.Fatalf("Format = %q, want %q", plan.Format, wantFormat)
	}
	if plan.FinalCount != 255 {
		t.Fatalf("FinalCount = %d, want 255", plan.FinalCount)
	}
	if len(plan.Writes) != len(targets) {
		t.Fatalf("len(Writes) = %d, want %d", len(plan.Writes), len(targets))
	}
	for offset, want := range targets {
		if plan.FinalMemory[offset] != want {
			t.Fatalf("FinalMemory[%d] = 0x%02x, want 0x%02x", offset, plan.FinalMemory[offset], want)
		}
	}
	for offset := 4; offset < len(initial); offset++ {
		if plan.FinalMemory[offset] != 0xee {
			t.Fatalf("non-target offset %d changed to 0x%02x", offset, plan.FinalMemory[offset])
		}
	}
}

func TestPlanSameAddressArguments(t *testing.T) {
	initial := []byte{0, 0}
	plan, err := Plan([]uintptr{0, 0}, initial, map[int]byte{0: 5})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	wantFormat := strings.Repeat(" ", 5) + "%1$hhn"
	if string(plan.Format) != wantFormat {
		t.Fatalf("Format = %q, want %q", plan.Format, wantFormat)
	}
	if len(plan.Writes) != 1 || plan.Writes[0].Arg != 1 {
		t.Fatalf("Writes = %+v, want one write through argument 1", plan.Writes)
	}
	if plan.FinalCount != 5 || !bytes.Equal(plan.FinalMemory, []byte{5, 0}) {
		t.Fatalf("plan = count %d, memory % x", plan.FinalCount, plan.FinalMemory)
	}
}

func TestPlanSkipsUnchangedTargets(t *testing.T) {
	t.Run("unchanged target needs no argument", func(t *testing.T) {
		initial := []byte{0x11, 0x22}
		targets := map[int]byte{0: 0x11, 1: 0x33}
		plan, err := Plan([]uintptr{1}, initial, targets)
		if err != nil {
			t.Fatalf("Plan() error = %v", err)
		}

		wantFormat := strings.Repeat(" ", 0x33) + "%1$hhn"
		if string(plan.Format) != wantFormat {
			t.Fatalf("Format = %q, want %q", plan.Format, wantFormat)
		}
		if plan.FinalCount != 0x33 {
			t.Fatalf("FinalCount = %d, want %d", plan.FinalCount, 0x33)
		}
		if len(plan.Writes) != 1 || plan.Writes[0].Offset != 1 {
			t.Fatalf("Writes = %+v, want only offset 1", plan.Writes)
		}
		if !bytes.Equal(plan.FinalMemory, []byte{0x11, 0x33}) {
			t.Fatalf("FinalMemory = % x", plan.FinalMemory)
		}
	})

	t.Run("all targets already satisfied", func(t *testing.T) {
		initial := []byte{0x11, 0x22}
		plan, err := Plan(nil, initial, map[int]byte{0: 0x11})
		if err != nil {
			t.Fatalf("Plan() error = %v", err)
		}
		if len(plan.Format) != 0 {
			t.Fatalf("Format = %q, want empty", plan.Format)
		}
		if plan.FinalCount != 0 || len(plan.Writes) != 0 {
			t.Fatalf("plan = %+v, want zero count and no writes", plan)
		}
		if !bytes.Equal(plan.FinalMemory, initial) {
			t.Fatalf("FinalMemory = % x, want % x", plan.FinalMemory, initial)
		}
	})

	t.Run("satisfied large value does not raise final count", func(t *testing.T) {
		initial := []byte{0x50, 0}
		plan, err := Plan([]uintptr{1}, initial, map[int]byte{0: 0x50, 1: 0x10})
		if err != nil {
			t.Fatalf("Plan() error = %v", err)
		}
		if plan.FinalCount != 0x10 {
			t.Fatalf("FinalCount = %d, want %d", plan.FinalCount, 0x10)
		}
		if !bytes.Equal(plan.FinalMemory, []byte{0x50, 0x10}) {
			t.Fatalf("FinalMemory = % x", plan.FinalMemory)
		}
	})
}

func TestPlanZeroTargetUsesZeroCount(t *testing.T) {
	initial := make([]byte, MaxMemoryBytes)
	for i := range initial {
		initial[i] = 0xff
	}
	plan, err := Plan([]uintptr{0}, initial, map[int]byte{0: 0})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if string(plan.Format) != "%1$hhn" {
		t.Fatalf("Format = %q, want %q", plan.Format, "%1$hhn")
	}
	if plan.FinalCount != 0 || plan.FinalMemory[0] != 0 {
		t.Fatalf("plan = count %d, memory[0] = 0x%02x", plan.FinalCount, plan.FinalMemory[0])
	}
}

func TestPlanAdjacentZeroOutputWrites(t *testing.T) {
	initial := make([]byte, MaxMemoryBytes)
	for i := range initial {
		initial[i] = 0xff
	}
	plan, err := Plan([]uintptr{0, 1}, initial, map[int]byte{0: 0, 1: 0})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if string(plan.Format) != "%1$hhn%2$hhn" {
		t.Fatalf("Format = %q, want adjacent zero-output writes", plan.Format)
	}
	if plan.FinalCount != 0 || len(plan.Writes) != 2 {
		t.Fatalf("plan = count %d, writes %+v", plan.FinalCount, plan.Writes)
	}
	if plan.FinalMemory[0] != 0 || plan.FinalMemory[1] != 0 {
		t.Fatalf("FinalMemory = % x", plan.FinalMemory)
	}
}

func TestPlanUsesLowestArgumentNumberForSameAddress(t *testing.T) {
	plan, err := Plan([]uintptr{0, 0}, []byte{0xaa}, map[int]byte{0: 1})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if len(plan.Writes) != 1 || plan.Writes[0].Arg != 1 {
		t.Fatalf("Writes = %+v, want argument 1", plan.Writes)
	}
}

func TestPlanAtBaseAddress(t *testing.T) {
	const base = uintptr(0x2000)
	plan, err := PlanAt(base, []uintptr{base + 7}, defaultMemory(), map[int]byte{7: 9})
	if err != nil {
		t.Fatalf("PlanAt() error = %v", err)
	}
	if plan.Writes[0].Address != base+7 || plan.Writes[0].Offset != 7 {
		t.Fatalf("Writes[0] = %+v", plan.Writes[0])
	}
	if plan.FinalCount != 9 || plan.FinalMemory[7] != 9 {
		t.Fatalf("plan = count %d, memory[7] = 0x%02x", plan.FinalCount, plan.FinalMemory[7])
	}
}

func TestPlanErrors(t *testing.T) {
	tooManyArgs := make([]uintptr, MaxArgs+1)
	manyTargets := make(map[int]byte, MaxArgs+1)
	for offset := 0; offset <= MaxArgs; offset++ {
		manyTargets[offset] = 1
	}
	eightArgs := make([]uintptr, MaxArgs)
	for i := range eightArgs {
		eightArgs[i] = uintptr(i)
	}

	tests := []struct {
		name    string
		args    []uintptr
		initial []byte
		targets map[int]byte
	}{
		{
			name:    "changed target without argument",
			args:    []uintptr{1},
			initial: defaultMemory(),
			targets: map[int]byte{0: 1},
		},
		{
			name:    "target outside memory",
			args:    []uintptr{0},
			initial: defaultMemory(),
			targets: map[int]byte{MaxMemoryBytes: 1},
		},
		{
			name:    "negative target offset",
			args:    []uintptr{0},
			initial: defaultMemory(),
			targets: map[int]byte{-1: 1},
		},
		{
			name:    "too many arguments",
			args:    tooManyArgs,
			initial: defaultMemory(),
			targets: nil,
		},
		{
			name:    "empty memory",
			args:    nil,
			initial: []byte{},
			targets: nil,
		},
		{
			name:    "memory too large",
			args:    nil,
			initial: make([]byte, MaxMemoryBytes+1),
			targets: nil,
		},
		{
			name:    "more changed targets than arguments",
			args:    eightArgs,
			initial: defaultMemory(),
			targets: manyTargets,
		},
		{
			name:    "argument address outside memory",
			args:    []uintptr{MaxMemoryBytes},
			initial: defaultMemory(),
			targets: map[int]byte{0: 1},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Plan(test.args, test.initial, test.targets); err == nil {
				t.Fatal("Plan() succeeded, want an error")
			}
		})
	}
}

func TestPlanOutputReplaysToTargets(t *testing.T) {
	initial := []byte{0xde, 0xad, 0xbe, 0xef, 0, 0, 0, 0}
	targets := map[int]byte{0: 0, 3: 3, 7: 200}
	args := []uintptr{0, 3, 7}
	plan, err := Plan(args, initial, targets)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	replayed, err := Replay(plan.Format, args, initial)
	if err != nil {
		t.Fatalf("Replay(plan.Format) error = %v", err)
	}
	if replayed.FinalCount != plan.FinalCount {
		t.Fatalf("replayed count = %d, plan count = %d", replayed.FinalCount, plan.FinalCount)
	}
	if !bytes.Equal(replayed.FinalMemory, plan.FinalMemory) {
		t.Fatalf("replayed memory = % x, plan memory = % x", replayed.FinalMemory, plan.FinalMemory)
	}
}
