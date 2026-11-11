// Package loudness implements a streaming loudness meter conforming to
// ITU-R BS.1770-5 and EBU R128, intended for pre-broadcast loudness
// checks in a Go audio processing pipeline.
//
// The meter applies K-weighting (with filter coefficients recomputed for
// any sample rate), analyses 400 ms blocks with 75% overlap, gates with
// the -70 LUFS absolute gate followed by the -10 LU relative gate, and
// reports integrated loudness, momentary/short-term loudness and the
// 4x oversampled true peak. Multichannel signals are summed with the
// BS.1770 channel weights; the LFE channel is excluded.
package loudness

import (
	"errors"
	"fmt"
	"math"
	"sync"
)

// ChannelRole identifies a channel so the correct BS.1770 weight can be
// applied. The LFE channel carries weight zero and is excluded from the
// loudness sum.
type ChannelRole int

const (
	// RoleOther covers left, right, centre and any unlabelled channel
	// (weight 1.0).
	RoleOther ChannelRole = iota
	// RoleSurround covers left/right surround and rear channels
	// (weight ~1.41, i.e. +1.5 dB).
	RoleSurround
	// RoleLFE is the low-frequency effects channel (weight 0, excluded).
	RoleLFE
)

// ErrNoChannels is returned when a meter is created without channels.
var ErrNoChannels = errors.New("loudness: at least one channel is required")

// ChannelWeight returns the BS.1770 linear weight for a channel role.
func ChannelWeight(r ChannelRole) float64 {
	switch r {
	case RoleSurround:
		return 1.41
	case RoleLFE:
		return 0
	default:
		return 1
	}
}

// StandardRoles returns the channel roles for common layouts: 1.0 mono,
// 2.0 stereo, 5.1 and 7.1 (SMPTE order: L R C LFE Ls Rs [Lrs Rrs]).
func StandardRoles(numChannels int) ([]ChannelRole, error) {
	switch numChannels {
	case 1:
		return []ChannelRole{RoleOther}, nil
	case 2:
		return []ChannelRole{RoleOther, RoleOther}, nil
	case 6:
		return []ChannelRole{RoleOther, RoleOther, RoleOther, RoleLFE, RoleSurround, RoleSurround}, nil
	case 8:
		return []ChannelRole{RoleOther, RoleOther, RoleOther, RoleLFE,
			RoleSurround, RoleSurround, RoleSurround, RoleSurround}, nil
	default:
		return nil, fmt.Errorf("loudness: no standard layout for %d channels", numChannels)
	}
}

// biquad is a second-order IIR section in direct-form II transposed.
type biquad struct {
	b0, b1, b2 float64
	a1, a2     float64
	z1, z2     float64
}

func (f *biquad) process(x float64) float64 {
	y := f.b0*x + f.z1
	f.z1 = f.b1*x - f.a1*y + f.z2
	f.z2 = f.b2*x - f.a2*y
	return y
}

func (f *biquad) reset() { f.z1, f.z2 = 0, 0 }

// kFilter is the BS.1770 K-weighting chain: pre-filter (shelf) followed
// by the RLB high-pass. Coefficients are recomputed for the actual
// sample rate using the bilinear transform of the analogue prototypes
// given in BS.1770.
type kFilter struct {
	shelf biquad
	rlb   biquad
}

func newKFilter(sampleRate float64) kFilter {
	var k kFilter

	// Pre-filter: high shelf, +4 dB above ~1.68 kHz.
	// Bilinear-transformed analogue prototype from BS.1770, computed for
	// the actual sample rate (matches the published 48 kHz coefficients
	// b = [1.53512485958697, -2.69169618940638, 1.19839281085285],
	// a = [1, -1.69065929318241, 0.73248077421585]).
	{
		f0 := 1681.974450955533
		G := 3.999843853973347 // dB
		Q := 0.7071752369554196
		K := math.Tan(math.Pi * f0 / sampleRate)
		Vh := math.Pow(10, G/20)
		Vb := math.Pow(Vh, 0.4996667741545416)
		denom := 1 + K/Q + K*K
		a0 := (Vh + Vb*K/Q + K*K) / denom
		a1 := 2 * (K*K - Vh) / denom
		a2 := (Vh - Vb*K/Q + K*K) / denom
		b1 := 2 * (K*K - 1) / denom
		b2 := (1 - K/Q + K*K) / denom
		k.shelf = biquad{b0: a0, b1: a1, b2: a2, a1: b1, a2: b2}
	}

	// RLB high-pass, 2nd order, ~38 Hz. Per BS.1770 the numerator is
	// exactly [1, -2, 1] (double zero at DC); only the denominator
	// depends on the sample rate.
	// (48 kHz reference: a = [1, -1.99004745483398, 0.99007225036621]).
	{
		f0 := 38.13547087602444
		Q := 0.5003270373238773
		K := math.Tan(math.Pi * f0 / sampleRate)
		denom := 1 + K/Q + K*K
		k.rlb = biquad{
			b0: 1,
			b1: -2,
			b2: 1,
			a1: 2 * (K*K - 1) / denom,
			a2: (1 - K/Q + K*K) / denom,
		}
	}

	return k
}

