package loudness

import "math"

// truePeak oversampling factor required by BS.1770 / EBU R128.
const oversampleFactor = 4

// truePeak tracks the 4x oversampled true peak of one channel.
//
// The input is upsampled by 4 with a windowed-sinc interpolation filter
// (Kaiser window, 16 zero crossings per side, ~90 dB stop-band), which
// reveals inter-sample peaks above the sample-rate peak.
type truePeak struct {
	// Polyphase branches of the interpolation filter. Branch p
	// produces the output sample at phase p/4 between two inputs.
	branches [oversampleFactor][]float64
	hist     []float64 // delay line, len = max branch length
	pos      int
	peak     float64
}

// newTruePeak builds the 4x interpolation filter. The pass-band edge is
// 0.45*fs (0.9*Nyquist); as the filter is normalised to the oversampled
// rate, its response is sample-rate independent.
func newTruePeak(sampleRate float64) *truePeak {
	const (
		zeroCrossings = 16   // sinc zero crossings per side (input rate)
		beta          = 9.0  // Kaiser beta, ~90 dB stop-band
		passEdge      = 0.45 // fraction of input sample rate
	)
	n := oversampleFactor * zeroCrossings // taps per branch
	// Cutoff as a fraction of the *oversampled* rate: halfway between
	// the pass-band edge and the input Nyquist, divided by 4.
	fc := (passEdge + 0.5) / 2 / oversampleFactor

	t := &truePeak{hist: make([]float64, n)}
	full := make([]float64, oversampleFactor*n)
	center := float64(len(full)-1) / 2
	for i := range full {
		// Symmetric (Type II) FIR; sinc and Kaiser window share the
		// same centre so the pass-band stays flat.
		d := float64(i) - center
		var sinc float64
		if d == 0 {
			sinc = 2 * fc
		} else {
			sinc = math.Sin(2*math.Pi*fc*d) / (math.Pi * d)
		}
		r := d / center
		w := kaiser0(beta*math.Sqrt(max(0, 1-r*r))) / kaiser0(beta)
		full[i] = sinc * w
	}
	// Split into polyphase branches. The composite filter is scaled to
	// a DC gain of oversampleFactor (zero-stuffed upsampling attenuates
	// by that factor), so each branch has ~unity gain and the
	// oversampled level matches the input level.
	var total float64
	for _, c := range full {
		total += c
	}
	scale := oversampleFactor / total
	for p := 0; p < oversampleFactor; p++ {
		branch := make([]float64, n)
		for k := 0; k < n; k++ {
			branch[k] = full[k*oversampleFactor+p] * scale
		}
		t.branches[p] = branch
	}
	return t
}

// kaiser0 is the zero-order modified Bessel function of the first kind.
func kaiser0(x float64) float64 {
	sum, term := 1.0, 1.0
	for k := 1; k < 40; k++ {
		term *= (x * x) / (4 * float64(k*k))
		sum += term
		if term < 1e-16*sum {
			break
		}
	}
	return sum
}

// process feeds one input sample and updates the running peak with the
// four oversampled outputs.
func (t *truePeak) process(x float64) {
	t.pos--
	if t.pos < 0 {
		t.pos = len(t.hist) - 1
	}
	t.hist[t.pos] = x
	for p := 0; p < oversampleFactor; p++ {
		branch := t.branches[p]
		var y float64
		idx := t.pos
		for k := 0; k < len(branch); k++ {
			y += branch[k] * t.hist[idx]
			idx++
			if idx == len(t.hist) {
				idx = 0
			}
		}
		if a := math.Abs(y); a > t.peak {
			t.peak = a
		}
	}
}

// dBTP returns the true peak in dBTP, or -Inf if no samples were seen.
func (t *truePeak) dBTP() float64 {
	if t.peak <= 0 {
		return math.Inf(-1)
	}
	return 20 * math.Log10(t.peak)
}

func (t *truePeak) reset() {
	for i := range t.hist {
		t.hist[i] = 0
	}
	t.pos = 0
	t.peak = 0
}
