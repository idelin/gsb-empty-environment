package netsec

import (
	"errors"
	"testing"
)

func q0048TableWithExport(name string, terminal []byte) []byte {
	root := &q0048Node{children: []q0048Child{{
		label:  name,
		target: &q0048Node{terminal: terminal},
	}}}
	return q0048EncodeGraph([]*q0048Node{root, root.children[0].target})
}

func TestForwardBindingAndEmptyForwardName(t *testing.T) {
	modules := ModuleTable{
		1: {
			Table:      q0048TableWithExport("alias", q0048ForwardTerminal(2, "target")),
			Executable: []Range{{Start: 0, End: 0x10}},
		},
		2: {
			Table:      q0048TableWithExport("target", q0048AddrTerminal(0x42)),
			Executable: []Range{{Start: 0x40, End: 0x44}},
		},
	}

	exports, err := Load(1, modules, Limits{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	export, ok := exports.Lookup("alias")
	if !ok {
		t.Fatalf("missing alias in %v", exports)
	}
	if export.Module != 2 || export.Address != 0x42 {
		t.Fatalf("alias = %+v, want module 2 address 0x42", export)
	}

	// An empty forward name means the current full export name.
	modules[1] = Module{
		Table:      q0048TableWithExport("same", q0048ForwardTerminal(2, "")),
		Executable: []Range{{Start: 0, End: 0x10}},
	}
	modules[2] = Module{
		Table:      q0048TableWithExport("same", q0048AddrTerminal(0x43)),
		Executable: []Range{{Start: 0x40, End: 0x44}},
	}
	exports, err = Load(1, modules, Limits{})
	if err != nil {
		t.Fatalf("Load with empty forward name: %v", err)
	}
	export = exports["same"]
	if export.Module != 2 || export.Address != 0x43 {
		t.Fatalf("same = %+v, want module 2 address 0x43", export)
	}
}

func TestSharedForwardNodeBindsPerFullName(t *testing.T) {
	shared := &q0048Node{terminal: q0048ForwardTerminal(2, "")}
	root := &q0048Node{children: []q0048Child{
		{label: "a", target: shared},
		{label: "b", target: shared},
	}}
	targetA := &q0048Node{terminal: q0048AddrTerminal(0x10)}
	targetB := &q0048Node{terminal: q0048AddrTerminal(0x11)}
	targetRoot := &q0048Node{children: []q0048Child{
		{label: "a", target: targetA},
		{label: "b", target: targetB},
	}}
	modules := ModuleTable{
		1: {Table: q0048EncodeGraph([]*q0048Node{root, shared})},
		2: {
			Table:      q0048EncodeGraph([]*q0048Node{targetRoot, targetA, targetB}),
			Executable: []Range{{Start: 0x10, End: 0x20}},
		},
	}

	exports, err := Load(1, modules, Limits{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if exports["a"].Address != 0x10 || exports["b"].Address != 0x11 {
		t.Fatalf("exports = %v, want per-name forward binding", exports)
	}
}

func TestCrossModuleForwardCycleIsRejected(t *testing.T) {
	modules := ModuleTable{
		1: {Table: q0048TableWithExport("x", q0048ForwardTerminal(2, "x"))},
		2: {Table: q0048TableWithExport("x", q0048ForwardTerminal(1, "x"))},
	}

	exports, err := Load(1, modules, Limits{})
	if !errors.Is(err, ErrForwardCycle) {
		t.Fatalf("error = %v, want ErrForwardCycle", err)
	}
	if exports != nil {
		t.Fatalf("partial exports published: %v", exports)
	}
}

func TestCyclePublishesNothingAfterAnotherExportResolved(t *testing.T) {
	okay := &q0048Node{terminal: q0048AddrTerminal(1)}
	bad := &q0048Node{terminal: q0048ForwardTerminal(2, "bad")}
	root := &q0048Node{children: []q0048Child{
		{label: "ok", target: okay},
		{label: "bad", target: bad},
	}}
	startTable := q0048EncodeGraph([]*q0048Node{root, okay, bad})
	modules := ModuleTable{
		1: {
			Table:      startTable,
			Executable: []Range{{Start: 0, End: 0x10}},
		},
		2: {Table: q0048TableWithExport("bad", q0048ForwardTerminal(1, "bad"))},
	}

	exports, err := Load(1, modules, Limits{})
	if !errors.Is(err, ErrForwardCycle) {
		t.Fatalf("error = %v, want ErrForwardCycle", err)
	}
	if exports != nil {
		t.Fatalf("partial exports published: %v", exports)
	}
}

func TestBudgetDuringBindingPublishesNothing(t *testing.T) {
	targetA := &q0048Node{terminal: q0048AddrTerminal(0x20)}
	targetB := &q0048Node{terminal: q0048AddrTerminal(0x21)}
	targetRoot := &q0048Node{children: []q0048Child{
		{label: "a", target: targetA},
		{label: "b", target: targetB},
	}}

	alias := &q0048Node{terminal: q0048ForwardTerminal(2, "a")}
	okay := &q0048Node{terminal: q0048AddrTerminal(0x0f)}
	startRoot := &q0048Node{children: []q0048Child{
		{label: "ok", target: okay},
		{label: "alias", target: alias},
	}}
	modules := ModuleTable{
		1: {
			Table:      q0048EncodeGraph([]*q0048Node{startRoot, okay, alias}),
			Executable: []Range{{Start: 0, End: 0x10}},
		},
		2: {
			Table:      q0048EncodeGraph([]*q0048Node{targetRoot, targetA, targetB}),
			Executable: []Range{{Start: 0x20, End: 0x30}},
		},
	}

	exports, err := Load(1, modules, Limits{MaxExports: 2})
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("error = %v, want ErrIncomplete", err)
	}
	if exports != nil {
		t.Fatalf("partial exports published: %v", exports)
	}
}

func TestForwardCycleThroughRenamedExportIsRejected(t *testing.T) {
	modules := ModuleTable{
		1: {Table: q0048TableWithExport("a", q0048ForwardTerminal(2, "b"))},
		2: {Table: q0048TableWithExport("b", q0048ForwardTerminal(1, "a"))},
	}

	_, err := Load(1, modules, Limits{})
	if !errors.Is(err, ErrForwardCycle) {
		t.Fatalf("error = %v, want ErrForwardCycle", err)
	}
}

func TestAddressMustBeInHalfOpenExecutableRange(t *testing.T) {
	cases := []struct {
		name    string
		address uint64
		wantErr bool
	}{
		{name: "start", address: 0x10},
		{name: "inside", address: 0x1f},
		{name: "end is excluded", address: 0x20, wantErr: true},
		{name: "before", address: 0x0f, wantErr: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			modules := ModuleTable{
				1: {
					Table:      q0048TableWithExport("x", q0048AddrTerminal(test.address)),
					Executable: []Range{{Start: 0x10, End: 0x20}},
				},
			}
			exports, err := Load(1, modules, Limits{})
			if test.wantErr {
				if !errors.Is(err, ErrAddressOutOfRange) {
					t.Fatalf("error = %v, want ErrAddressOutOfRange", err)
				}
				if exports != nil {
					t.Fatalf("partial exports published: %v", exports)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got := exports["x"].Address; got != test.address {
				t.Fatalf("address = %#x, want %#x", got, test.address)
			}
		})
	}
}

func TestForwardToMissingExportOrModule(t *testing.T) {
	t.Run("missing export", func(t *testing.T) {
		modules := ModuleTable{
			1: {Table: q0048TableWithExport("x", q0048ForwardTerminal(2, "missing"))},
			2: {
				Table:      q0048TableWithExport("present", q0048AddrTerminal(1)),
				Executable: []Range{{Start: 0, End: 2}},
			},
		}
		_, err := Load(1, modules, Limits{})
		if !errors.Is(err, ErrExportNotFound) {
			t.Fatalf("error = %v, want ErrExportNotFound", err)
		}
	})

	t.Run("missing module", func(t *testing.T) {
		modules := ModuleTable{
			1: {Table: q0048TableWithExport("x", q0048ForwardTerminal(9, "x"))},
		}
		_, err := Load(1, modules, Limits{})
		if !errors.Is(err, ErrModuleNotFound) {
			t.Fatalf("error = %v, want ErrModuleNotFound", err)
		}
	})
}

func TestParseConvenienceForms(t *testing.T) {
	table := q0048TableWithExport("x", q0048AddrTerminal(0x10))
	module := Module{
		Table:      table,
		Executable: []Range{{Start: 0, End: 0x20}},
	}
	exports, err := Parse(table, module, Limits{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if exports["x"].Address != 0x10 {
		t.Fatalf("exports = %v", exports)
	}

	modules := ModuleTable{
		0: {Executable: []Range{{Start: 0, End: 0x20}}},
	}
	exports, err = Parse(table, modules, Limits{})
	if err != nil {
		t.Fatalf("Parse with host: %v", err)
	}
	if exports["x"].Address != 0x10 {
		t.Fatalf("host-backed exports = %v", exports)
	}

	exports, err = Parse(table, 0, modules, Limits{})
	if err != nil {
		t.Fatalf("Parse with integer module index: %v", err)
	}
	if exports["x"].Address != 0x10 {
		t.Fatalf("integer-index exports = %v", exports)
	}
}
