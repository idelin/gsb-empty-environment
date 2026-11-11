package loudness

import (
	"fmt"
	"math"
	"testing"
)

// The tests synthesise signals to the exact specifications of the EBU
// conformance suites (EBU Tech 3341 for loudness, Tech 3342 for
// loudness range / true peak) and require agreement within 0.1 LU.

const tolerance = 0.1

func sine(freq, amp float64, n int, rate float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = amp * math.Sin(2*math.Pi*freq*float64(i)/rate)
	}
	return out
}

func seconds(rate, s float64) int { return int(math.Round(rate * s)) }

func concat(parts ...[]float64) []float64 {
	var n int
	for _, p := range parts {
		n += len(p)
	}
	out := make([]float64, 0, n)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func interleave(chans ...[]float64) []float64 {
	n := len(chans[0])
	out := make([]float64, n*len(chans))
	for c, ch := range chans {
		for i, v := range ch {
			out[i*len(chans)+c] = v
		}
	}
	return out
}

func checkLU(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.IsInf(want, -1) {
		if !math.IsInf(got, -1) {
			t.Errorf("%s: got %.3f LUFS, want -Inf", name, got)
		}
		return
	}
	if d := math.Abs(got - want); d > tolerance {
		t.Errorf("%s: got %.3f, want %.3f (err %.3f LU > %.1f)", name, got, want, d, tolerance)
	} else {
		t.Logf("%s: got %.3f, want %.3f (err %.4f LU)", name, got, want, d)
	}
}

// feed runs the signal through a fresh meter in small chunks to
// exercise the streaming path, and returns the meter.
func feed(t *testing.T, rate float64, roles []ChannelRole, inter []float64) *Meter {
	t.Helper()
	m, err := NewMeter(rate, roles)
	if err != nil {
		t.Fatal(err)
	}
	const chunkFrames = 512
	nch := len(roles)
	for off := 0; off < len(inter); {
		n := chunkFrames * nch
		if off+n > len(inter) {
			n = len(inter) - off
		}
		if err := m.Write(inter[off : off+n]); err != nil {
			t.Fatal(err)
		}
		off += n
	}
	return m
}

func stereoRoles() []ChannelRole { return []ChannelRole{RoleOther, RoleOther} }

// --- EBU Tech 3341: K-weighting and absolute level --------------------

// EBU Tech 3341 case: 1 kHz sine at -20 dBFS, mono -> -23.0 LUFS.
func TestEBUMonoSine(t *testing.T) {
	const rate = 48000
	sig := sine(1000, 0.1, seconds(rate, 20), rate)
	m := feed(t, rate, []ChannelRole{RoleOther}, sig)
	checkLU(t, "mono 1kHz -20dBFS integrated", m.Integrated(), -23.0)
}

// EBU Tech 3341 case: 1 kHz sine at -20 dBFS, stereo (both channels)
// -> -20.0 LUFS.
func TestEBUStereoSine(t *testing.T) {
	const rate = 48000
	sig := sine(1000, 0.1, seconds(rate, 20), rate)
	m := feed(t, rate, stereoRoles(), interleave(sig, sig))
	checkLU(t, "stereo 1kHz -20dBFS integrated", m.Integrated(), -20.0)
}

// The K-weighting coefficients must be recomputed for every sample
// rate; the same signal must measure the same loudness everywhere.
func TestSampleRateIndependence(t *testing.T) {
	for _, rate := range []float64{32000, 44100, 48000, 88200, 96000} {
		sig := sine(1000, 0.1, seconds(rate, 10), rate)
		m := feed(t, rate, stereoRoles(), interleave(sig, sig))
		checkLU(t, fmt.Sprintf("rate %.0f", rate), m.Integrated(), -20.0)
	}
}

// The 48 kHz coefficients must match the constants published in
// BS.1770 exactly.
func TestKFilterCoefficients48k(t *testing.T) {
	k := newKFilter(48000)
	const eps = 1e-10
	wantShelf := [5]float64{1.53512485958697, -2.69169618940638, 1.19839281085285,
		-1.69065929318241, 0.73248077421585}
	gotShelf := [5]float64{k.shelf.b0, k.shelf.b1, k.shelf.b2, k.shelf.a1, k.shelf.a2}
	for i := range wantShelf {
		if math.Abs(gotShelf[i]-wantShelf[i]) > eps {
			t.Errorf("shelf coeff %d: got %.14f, want %.14f", i, gotShelf[i], wantShelf[i])
		}
	}
	wantRLB := [5]float64{1, -2, 1, -1.99004745483398, 0.99007225036621}
	gotRLB := [5]float64{k.rlb.b0, k.rlb.b1, k.rlb.b2, k.rlb.a1, k.rlb.a2}
	for i := range wantRLB {
		if math.Abs(gotRLB[i]-wantRLB[i]) > eps {
			t.Errorf("rlb coeff %d: got %.14f, want %.14f", i, gotRLB[i], wantRLB[i])
		}
	}
}

// energyOf converts a loudness in LUFS back to a BS.1770 energy.
func energyOf(lufs float64) float64 { return math.Pow(10, (lufs+0.691)/10) }

// --- EBU Tech 3341: gating --------------------------------------------

// Relative gate: a loud segment mixed with a segment more than 10 LU
// quieter must be measured on the loud segment alone (EBU Tech 3341
// gating case). The expected value is derived from the loud segment
// measured on its own.
func TestRelativeGate(t *testing.T) {
	const rate = 48000
	loud := sine(1000, 0.1, seconds(rate, 10), rate)       // -20 dBFS
	quiet := sine(1000, 0.005623, seconds(rate, 10), rate) // -45 dBFS

	ref := feed(t, rate, stereoRoles(), interleave(loud, loud))
	want := ref.Integrated() // quiet part is ~25 LU below, gated out

	both := concat(loud, quiet)
	m := feed(t, rate, stereoRoles(), interleave(both, both))
	checkLU(t, "relative gate integrated", m.Integrated(), want)

	// Sanity: without gating the result would be ~3 LU lower.
	ungated := loudnessOfEnergy((energyOf(want) + energyOf(want-25)) / 2)
	if m.Integrated() < ungated+1 {
		t.Errorf("gating had no effect: got %.3f, ungated would be %.3f",
			m.Integrated(), ungated)
	}
}

// Both segments pass the relative gate when they are 5 LU apart; the
// integrated loudness is the energy average of the two.
func TestRelativeGatePasses(t *testing.T) {
	const rate = 48000
	a := sine(1000, 0.1, seconds(rate, 10), rate)      // -20 dBFS
	b := sine(1000, 0.056234, seconds(rate, 10), rate) // -25 dBFS
	la := feed(t, rate, stereoRoles(), interleave(a, a)).Integrated()
	lb := feed(t, rate, stereoRoles(), interleave(b, b)).Integrated()
	want := loudnessOfEnergy((energyOf(la) + energyOf(lb)) / 2)

	both := concat(a, b)
	m := feed(t, rate, stereoRoles(), interleave(both, both))
	checkLU(t, "two-level integrated", m.Integrated(), want)
}

// Absolute gate: content below -70 LUFS never contributes, and a
// programme that is entirely below the gate reports silence.
func TestAbsoluteGate(t *testing.T) {
	const rate = 48000
	loud := sine(1000, 0.1, seconds(rate, 20), rate)
	tiny := sine(1000, 0.00003, seconds(rate, 5), rate) // ~-70.5 LUFS stereo

	want := feed(t, rate, stereoRoles(), interleave(loud, loud)).Integrated()
	both := concat(loud, tiny)
	m := feed(t, rate, stereoRoles(), interleave(both, both))
	checkLU(t, "absolute gate integrated", m.Integrated(), want)

	silent := feed(t, rate, stereoRoles(), interleave(tiny, tiny))
	if got := silent.Integrated(); !math.IsInf(got, -1) {
		t.Errorf("all-quiet programme: got %.3f LUFS, want -Inf", got)
	}
}

// --- Momentary / short-term --------------------------------------------

// For a stationary signal, momentary (400 ms) and short-term (3 s)
// loudness converge to the integrated value (EBU Tech 3341
// momentary/short-term cases use a constant 1 kHz tone).
func TestMomentaryAndShortTerm(t *testing.T) {
	const rate = 48000
	sig := sine(1000, 0.1, seconds(rate, 10), rate)
	m := feed(t, rate, stereoRoles(), interleave(sig, sig))
	i := m.Integrated()
	checkLU(t, "momentary", m.Momentary(), i)
	checkLU(t, "short-term", m.ShortTerm(), i)
}

// Short-term loudness averages 3 s: after a level step it settles on
// the new level within 3 s.
func TestShortTermStep(t *testing.T) {
	const rate = 48000
	a := sine(1000, 0.1, seconds(rate, 5), rate)
	b := sine(1000, 0.031623, seconds(rate, 5), rate) // -30 dBFS
	want := feed(t, rate, stereoRoles(), interleave(b, b)).Integrated()

	both := concat(a, b)
	m := feed(t, rate, stereoRoles(), interleave(both, both))
	checkLU(t, "short-term after step", m.ShortTerm(), want)
}

// --- Multichannel weighting --------------------------------------------

// Surround channels carry a +1.5 dB weight (G = 1.41) per BS.1770.
func TestSurroundWeight(t *testing.T) {
	const rate = 48000
	sig := sine(1000, 0.1, seconds(rate, 5), rate)
	front := feed(t, rate, []ChannelRole{RoleOther}, sig).Integrated()
	surround := feed(t, rate, []ChannelRole{RoleSurround}, sig).Integrated()
	got := surround - front
	want := 10 * math.Log10(1.41) // 1.49 dB
	if d := math.Abs(got - want); d > tolerance {
		t.Errorf("surround weight: got %.3f dB, want %.3f dB", got, want)
	}
}

// The LFE channel is excluded from the loudness sum.
func TestLFEExcluded(t *testing.T) {
	const rate = 48000
	roles, err := StandardRoles(6)
	if err != nil {
		t.Fatal(err)
	}
	n := seconds(rate, 5)
	lr := sine(1000, 0.1, n, rate)
	silence := make([]float64, n)
	lfe := sine(80, 0.5, n, rate) // loud LFE must not change anything

	withLFE := feed(t, rate, roles, interleave(lr, lr, silence, lfe, silence, silence))
	without := feed(t, rate, roles, interleave(lr, lr, silence, silence, silence, silence))
	if d := math.Abs(withLFE.Integrated() - without.Integrated()); d > 0.01 {
		t.Errorf("LFE affected loudness by %.4f LU", d)
	}

	// LFE-only content gates to silence.
	only := feed(t, rate, roles, interleave(silence, silence, silence, lfe, silence, silence))
	if got := only.Integrated(); !math.IsInf(got, -1) {
		t.Errorf("LFE-only: got %.3f LUFS, want -Inf", got)
	}
}

// Full 5.1: all five main channels at -20 dBFS sum with the BS.1770
// weights to -15.3 LUFS (z = (3 + 2*1.41) * z_ch).
func Test51Sum(t *testing.T) {
	const rate = 48000
	roles, err := StandardRoles(6)
	if err != nil {
		t.Fatal(err)
	}
	n := seconds(rate, 10)
	sig := sine(1000, 0.1, n, rate)
	silence := make([]float64, n)
	m := feed(t, rate, roles, interleave(sig, sig, sig, silence, sig, sig))
	mono := feed(t, rate, []ChannelRole{RoleOther}, sig).Integrated()
	want := loudnessOfEnergy((3 + 2*1.41) * energyOf(mono))
	checkLU(t, "5.1 sum", m.Integrated(), want)
}

// --- EBU Tech 3342: true peak ------------------------------------------

// A sine at fs/4 sampled at 45 degrees hits only +/-0.707 of its
// amplitude on the samples; the 4x oversampled true peak must recover
// the full amplitude (EBU Tech 3342 true-peak case).
func TestTruePeakInterSample(t *testing.T) {
	const rate = 48000
	n := seconds(rate, 2)
	sig := make([]float64, n)
	for i := range sig {
		sig[i] = 0.5 * math.Sin(2*math.Pi*float64(i)/4+math.Pi/4)
	}
	m := feed(t, rate, []ChannelRole{RoleOther}, sig)
	got := m.MaxTruePeak()
	want := 20 * math.Log10(0.5) // -6.02 dBTP
	if d := math.Abs(got - want); d > tolerance {
		t.Errorf("inter-sample true peak: got %.3f dBTP, want %.3f (sample peak is %.2f dBFS)",
			got, want, 20*math.Log10(0.5/math.Sqrt2))
	}
}

// For a low-frequency tone the true peak equals the sample peak.
func TestTruePeakSine(t *testing.T) {
	const rate = 48000
	sig := sine(1000, 0.25, seconds(rate, 2), rate)
	m := feed(t, rate, []ChannelRole{RoleOther}, sig)
	got := m.MaxTruePeak()
	want := 20 * math.Log10(0.25)
	if d := math.Abs(got - want); d > tolerance {
		t.Errorf("true peak: got %.3f dBTP, want %.3f", got, want)
	}
}
