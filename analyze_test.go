package netsec

import (
	"strings"
	"testing"
)

func mustAnalyze(t *testing.T, program *Program, libraries Libraries) *Report {
	t.Helper()
	report, err := Analyze(program, libraries)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	return report
}

func findingCount(report *Report, kind Kind) int {
	count := 0
	for _, finding := range report.Findings {
		if finding.Kind == kind {
			count++
		}
	}
	return count
}

func TestMaskedSecretAddressStillLeaks(t *testing.T) {
	builder := NewBuilder("masked-address")
	secret := builder.Secret("secret", 8)
	mask := builder.Const("mask", 8, 0x0f)
	masked := builder.And("masked", secret, mask)
	declassified := builder.PublicMask("declared-public", masked, 0x0f)
	builder.Lookup("table", declassified, 8)
	program := builder.Build()

	report := mustAnalyze(t, program, nil)
	if got, want := len(report.Findings), 4; got != want {
		t.Fatalf("findings = %d, want %d\n%s", got, want, findingsText(report.Findings))
	}
	if got := findingCount(report, KindSecretAddress); got != 4 {
		t.Fatalf("secret-address findings = %d, want 4", got)
	}
	for i, finding := range report.Findings {
		if finding.Sink != "table" {
			t.Fatalf("finding %d sink = %q, want table", i, finding.Sink)
		}
		if finding.Bit != i {
			t.Fatalf("finding %d bit = %d, want %d", i, finding.Bit, i)
		}
		if finding.Source.Input != "secret" || finding.Source.Bit != i {
			t.Fatalf("finding %d source = %+v, want secret bit %d", i, finding.Source, i)
		}
	}
	if path := report.Findings[0].String(); !strings.Contains(path, "and(masked)") ||
		!strings.Contains(path, "public-mask(declared-public)") ||
		!strings.Contains(path, "address(table)") {
		t.Fatalf("path does not expose the bit-level chain: %s", path)
	}

	deps, err := report.BitDependencies("declared-public")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(deps), 4; got != want {
		t.Fatalf("declared-public dependencies = %d, want %d", got, want)
	}
	for _, dep := range deps {
		if !dep.Public {
			t.Fatalf("bit %d dependency is not declassified for timing: %+v", dep.Bit, dep)
		}
	}

	for bit := 0; bit < 4; bit++ {
		diff, err := Differential(program, nil, map[string]uint64{"secret": 0}, "secret", bit)
		if err != nil {
			t.Fatal(err)
		}
		if !diff.Leaked {
			t.Fatalf("differential execution bit %d did not change the table address", bit)
		}
	}
	for bit := 4; bit < 8; bit++ {
		diff, err := Differential(program, nil, map[string]uint64{"secret": 0}, "secret", bit)
		if err != nil {
			t.Fatal(err)
		}
		if diff.Leaked {
			t.Fatalf("masked-out bit %d changed the table address", bit)
		}
	}
}

func TestPublicIndexIsConstantTime(t *testing.T) {
	builder := NewBuilder("public-index")
	index := builder.Public("index", 8)
	builder.Lookup("table", index, 8)
	program := builder.Build()

	report := mustAnalyze(t, program, nil)
	if len(report.Findings) != 0 {
		t.Fatalf("public index produced findings: %s", findingsText(report.Findings))
	}
}

func timingLibrary() Libraries {
	return Libraries{
		"ctcmp": {
			Name: "ctcmp",
			Params: []ParamSummary{{
				TimingBits: Bits(0, 1, 2, 3, 4, 5, 6, 7),
			}},
			ResultWidth: 1,
		},
	}
}

func TestCallTimingContract(t *testing.T) {
	builder := NewBuilder("raw-secret-call")
	secret := builder.Secret("secret", 8)
	builder.Call("compare", CallSpec{Library: "ctcmp"}, secret)
	program := builder.Build()

	report := mustAnalyze(t, program, timingLibrary())
	if got, want := len(report.Findings), 8; got != want {
		t.Fatalf("findings = %d, want %d\n%s", got, want, findingsText(report.Findings))
	}
	for i, finding := range report.Findings {
		if finding.Kind != KindTimingContract {
			t.Fatalf("finding %d kind = %s", i, finding.Kind)
		}
		if finding.Param != 0 || finding.Bit != i || finding.Source.Bit != i {
			t.Fatalf("finding %d = param %d bit %d source %+v", i, finding.Param, finding.Bit, finding.Source)
		}
	}
}

