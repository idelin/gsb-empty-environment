package taskpkg

import (
	"math/rand"
	"testing"
)

func TestMatrixConvolution(t *testing.T) {
	rng := rand.New(rand.NewSource(21))
	const block = 32
	const n = 500 // not a multiple of the block size

	// 2 inputs, 3 outputs; IR lengths differ per pair, some pairs empty.
	irs := [3][2][]float64{
		{randSlice(rng, 90), nil},
		{randSlice(rng, 45), randSlice(rng, 120)},
		{nil, nil},
	}
	m := NewMatrix(2, 3, block)
	for j := 0; j < 3; j++ {
		for i := 0; i < 2; i++ {
			if irs[j][i] != nil {
				m.SetIR(j, i, toF32(irs[j][i]), 0)
			}
		}
	}

	sigs := [2][]float64{randSlice(rng, n), randSlice(rng, n)}
	var got [3][]float64
	fed := 0
	for fed < n {
		step := 37
		if step > n-fed {
			step = n - fed
		}
		outs := m.Process([][]float32{
			toF32(sigs[0][fed : fed+step]),
			toF32(sigs[1][fed : fed+step]),
		})
		for j := 0; j < 3; j++ {
			got[j] = append(got[j], toF64(outs[j])...)
		}
		fed += step
	}
	flushed := m.Flush()
	for j := 0; j < 3; j++ {
		got[j] = append(got[j], toF64(flushed[j])...)
	}

	for j := 0; j < 3; j++ {
		var want []float64
		maxIR := 0
		for i := 0; i < 2; i++ {
			if len(irs[j][i]) > maxIR {
				maxIR = len(irs[j][i])
			}
			conv := directConv(sigs[i], irs[j][i])
			if len(conv) > len(want) {
				want = append(want, make([]float64, len(conv)-len(want))...)
			}
			for k, v := range conv {
				want[k] += v
			}
		}
		// Exact output length: input samples plus the longest pair tail.
		wantLen := n
		if maxIR > 0 {
			wantLen += maxIR - 1
		}
		if len(want) < wantLen {
			want = append(want, make([]float64, wantLen-len(want))...)
		}
		if len(got[j]) != wantLen {
			t.Fatalf("output %d: length = %d, want %d", j, len(got[j]), wantLen)
		}
		checkClose(t, got[j], want, "matrix output")
	}
}

func TestMatrixCrossfadeAlignment(t *testing.T) {
	rng := rand.New(rand.NewSource(33))
	const block = 16
	const fade = 48
	const boundary = 2 * block
	const n = 200

	ir1 := randSlice(rng, 60)
	ir2 := randSlice(rng, 30)
	m := NewMatrix(1, 1, block)
	m.SetIR(0, 0, toF32(ir1), 0)

	sig := randSlice(rng, n)
	var got []float64
	got = append(got, toF64(m.Process([][]float32{toF32(sig[:boundary])})[0])...)
	m.SetIR(0, 0, toF32(ir2), fade)
	got = append(got, toF64(m.Process([][]float32{toF32(sig[boundary:])})[0])...)
	got = append(got, toF64(m.Flush()[0])...)

	conv1 := directConv(sig, ir1)
	conv2 := directConv(sig, ir2)
	want := make([]float64, n+len(ir2)-1)
	for k := range want {
		var a, b float64
		if k < len(conv1) {
			a = conv1[k]
		}
		if k < len(conv2) {
			b = conv2[k]
		}
		switch {
		case k < boundary:
			want[k] = a
		case k < boundary+fade:
			g := float64(k-boundary) / float64(fade-1)
			want[k] = a*(1-g) + b*g
		default:
			want[k] = b
		}
	}
	checkClose(t, got, want, "matrix crossfade")
}

func TestMatrixChannelMismatchPanics(t *testing.T) {
	m := NewMatrix(2, 1, 16)
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on wrong input channel count")
		}
	}()
	m.Process([][]float32{make([]float32, 16)})
}
