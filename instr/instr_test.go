package instr

import (
	"testing"
)

func TestGenerateStructured(t *testing.T) {
	prog := Generate(20000, 7)
	if len(prog) != 20000 {
		t.Fatalf("len = %d, want 20000", len(prog))
	}
	var nops int
	for _, op := range prog {
		if op >= NumOpcodes {
			t.Fatalf("invalid opcode %d", op)
		}
		if op == NOP {
			nops++
		}
	}
	// Structured stream must be NOP-dominated but not pure NOP.
	if frac := float64(nops) / float64(len(prog)); frac < 0.8 || frac > 0.99 {
		t.Fatalf("NOP fraction %.3f outside [0.8, 0.99]", frac)
	}
	// Deterministic for a fixed seed.
	again := Generate(20000, 7)
	for i := range prog {
		if prog[i] != again[i] {
			t.Fatalf("Generate not deterministic at %d", i)
		}
	}
}

func TestBinarizeShape(t *testing.T) {
	cases := []struct {
		v    uint8
		want []uint32
	}{
		{NOP, []uint32{0}},
		{MOV, []uint32{1, 0}},
		{ADD, []uint32{1, 1, 0}},
		{CALL, []uint32{1, 1, 1, 1, 1, 1, 1}},
	}
	for _, c := range cases {
		got := Binarize(c.v, nil)
		if len(got) != len(c.want) {
			t.Fatalf("Binarize(%d) len = %d, want %d", c.v, len(got), len(c.want))
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("Binarize(%d)[%d] = %d, want %d", c.v, i, got[i], c.want[i])
			}
		}
	}
}

func TestExperimentRoundTrip(t *testing.T) {
	prog := Generate(30000, 1234)
	res := RunExperiment(prog, 2000)
	if !res.RoundTripOK {
		t.Fatal("CABAC round trip failed")
	}
	if !res.StaticOK {
		t.Fatal("static model round trip failed")
	}
	if res.NumBins <= res.NumInstr {
		t.Fatalf("NumBins = %d, want > NumInstr = %d", res.NumBins, res.NumInstr)
	}
}

// Acceptance: the high-frequency instruction's effective code length
// converges below 0.2 bits as encoding progresses.
func TestNOPCostConverges(t *testing.T) {
	prog := Generate(50000, 99)
	res := RunExperiment(prog, 2000)
	if len(res.NOPBits) < 4 {
		t.Fatalf("too few windows: %d", len(res.NOPBits))
	}
	last := res.NOPBits[len(res.NOPBits)-1]
	t.Logf("bits/NOP per window: %.3f ... %.4f (last)", res.NOPBits[0], last)
	if last > 0.2 {
		t.Fatalf("converged NOP cost %.4f bits > 0.2 bits", last)
	}
	if last >= res.NOPBits[0] {
		t.Fatalf("no convergence: first %.4f, last %.4f", res.NOPBits[0], last)
	}
}

// Acceptance: CABAC must clearly beat the context-free static model.
func TestBeatsStaticModel(t *testing.T) {
	prog := Generate(50000, 2024)
	res := RunExperiment(prog, 2000)
	ratio := float64(res.StaticBytes) / float64(res.CABACBytes)
	t.Logf("cabac=%dB static=%dB raw=%dB ratio=%.2fx",
		res.CABACBytes, res.StaticBytes, res.RawBits/8, ratio)
	if res.CABACBytes >= res.StaticBytes {
		t.Fatalf("CABAC (%dB) not smaller than static model (%dB)",
			res.CABACBytes, res.StaticBytes)
	}
	if ratio < 1.5 {
		t.Fatalf("CABAC only %.2fx smaller than static model, want >= 1.5x", ratio)
	}
}
