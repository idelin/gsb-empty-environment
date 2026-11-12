package audioalign

import "math/rand"

// synthConfig describes a synthetic two-track recording.
type synthConfig struct {
	n             int
	driftStart    float64 // delay at sample 0, in samples
	driftSlope    float64 // delay increase per reference sample
	beatPeriod    int     // 0 means no periodic beats (colored noise only)
	beatAmp       float64
	noiseAmp      float64
	silencePeriod int  // 0 means no inserted silence
	silenceGap    int  // length of each silent block
	silenceBoth   bool // true: silence in both tracks; false: reference only
	subsample     bool // true: linear-interpolate fractional source positions
	seed          int64
}

// synthPCM builds a reference track and a delayed copy whose clock drifts
// linearly: source position n in the delayed track equals
// n - (driftStart + driftSlope*n). Silence blocks are inserted at the same
// wall-clock positions in both tracks so that windows simply skip them.
func synthPCM(sc synthConfig) (ref, delayed []float64) {
	rng := rand.New(rand.NewSource(sc.seed))
	ref = make([]float64, sc.n)

	var cur float64
	for n := 0; n < sc.n; n++ {
		v := sc.noiseAmp * (rng.Float64()*2 - 1)
		// Light low-pass filtering so the source is spectrally smooth.
		cur = 0.6*cur + 0.4*v
		ref[n] = cur
		if sc.beatPeriod > 0 && n%sc.beatPeriod == 0 {
			ref[n] += sc.beatAmp
		}
	}

	if sc.silencePeriod > 0 {
		for start := sc.silencePeriod; start < sc.n; start += sc.silencePeriod {
			end := start + sc.silenceGap
			if end > sc.n {
				end = sc.n
			}
			for n := start; n < end; n++ {
				ref[n] = 0
			}
		}
	}

	// Extend the delayed buffer so large positive lags stay in range.
	extra := int(sc.driftStart+sc.driftSlope*float64(sc.n)) + sc.beatPeriod + 8
	if extra < 0 {
		extra = 0
	}
	delayed = make([]float64, sc.n+extra)
	for n := 0; n < len(delayed); n++ {
		lag := sc.driftStart + sc.driftSlope*float64(n)
		src := float64(n) - lag
		delayed[n] = sampleAt(ref, src, sc.subsample)
	}

	if sc.silencePeriod > 0 && sc.silenceBoth {
		// Shift the silence positions by the local lag so the blocks line up.
		for start := sc.silencePeriod; start < sc.n; start += sc.silencePeriod {
			lag := sc.driftStart + sc.driftSlope*float64(start)
			dStart := int(mathRound(float64(start) + lag))
			dEnd := dStart + sc.silenceGap
			if dStart < 0 {
				dStart = 0
			}
			if dEnd > len(delayed) {
				dEnd = len(delayed)
			}
			for n := dStart; n < dEnd; n++ {
				delayed[n] = 0
			}
		}
	}
	return ref, delayed
}

func sampleAt(ref []float64, pos float64, interpolate bool) float64 {
	if !interpolate {
		idx := mathRound(pos)
		if idx < 0 || idx >= len(ref) {
			return 0
		}
		return ref[idx]
	}
	if pos < 0 || pos > float64(len(ref)-1) {
		return 0
	}
	lo := int(mathFloor(pos))
	frac := pos - float64(lo)
	if lo+1 >= len(ref) {
		return ref[lo]
	}
	return ref[lo]*(1-frac) + ref[lo+1]*frac
}

func mathRound(x float64) int {
	if x < 0 {
		return -int(-x + 0.5)
	}
	return int(x + 0.5)
}

func mathFloor(x float64) int {
	i := int(x)
	if x < float64(i) {
		i--
	}
	return i
}