func TestCallAllowedPublicMasksTimingOnly(t *testing.T) {
	builder := NewBuilder("masked-call")
	secret := builder.Secret("secret", 8)
	mask := builder.Const("low-nibble", 8, 0x0f)
	masked := builder.And("masked", secret, mask)
	builder.Call("compare", CallSpec{
		Library:       "ctcmp",
		AllowedPublic: []Mask{0x0f},
	}, masked)
	program := builder.Build()

	report := mustAnalyze(t, program, timingLibrary())
	if len(report.Findings) != 0 {
		t.Fatalf("allowed-public low nibble produced findings: %s", findingsText(report.Findings))
	}

	builder = NewBuilder("uncontracted-masked-call")
	secret = builder.Secret("secret", 8)
	mask = builder.Const("low-nibble", 8, 0x0f)
	masked = builder.And("masked", secret, mask)
	builder.Call("compare", CallSpec{Library: "ctcmp"}, masked)
	program = builder.Build()

	report = mustAnalyze(t, program, timingLibrary())
	if got, want := len(report.Findings), 4; got != want {
		t.Fatalf("findings = %d, want %d\n%s", got, want, findingsText(report.Findings))
	}
}

func TestCallSiteCanDeclareTimingBits(t *testing.T) {
	builder := NewBuilder("site-timing")
	secret := builder.Secret("secret", 8)
	builder.Call("compare", CallSpec{
		Library:    "plain",
		TimingBits: []Mask{0x0f},
	}, secret)
	program := builder.Build()

	libraries := Libraries{
		"plain": {
			Name:   "plain",
			Params: []ParamSummary{{}},
		},
	}
	report := mustAnalyze(t, program, libraries)
	if got, want := len(report.Findings), 4; got != want {
		t.Fatalf("findings = %d, want %d\n%s", got, want, findingsText(report.Findings))
	}
}

func addressLibrary() Libraries {
	return Libraries{
		"ctread": {
			Name: "ctread",
			Params: []ParamSummary{{
				TimingBits:  0x0f,
				AddressBits: 0x0f,
			}},
			ResultWidth: 8,
		},
	}
}

func TestPublicMaskCannotEraseCallAddress(t *testing.T) {
	builder := NewBuilder("address-declassification")
	secret := builder.Secret("secret", 8)
	mask := builder.Const("low-nibble", 8, 0x0f)
	masked := builder.And("masked", secret, mask)
	builder.Call("read", CallSpec{
		Library:       "ctread",
		AllowedPublic: []Mask{0x0f},
	}, masked)
	program := builder.Build()

	report := mustAnalyze(t, program, addressLibrary())
	if got := findingCount(report, KindSecretAddress); got != 4 {
		t.Fatalf("secret-address findings = %d, want 4\n%s", got, findingsText(report.Findings))
	}
	if got := findingCount(report, KindTimingContract); got != 0 {
		t.Fatalf("address bits should not be double-reported as timing findings")
	}
	for _, finding := range report.Findings {
		if finding.Library != "ctread" || finding.Param != 0 {
			t.Fatalf("unexpected finding: %s", finding)
		}
	}

	builder = NewBuilder("address-explicitly-allowed")
	secret = builder.Secret("secret", 8)
	mask = builder.Const("low-nibble", 8, 0x0f)
	masked = builder.And("masked", secret, mask)
	builder.Call("read", CallSpec{
		Library:            "ctread",
		AllowedPublic:      []Mask{0x0f},
		AllowPublicAddress: true,
	}, masked)
	program = builder.Build()

	report = mustAnalyze(t, program, addressLibrary())
	if len(report.Findings) != 0 {
		t.Fatalf("explicitly allowed address bits produced findings: %s", findingsText(report.Findings))
	}
}

func TestLookupAddressContractOptIn(t *testing.T) {
	builder := NewBuilder("lookup-address-contract")
	secret := builder.Secret("secret", 8)
	mask := builder.Const("mask", 8, 0x0f)
	index := builder.And("index", secret, mask)
	builder.Lookup("table", index, 8)
	builder.LookupContract("table", 0x0f, false)
	program := builder.Build()

	report := mustAnalyze(t, program, nil)
	if got := findingCount(report, KindSecretAddress); got != 4 {
		t.Fatalf("strict lookup findings = %d, want 4", got)
	}

	builder = NewBuilder("lookup-address-contract-opt-in")
	secret = builder.Secret("secret", 8)
	mask = builder.Const("mask", 8, 0x0f)
	index = builder.And("index", secret, mask)
	builder.Lookup("table", index, 8)
	builder.LookupContract("table", 0x0f, true)
	program = builder.Build()

	report = mustAnalyze(t, program, nil)
	if len(report.Findings) != 0 {
		t.Fatalf("explicit lookup address contract produced findings: %s", findingsText(report.Findings))
	}
}

