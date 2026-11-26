// Command stlfcheck is a thin command-line front end for the stlf package.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	stlf "formal.local/w3b-984d40600f"
)

func main() {
	var (
		mode        = flag.String("mode", "demo", "demo|analyze|verify|minimal")
		code        = flag.String("code", "", "inline assembly program")
		file        = flag.String("file", "", "read assembly program from file")
		high6       = flag.Uint("high6", 0x10, "fixed high six address bits")
		secretSlot  = flag.Uint("secret-slot", 0x405, "12-bit secret slot")
		publicReg   = flag.Int("public-reg", 0, "register receiving public low6 (0..15)")
		secretReg   = flag.Int("secret-reg", -1, "optional register receiving secret")
		publics     = flag.String("public", "", "comma-separated public low6 values")
		secrets     = flag.String("secrets", "", "comma-separated secret values")
		positions   = flag.String("positions", "", "comma-separated barrier sites for verify")
		timeout     = flag.Duration("timeout", 30*time.Second, "search/verification timeout")
		maxSteps    = flag.Int("max-steps", 0, "dynamic instruction bound")
		partialOnly = flag.Bool("partial-only", false, "require a witness that exercised partial STLF")
		metric      = flag.String("metric", "steps", "witness cost: steps|probes|divergence")
		jsonOut     = flag.Bool("json", false, "emit JSON")
	)
	flag.Parse()

	pubVals, err := parseValues(*publics, 0x3f)
	if err != nil {
		fail(err)
	}
	secVals, err := parseValues(*secrets, 0xff)
	if err != nil {
		fail(err)
	}
	posVals, err := parseInts(*positions)
	if err != nil {
		fail(err)
	}
	searchOpts := stlf.SearchOptions{
		PublicValues:          pubVals,
		SecretValues:          secVals,
		Timeout:               *timeout,
		MaxSteps:              *maxSteps,
		RequirePartialForward: *partialOnly,
		Metric:                parseMetric(*metric),
	}
	verifyOpts := stlf.VerifyOptions{
		PublicValues: pubVals,
		SecretValues: secVals,
		Timeout:      *timeout,
		MaxSteps:     *maxSteps,
	}

	if *mode == "demo" {
		demo := stlf.RunDemo(searchOpts)
		if *jsonOut {
			emit(demoView(demo))
		} else {
			printSearch(demo.Search)
			fmt.Printf("minimal patch: %s\n%s\n", demo.Patch, demo.Report.Message)
		}
		return
	}

	src := *code
	if *file != "" {
		data, err := os.ReadFile(*file)
		if err != nil {
			fail(err)
		}
		src = string(data)
	}
	program, err := stlf.ParseProgram(src)
	if err != nil {
		fail(err)
	}
	host := stlf.Host{
		High6:        uint8(*high6 & 0x3f),
		SecretSlot:   uint16(*secretSlot & 0x0fff),
		PublicLowReg: uint8(*publicReg & 0x0f),
	}
	if *secretReg >= 0 {
		host.HasSecretReg = true
		host.SecretReg = uint8(*secretReg & 0x0f)
	}
	verifier, err := stlf.NewVerifier(host, program)
	if err != nil {
		fail(err)
	}
	verifier.SearchOptions = searchOpts
	verifier.VerifyOptions = verifyOpts

	out := output{Mode: *mode}
	switch *mode {
	case "analyze":
		res := verifier.FindWitness(searchOpts)
		out.Status = string(res.Status)
		out.Message = res.Message
		out.NativeCount = res.NativeCount
		out.CheckedInputs = res.CheckedInputs
		out.CheckedPairs = res.CheckedPairs
		out.ElapsedMS = res.Elapsed.Milliseconds()
		out.Witness = witnessViewOf(res.Witness)
		if *jsonOut {
			emit(out)
		} else {
			printSearch(res)
		}
	case "verify":
		if len(posVals) == 0 {
			fail(fmt.Errorf("verify mode requires -positions, use minimal mode to search"))
		}
		report := verifier.VerifyPatch(stlf.NewPatch(posVals...), verifyOpts)
		out.Patch = posVals
		out.PatchReport = patchViewOf(report)
		out.Status = statusOfPatch(report)
		out.Message = report.Message
		out.ElapsedMS = report.Elapsed.Milliseconds()
		if *jsonOut {
			emit(out)
		} else {
			printPatch(report)
		}
	case "minimal":
		patch, report := verifier.MinimalPatch(stlf.PatchSearchOptions{VerifyOptions: verifyOpts})
		out.Patch = patch.Positions
		out.PatchReport = patchViewOf(report)
		out.Status = statusOfPatch(report)
		out.Message = report.Message
		out.ElapsedMS = report.Elapsed.Milliseconds()
		if *jsonOut {
			emit(out)
		} else {
			printPatch(report)
		}
	default:
		fail(fmt.Errorf("unknown mode %q", *mode))
	}
}

