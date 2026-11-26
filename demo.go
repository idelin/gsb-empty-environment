package stlf

import "time"

// ExampleHost returns the host used by the bundled nested-STLF example.
//
// The fixed high six bits are 0x10 (addresses 0x400..0x43f), the secret lives
// at 0x405, and r0 receives the public six-bit address part.
func ExampleHost() Host {
	var h Host
	h.High6 = 0x10
	h.SecretSlot = 0x405
	h.PublicLowReg = 0
	h.InitialRegs[3] = 0x0f // public committed probe
	// r4 and the fillers stay zero; PublicMemory is zero.
	return h
}

// ExampleProgram is a 19-instruction nested-misprediction/partial-STLF attack.
//
// Branch 0 is predicted taken but is not taken for nonzero public-low values.
// The wrong path reaches branch 7, which is also predicted taken and is not
// taken.  The resulting depth-2 wrong path loads the secret, stores it to
// 0x40a, and loads 0x00a.  The two addresses have the same low six bits but
// different high six bits, so speculative forwarding returns the secret.
// probe(r2) then observes secret&0x0f before the four-cycle window closes.
//
// Architecture only executes the public probe at instruction 1 and then jumps
// to the barrier at 15, so its committed probe trace is identical for every
// secret.
func ExampleProgram() []Instruction {
	return []Instruction{
		CmpBranchImm(0, 0x00, 7, true),  // 0: predicted taken, actually not
		Probe(3),                        // 1: committed public probe
		CmpBranchReg(4, 4, 15, true),    // 2: committed jump to end
		XorImm(6, 0),                    // 3
		XorImm(6, 0),                    // 4
		XorImm(6, 0),                    // 5
		XorImm(6, 0),                    // 6
		CmpBranchImm(0, 0xff, 11, true), // 7: nested predicted-taken miss
		CmpBranchReg(4, 4, 15, true),    // 8: public-zero architectural path
		XorImm(6, 0),                    // 9
		XorImm(6, 0),                    // 10
		Load(1, 0x405),                  // 11: secret load (speculative)
		Store(0x40a, 1),                 // 12: uncommitted store
		Load(2, 0x00a),                  // 13: low6 alias, high6 differs
		Probe(2),                        // 14: leaks secret&0x0f
		Barrier(),                       // 15: drain speculation
		XorImm(6, 0),                    // 16
		XorImm(6, 0),                    // 17
		XorImm(6, 0),                    // 18
	}
}

// ExampleScenario bundles ExampleHost and ExampleProgram.
func ExampleScenario() Scenario {
	return Scenario{Host: ExampleHost(), Code: ExampleProgram()}
}

// DemoResult contains the bundled end-to-end result.
type DemoResult struct {
	Scenario Scenario
	Search   SearchResult
	Patch    Patch
	Report   PatchReport
}

// RunDemo searches the bundled example and verifies a minimal barrier patch.
func RunDemo(opts ...SearchOptions) DemoResult {
	sc := ExampleScenario()
	search := FindWitness(sc.Host, sc.Code, opts...)
	patch, report := FindMinimalBarrierPatch(sc.Host, sc.Code, PatchSearchOptions{
		VerifyOptions: VerifyOptions{
			PublicValues: nil,
			SecretValues: nil,
			Timeout:      30 * time.Second,
		},
	})
	return DemoResult{Scenario: sc, Search: search, Patch: patch, Report: report}
}