func TestSelectPropagatesConditionAndAddress(t *testing.T) {
	builder := NewBuilder("secret-select")
	condition := builder.Secret("condition", 1)
	left := builder.Public("left", 8)
	right := builder.Public("right", 8)
	selected := builder.Select("selected", condition, left, right)
	builder.Lookup("table", selected, 8)
	program := builder.Build()

	report := mustAnalyze(t, program, nil)
	if got, want := len(report.Findings), 8; got != want {
		t.Fatalf("findings = %d, want %d\n%s", got, want, findingsText(report.Findings))
	}
	for _, finding := range report.Findings {
		if finding.Source.Input != "condition" || finding.Source.Bit != 0 {
			t.Fatalf("unexpected source: %s", finding)
		}
	}
	diff, err := Differential(program, nil, map[string]uint64{
		"condition": 0,
		"left":      0x12,
		"right":     0x34,
	}, "condition", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.Leaked {
		t.Fatal("condition flip did not change the selected table address")
	}

	builder = NewBuilder("same-select")
	condition = builder.Secret("condition", 1)
	value := builder.Public("value", 8)
	builder.Select("selected", condition, value, value)
	builder.Lookup("table", value, 8)
	program = builder.Build()

	report = mustAnalyze(t, program, nil)
	if len(report.Findings) != 0 {
		t.Fatalf("selecting the same value produced findings: %s", findingsText(report.Findings))
	}
}

func TestArithmeticPropagationIsBitLevel(t *testing.T) {
	builder := NewBuilder("add-address")
	secret := builder.Secret("secret", 8)
	public := builder.Public("public", 8)
	sum := builder.Add("sum", secret, public)
	one := builder.Const("one", 8, 1)
	low := builder.And("low", sum, one)
	builder.Lookup("table", low, 8)
	program := builder.Build()

	report := mustAnalyze(t, program, nil)
	if got, want := len(report.Findings), 1; got != want {
		t.Fatalf("findings = %d, want %d\n%s", got, want, findingsText(report.Findings))
	}
	if report.Findings[0].Source.Bit != 0 || report.Findings[0].Bit != 0 {
		t.Fatalf("unexpected arithmetic path: %s", report.Findings[0])
	}
	if path := report.Findings[0].String(); !strings.Contains(path, "add(sum)") {
		t.Fatalf("path does not contain add step: %s", path)
	}

	diff, err := Differential(program, nil, map[string]uint64{"secret": 0, "public": 0}, "secret", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.Leaked {
		t.Fatal("LSB flip did not change the low-bit address")
	}
	diff, err = Differential(program, nil, map[string]uint64{"secret": 0, "public": 0}, "secret", 1)
	if err != nil {
		t.Fatal(err)
	}
	if diff.Leaked {
		t.Fatal("bit 1 should not affect bit 0 of the address")
	}
}

func TestShiftPropagationIsBitLevel(t *testing.T) {
	builder := NewBuilder("shift-address")
	secret := builder.Secret("secret", 8)
	shifted := builder.Shr("shifted", secret, 4)
	mask := builder.Const("mask", 8, 0x0f)
	index := builder.And("index", shifted, mask)
	builder.Lookup("table", index, 8)
	program := builder.Build()

	report := mustAnalyze(t, program, nil)
	if got, want := len(report.Findings), 4; got != want {
		t.Fatalf("findings = %d, want %d\n%s", got, want, findingsText(report.Findings))
	}
	for i, finding := range report.Findings {
		if finding.Bit != i || finding.Source.Bit != i+4 {
			t.Fatalf("finding %d = address bit %d source bit %d, want %d/%d",
				i, finding.Bit, finding.Source.Bit, i, i+4)
		}
		if path := finding.String(); !strings.Contains(path, "shr(shifted)") {
			t.Fatalf("shift path missing: %s", path)
		}
	}

	diff, err := Differential(program, nil, map[string]uint64{"secret": 0}, "secret", 4)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.Leaked {
		t.Fatal("source bit 4 did not reach address bit 0")
	}
	diff, err = Differential(program, nil, map[string]uint64{"secret": 0}, "secret", 3)
	if err != nil {
		t.Fatal(err)
	}
	if diff.Leaked {
		t.Fatal("source bit 3 should be shifted out")
	}
}

func TestLookupContentsPropagate(t *testing.T) {
	builder := NewBuilder("lookup-contents")
	table := builder.Secret("table", 8)
	index := builder.Public("index", 8)
	loaded := builder.LookupTable("loaded", table, index, 8)
	builder.Lookup("next", loaded, 8)
	program := builder.Build()

	report := mustAnalyze(t, program, nil)
	if got := findingCount(report, KindSecretAddress); got != 64 {
		t.Fatalf("secret-address findings = %d, want 64\n%s", got, findingsText(report.Findings))
	}
}

func TestIdenticalXorIsPublic(t *testing.T) {
	builder := NewBuilder("xor-self")
	secret := builder.Secret("secret", 8)
	x := builder.Xor("x", secret, secret)
	builder.Lookup("table", x, 8)
	program := builder.Build()

	report := mustAnalyze(t, program, nil)
	if len(report.Findings) != 0 {
		t.Fatalf("secret XOR itself produced findings: %s", findingsText(report.Findings))
	}
}

func TestDeclassifiedBranchAndLookup(t *testing.T) {
	builder := NewBuilder("declassified-branch")
	secret := builder.Secret("secret", 8)
	mask := builder.Const("mask", 8, 0x0f)
	masked := builder.And("masked", secret, mask)
	declassified := builder.PublicMask("declassified", masked, 0x0f)
	builder.Branch("branch", declassified)
	builder.Lookup("table", declassified, 8)
	program := builder.Build()

	report := mustAnalyze(t, program, nil)
	if got := findingCount(report, KindSecretBranch); got != 0 {
		t.Fatalf("declassified branch findings = %d, want 0", got)
	}
	if got := findingCount(report, KindSecretAddress); got != 4 {
		t.Fatalf("declassified lookup findings = %d, want 4", got)
	}
}

func TestNestedCallResultWidth(t *testing.T) {
	libraries := Libraries{
		"wide": {
			Name:        "wide",
			Params:      []ParamSummary{{}},
			ResultWidth: 16,
		},
		"sink": {
			Name:        "sink",
			Params:      []ParamSummary{{TimingBits: 0xffff}},
			ResultWidth: 1,
		},
	}
	builder := NewBuilder("nested-width")
	secret := builder.Secret("secret", 8)
	inner := builder.Call("inner", CallSpec{Library: "wide"}, secret)
	builder.Call("outer", CallSpec{Library: "sink"}, inner)
	program := builder.Build()

	report := mustAnalyze(t, program, libraries)
	if got, want := len(report.Findings), 16*8; got != want {
		t.Fatalf("findings = %d, want %d\n%s", got, want, findingsText(report.Findings))
	}
	for _, finding := range report.Findings {
		if finding.Sink != "outer" || finding.Param != 0 || finding.Bit >= 16 {
			t.Fatalf("unexpected nested-call finding: %s", finding)
		}
	}
}

func TestValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		program *Program
		libs    Libraries
	}{
		{
			name: "width too large",
			program: &Program{Nodes: []Node{{
				Name: "secret", Op: OpSecretInput, Width: 65,
			}}},
		},
		{
			name: "bad argument index",
			program: &Program{Nodes: []Node{
				{Name: "secret", Op: OpSecretInput, Width: 8},
				{Name: "bad", Op: OpNot, Width: 8, Args: []int{2}},
			}},
		},
		{
			name: "duplicate name",
			program: &Program{Nodes: []Node{
				{Name: "x", Op: OpSecretInput, Width: 8},
				{Name: "x", Op: OpPublicInput, Width: 8},
			}},
		},
		{
			name: "unknown library",
			program: &Program{Nodes: []Node{
				{Name: "seret", Op: OpSecretInput, Width: 8},
				{Name: "call", Op: OpCall, Width: 8, Args: []int{0}, Call: &CallSpec{Library: "missing"}},
			}},
		},
		{
			name: "parameter count",
			program: &Program{Nodes: []Node{
				{Name: "secret", Op: OpSecretInput, Width: 8},
				{Name: "call", Op: OpCall, Width: 8, Args: []int{0}, Call: &CallSpec{Library: "one"}},
			}},
			libs: Libraries{"one": {Name: "one", Params: []ParamSummary{{}, {}}}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Analyze(test.program, test.libs); err == nil {
				t.Fatalf("expected validation error")
			}
		})
	}
}

func findingsText(findings []Finding) string {
	var text strings.Builder
	for i, finding := range findings {
		if i > 0 {
			text.WriteByte('\n')
		}
		text.WriteString(finding.String())
	}
	return text.String()
}