func parseMetric(s string) stlf.CostMetric {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "steps", "step":
		return stlf.CostSteps
	case "probes", "probe", "observations":
		return stlf.CostSpecProbes
	case "divergence", "first", "early":
		return stlf.CostFirstDivergence
	default:
		fail(fmt.Errorf("unknown metric %q", s))
		return stlf.CostSteps
	}
}

type output struct {
	Mode          string       `json:"mode"`
	Status        string       `json:"status,omitempty"`
	Message       string       `json:"message,omitempty"`
	Witness       *witnessView `json:"witness,omitempty"`
	Patch         []int        `json:"patch,omitempty"`
	PatchReport   *patchView   `json:"patch_report,omitempty"`
	NativeCount   int          `json:"native_count,omitempty"`
	CheckedInputs int          `json:"checked_inputs,omitempty"`
	CheckedPairs  int          `json:"checked_pairs,omitempty"`
	ElapsedMS     int64        `json:"elapsed_ms,omitempty"`
}

type witnessView struct {
	PublicLow6      uint8  `json:"public_low6"`
	SecretA         uint8  `json:"secret_a"`
	SecretB         uint8  `json:"secret_b"`
	ArchA           string `json:"arch_a"`
	ArchB           string `json:"arch_b"`
	SpecA           string `json:"spec_a"`
	SpecB           string `json:"spec_b"`
	SpecStepsA      int    `json:"spec_steps_a"`
	SpecStepsB      int    `json:"spec_steps_b"`
	FirstDivergence int    `json:"first_divergence"`
	Native          bool   `json:"native"`
	UsesPartialSTLF bool   `json:"uses_partial_stlf"`
}

type patchView struct {
	Positions          []int         `json:"positions"`
	Valid              bool          `json:"valid"`
	SemanticsPreserved bool          `json:"semantics_preserved"`
	LeakFree           bool          `json:"leak_free"`
	NativeLeakFree     bool          `json:"native_leak_free"`
	NativeVulnerable   bool          `json:"native_vulnerable"`
	SpecLeakCount      int           `json:"spec_leak_count"`
	NativeCount        int           `json:"native_count"`
	Remaining          []witnessView `json:"remaining,omitempty"`
	Message            string        `json:"message"`
	ElapsedMS          int64         `json:"elapsed_ms"`
	Unknown            bool          `json:"unknown"`
}

func witnessViewOf(w *stlf.Witness) *witnessView {
	if w == nil {
		return nil
	}
	return &witnessView{
		PublicLow6:      w.PublicLow6,
		SecretA:         w.SecretA,
		SecretB:         w.SecretB,
		ArchA:           w.A.ArchTrace.String(),
		ArchB:           w.B.ArchTrace.String(),
		SpecA:           w.A.SpecTrace.String(),
		SpecB:           w.B.SpecTrace.String(),
		SpecStepsA:      w.A.Steps,
		SpecStepsB:      w.B.Steps,
		FirstDivergence: w.Cost.FirstDivergence,
		Native:          w.Native,
		UsesPartialSTLF: w.UsesPartialForward,
	}
}

