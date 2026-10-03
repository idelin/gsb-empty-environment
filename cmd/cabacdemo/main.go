// cabacdemo encodes a structured instruction stream with the CABAC kernel
// and contrasts it with a context-free static model and raw 3-bit opcodes.
package main

import (
	"fmt"

	"example.com/cabac/instr"
)

func main() {
	const n = 50000
	prog := instr.Generate(n, 42)

	var hist [instr.NumOpcodes]int
	for _, op := range prog {
		hist[op]++
	}

	res := instr.RunExperiment(prog, 2000)

	fmt.Printf("=== CABAC kernel demo: structured instruction stream ===\n\n")
	fmt.Printf("instructions: %d   bins after unary binarization: %d\n", res.NumInstr, res.NumBins)
	fmt.Printf("opcode histogram:")
	for op := 0; op < instr.NumOpcodes; op++ {
		fmt.Printf(" %s=%.1f%%", instr.Names[op], 100*float64(hist[op])/float64(n))
	}
	fmt.Printf("\n\n")

	fmt.Printf("round-trip (CABAC adaptive):  %v\n", ok(res.RoundTripOK))
	fmt.Printf("round-trip (static baseline): %v\n\n", ok(res.StaticOK))

	fmt.Printf("%-34s %10s %12s\n", "model", "bytes", "bits/instr")
	fmt.Printf("%-34s %10d %12.3f\n", "raw fixed-length (3 bit/opcode)", res.RawBits/8, 3.0)
	fmt.Printf("%-34s %10d %12.3f\n", "static context-free (global hist)", res.StaticBytes, 8*float64(res.StaticBytes)/float64(res.NumInstr))
	fmt.Printf("%-34s %10d %12.3f\n", "CABAC (adaptive contexts)", res.CABACBytes, 8*float64(res.CABACBytes)/float64(res.NumInstr))
	fmt.Printf("\nCABAC vs static model: %.1fx smaller\n", float64(res.StaticBytes)/float64(res.CABACBytes))
	fmt.Printf("CABAC vs raw stream:   %.1fx smaller\n\n", float64(res.RawBits/8)/float64(res.CABACBytes))

	fmt.Printf("convergence of high-frequency instruction (NOP) cost:\n")
	fmt.Printf("%-14s %s\n", "NOP window", "bits/NOP")
	for i, b := range res.NOPBits {
		fmt.Printf("%-14d %.4f\n", (i+1)*2000, b)
	}
}

func ok(b bool) string {
	if b {
		return "OK"
	}
	return "FAILED"
}