func (k *kFilter) process(x float64) float64 {
	return k.rlb.process(k.shelf.process(x))
}

func (k *kFilter) reset() {
	k.shelf.reset()
	k.rlb.reset()
}

// loudnessOfEnergy converts a mean-square energy z to loudness in LUFS
// per BS.1770: L = -0.691 + 10*log10(z). Energies at or below zero map
// to -Inf (silence).
func loudnessOfEnergy(z float64) float64 {
	if z <= 0 {
		return math.Inf(-1)
	}
	return -0.691 + 10*math.Log10(z)
}

const (
	// absoluteGateLUFS is the BS.1770 absolute gating threshold.
	absoluteGateLUFS = -70.0
	// relativeGateLU is the relative gate offset below the
	// absolute-gated loudness.
	relativeGateLU = -10.0
)

// Meter is a streaming BS.1770 / EBU R128 loudness meter. It is safe
// for concurrent use by a single writer and multiple readers.
type Meter struct {
	mu sync.Mutex

	sampleRate float64
	weights    []float64

	filters []kFilter
	peaks   []*truePeak

	// 400 ms block ring buffer, 75% overlap => hop of 100 ms.
	blockSize  int
	hopSize    int
	blockBuf   []float64
	blockFill  int
	hopCounter int

	blocks []float64 // 400 ms block energies z_j for gating

	// Momentary (400 ms) and short-term (3 s) sliding windows, in
	// 100 ms hops.
	momWin []float64
	momPos int
	momSum float64
	stWin  []float64
	stPos  int
	stSum  float64

	momentary float64
	shortTerm float64
}

// NewMeter creates a meter for the given sample rate and channel roles.
// Filter coefficients are computed for the exact sample rate supplied.
func NewMeter(sampleRate float64, roles []ChannelRole) (*Meter, error) {
	if len(roles) == 0 {
		return nil, ErrNoChannels
	}
	if sampleRate <= 0 {
		return nil, fmt.Errorf("loudness: invalid sample rate %v", sampleRate)
	}
	blockSize := int(math.Round(0.400 * sampleRate))
	m := &Meter{
		sampleRate: sampleRate,
		weights:    make([]float64, len(roles)),
		filters:    make([]kFilter, len(roles)),
		peaks:      make([]*truePeak, len(roles)),
		blockSize:  blockSize,
		hopSize:    int(math.Round(0.100 * sampleRate)),
		blockBuf:   make([]float64, blockSize),
		momWin:     make([]float64, 4),  // 400 ms in 100 ms hops
		stWin:      make([]float64, 30), // 3 s in 100 ms hops
		momentary:  math.Inf(-1),
		shortTerm:  math.Inf(-1),
	}
	for i, r := range roles {
		m.weights[i] = ChannelWeight(r)
		m.filters[i] = newKFilter(sampleRate)
		m.peaks[i] = newTruePeak(sampleRate)
	}
	return m, nil
}

// NewStandardMeter creates a meter using the standard channel layout
// (see StandardRoles).
func NewStandardMeter(sampleRate float64, numChannels int) (*Meter, error) {
	roles, err := StandardRoles(numChannels)
	if err != nil {
		return nil, err
	}
	return NewMeter(sampleRate, roles)
}

// Channels returns the number of channels.
func (m *Meter) Channels() int { return len(m.weights) }

