package netsec

import "testing"

func TestDifferentialSummarizedCall(t *testing.T) {
	libs := Libraries{
		"ctcmp": {
			Name: "ctcmp",
			Params: []ParamSummary{{
				TimingBits: Bits(0, 1, 2, 3, 4, 5, 6, 7),
			}},
			ResultWidth: 1,
			Model: func(args []uint64) uint64 {
				if args[0] == 0 {
					return 1
				}
				return 0
			},
		},
	}
	builder := NewBuilder("diff-call")
	secret := builder.Secret("secret", 8)
	mask := builder.Const("mask", 8, 0x0f)
	masked := builder.And("masked", secret, mask)
	builder.Call("compare", CallSpec{
		Library:       "ctcmp",
		AllowedPublic: []Mask{0x0f},
	}, masked)
	program := builder.Build()

	report := mustAnalyze(t, program, libs)
	if len(report.Findings) != 0 {
		t.Fatalf("contracted call produced findings: %s", findingsText(report.Findings))
	}

	for bit := 0; bit < 4; bit++ {
		diff, err := Differential(program, libs, map[string]uint64{"secret": 0}, "secret", bit)
		if err != nil {
			t.Fatal(err)
		}
		if !diff.Leaked {
			t.Fatalf("allowed-public bit %d did not reach the callee timing model", bit)
		}
	}
	for bit := 4; bit < 8; bit++ {
		diff, err := Differential(program, libs, map[string]uint64{"secret": 0}, "secret", bit)
		if err != nil {
			t.Fatal(err)
		}
		if diff.Leaked {
			t.Fatalf("masked-out bit %d changed call timing: %+v", bit, diff)
		}
	}

	builder = NewBuilder("diff-raw-call")
	secret = builder.Secret("secret", 8)
	builder.Call("compare", CallSpec{Library: "ctcmp"}, secret)
	program = builder.Build()
	report = mustAnalyze(t, program, libs)
	if got := len(report.Findings); got != 8 {
		t.Fatalf("findings = %d, want 8", got)
	}
	for bit := 0; bit < 8; bit++ {
		diff, err := Differential(program, libs, map[string]uint64{"secret": 0}, "secret", bit)
		if err != nil {
			t.Fatal(err)
		}
		if !diff.Leaked {
			t.Fatalf("raw secret bit %d did not change call timing", bit)
		}
	}
}

func TestDifferentialAllReportsEveryLeakingBit(t *testing.T) {
	builder := NewBuilder("all-bits")
	secret := builder.Secret("secret", 8)
	builder.Lookup("table", secret, 8)
	program := builder.Build()

	if report := mustAnalyze(t, program, nil); len(report.Findings) != 8 {
		t.Fatalf("findings = %d, want 8", len(report.Findings))
	}
	diffs, err := DifferentialAll(program, nil, map[string]uint64{"secret": 0}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	for bit, diff := range diffs {
		if !diff.Leaked {
			t.Fatalf("bit %d did not leak in differential execution", bit)
		}
		if len(diff.DifferingEvents) == 0 {
			t.Fatalf("bit %d has no differing event", bit)
		}
	}
}

func TestExecuteRespectsWidth(t *testing.T) {
	builder := NewBuilder("wrap")
	secret := builder.Secret("secret", 8)
	one := builder.Const("one", 8, 1)
	builder.Add("sum", secret, one)
	program := builder.Build()

	observation, err := Execute(program, nil, map[string]uint64{"secret": 0xff})
	if err != nil {
		t.Fatal(err)
	}
	if got := observation.Values["sum"]; got != 0 {
		t.Fatalf("8-bit add result = %#x, want 0", got)
	}
}