func patchViewOf(r stlf.PatchReport) *patchView {
	v := &patchView{
		Positions:          append([]int(nil), r.Patch.Positions...),
		Valid:              r.Valid,
		SemanticsPreserved: r.SemanticsPreserved,
		LeakFree:           r.LeakFree,
		NativeLeakFree:     r.NativeLeakFree,
		NativeVulnerable:   r.NativeVulnerable,
		SpecLeakCount:      r.SpecLeakCount,
		NativeCount:        r.NativeCount,
		Message:            r.Message,
		ElapsedMS:          r.Elapsed.Milliseconds(),
		Unknown:            r.Unknown,
	}
	for i := range r.Remaining {
		v.Remaining = append(v.Remaining, *witnessViewOf(&r.Remaining[i]))
	}
	return v
}

func statusOfPatch(r stlf.PatchReport) string {
	switch {
	case r.Err != nil:
		return "Error"
	case r.Unknown:
		return "Unknown"
	case r.Valid:
		return "Valid"
	case !r.SemanticsPreserved:
		return "SemanticsChanged"
	case r.NativeCount > 0:
		return "NativeLeak"
	case r.SpecLeakCount > 0:
		return "LeakRemains"
	default:
		return "Invalid"
	}
}

func printSearch(r stlf.SearchResult) {
	fmt.Printf("status: %s\n", r.Status)
	fmt.Printf("message: %s\n", r.Message)
	fmt.Printf("checked: %d inputs, %d pairs in %s\n", r.CheckedInputs, r.CheckedPairs, r.Elapsed.Round(time.Millisecond))
	if r.NativeCount > 0 {
		fmt.Printf("native architectural leak pairs: %d\n", r.NativeCount)
	}
	if w := r.Witness; w != nil {
		fmt.Printf("witness: public_low6=%#02x secret=%#02x/%#02x\n", w.PublicLow6, w.SecretA, w.SecretB)
		fmt.Printf("  arch A=%s B=%s (identical)\n", w.A.ArchTrace, w.B.ArchTrace)
		fmt.Printf("  spec A=%s B=%s\n", w.A.SpecTrace, w.B.SpecTrace)
		fmt.Printf("  steps A/B=%d/%d first divergence=%d\n", w.A.Steps, w.B.Steps, w.Cost.FirstDivergence)
	}
}

func printPatch(r stlf.PatchReport) {
	fmt.Printf("patch: %s\n", r.Patch)
	fmt.Printf("valid=%v semantics_preserved=%v leak_free=%v native_leak_free=%v\n",
		r.Valid, r.SemanticsPreserved, r.LeakFree, r.NativeLeakFree)
	fmt.Printf("spec_leak_pairs=%d native_pairs=%d\n", r.SpecLeakCount, r.NativeCount)
	if r.Message != "" {
		fmt.Printf("message: %s\n", r.Message)
	}
	if r.Elapsed > 0 {
		fmt.Printf("elapsed: %s\n", r.Elapsed.Round(time.Millisecond))
	}
}

func demoView(d stlf.DemoResult) output {
	return output{
		Mode:          "demo",
		Status:        string(d.Search.Status),
		Message:       d.Report.Message,
		Witness:       witnessViewOf(d.Search.Witness),
		Patch:         d.Patch.Positions,
		PatchReport:   patchViewOf(d.Report),
		NativeCount:   d.Search.NativeCount,
		CheckedInputs: d.Search.CheckedInputs,
		CheckedPairs:  d.Search.CheckedPairs,
		ElapsedMS:     d.Search.Elapsed.Milliseconds(),
	}
}

func parseValues(s string, max uint) ([]uint8, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var out []uint8
	for _, field := range strings.Split(s, ",") {
		n, err := strconv.ParseUint(strings.TrimSpace(field), 0, 16)
		if err != nil || n > uint64(max) {
			return nil, fmt.Errorf("invalid value %q", field)
		}
		out = append(out, uint8(n))
	}
	return out, nil
}

func parseInts(s string) ([]int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var out []int
	for _, field := range strings.Split(s, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(field))
		if err != nil || n < 0 {
			return nil, fmt.Errorf("invalid position %q", field)
		}
		out = append(out, n)
	}
	return out, nil
}

func emit(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "stlfcheck:", err)
	os.Exit(2)
}