// Write feeds one buffer of interleaved samples (all channels) into the
// meter. The length must be a multiple of the channel count.
func (m *Meter) Write(interleaved []float64) error {
	if len(interleaved)%len(m.weights) != 0 {
		return fmt.Errorf("loudness: buffer length %d not a multiple of %d channels",
			len(interleaved), len(m.weights))
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.process(interleaved)
	return nil
}

// WriteFrames feeds one buffer of deinterleaved frames (frames[ch][n]).
func (m *Meter) WriteFrames(frames [][]float64) error {
	if len(frames) != len(m.weights) {
		return fmt.Errorf("loudness: got %d channels, want %d", len(frames), len(m.weights))
	}
	n := len(frames[0])
	inter := make([]float64, n*len(frames))
	for ch, f := range frames {
		if len(f) != n {
			return errors.New("loudness: channels have different lengths")
		}
		for i, v := range f {
			inter[i*len(frames)+ch] = v
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.process(inter)
	return nil
}

func (m *Meter) process(inter []float64) {
	nch := len(m.weights)
	for i := 0; i < len(inter); i += nch {
		var sum float64
		for ch := 0; ch < nch; ch++ {
			x := inter[i+ch]
			m.peaks[ch].process(x)
			if m.weights[ch] == 0 {
				continue // LFE excluded from the loudness sum
			}
			y := m.filters[ch].process(x)
			sum += m.weights[ch] * y * y
		}
		m.blockBuf[m.blockFill] = sum
		m.blockFill++
		m.hopCounter++
		if m.hopCounter == m.hopSize {
			m.hopCounter = 0
			m.emitBlock()
		}
		if m.blockFill == m.blockSize {
			// Slide the 400 ms window by one 100 ms hop (75% overlap).
			copy(m.blockBuf, m.blockBuf[m.hopSize:m.blockSize])
			m.blockFill = m.blockSize - m.hopSize
		}
	}
}

// emitBlock is called every 100 ms hop. Once the first full 400 ms
// block is available it computes the block energy, updates the
// momentary/short-term windows and stores the block for gating.
func (m *Meter) emitBlock() {
	if m.blockFill < m.blockSize {
		return
	}
	var energy float64
	for _, v := range m.blockBuf {
		energy += v
	}
	energy /= float64(m.blockSize)
	m.blocks = append(m.blocks, energy)

	// Momentary: mean of the last 4 hops (400 ms).
	m.momSum -= m.momWin[m.momPos]
	m.momWin[m.momPos] = energy
	m.momSum += energy
	m.momPos = (m.momPos + 1) % len(m.momWin)
	m.momentary = loudnessOfEnergy(m.momSum / float64(len(m.momWin)))

	// Short-term: mean of the last 30 hops (3 s).
	m.stSum -= m.stWin[m.stPos]
	m.stWin[m.stPos] = energy
	m.stSum += energy
	m.stPos = (m.stPos + 1) % len(m.stWin)
	m.shortTerm = loudnessOfEnergy(m.stSum / float64(len(m.stWin)))
}

// Integrated returns the gated integrated loudness in LUFS. It applies
// the -70 LUFS absolute gate, then the relative gate 10 LU below the
// absolute-gated loudness. If no block passes the absolute gate it
// returns -Inf.
func (m *Meter) Integrated() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return integratedLoudness(m.blocks)
}

func integratedLoudness(blocks []float64) float64 {
	// Absolute gate at -70 LUFS.
	var absSum float64
	var absCount int
	for _, z := range blocks {
		if loudnessOfEnergy(z) > absoluteGateLUFS {
			absSum += z
			absCount++
		}
	}
	if absCount == 0 {
		return math.Inf(-1)
	}
	// Relative gate 10 LU below the absolute-gated loudness.
	relGate := loudnessOfEnergy(absSum/float64(absCount)) + relativeGateLU
	var sum float64
	var count int
	for _, z := range blocks {
		l := loudnessOfEnergy(z)
		if l > absoluteGateLUFS && l > relGate {
			sum += z
			count++
		}
	}
	if count == 0 {
		return math.Inf(-1)
	}
	return loudnessOfEnergy(sum / float64(count))
}

// Momentary returns the loudness of the last 400 ms in LUFS.
func (m *Meter) Momentary() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.momentary
}

// ShortTerm returns the loudness of the last 3 s in LUFS.
func (m *Meter) ShortTerm() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.shortTerm
}

// TruePeak returns the 4x oversampled true peak per channel, in dBTP.
// Silent channels report -Inf.
func (m *Meter) TruePeak() []float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]float64, len(m.peaks))
	for i, p := range m.peaks {
		out[i] = p.dBTP()
	}
	return out
}

// MaxTruePeak returns the highest true peak across channels, in dBTP.
func (m *Meter) MaxTruePeak() float64 {
	max := math.Inf(-1)
	for _, p := range m.TruePeak() {
		if p > max {
			max = p
		}
	}
	return max
}

// Reset returns the meter to its initial state.
func (m *Meter) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.filters {
		m.filters[i].reset()
	}
	for _, p := range m.peaks {
		p.reset()
	}
	for i := range m.blockBuf {
		m.blockBuf[i] = 0
	}
	m.blockFill, m.hopCounter = 0, 0
	m.blocks = m.blocks[:0]
	for i := range m.momWin {
		m.momWin[i] = 0
	}
	for i := range m.stWin {
		m.stWin[i] = 0
	}
	m.momPos, m.stPos = 0, 0
	m.momSum, m.stSum = 0, 0
	m.momentary, m.shortTerm = math.Inf(-1), math.Inf(-1)
}
