package taskpkg

import (
	"errors"
	"fmt"
	"math"
)

var errNilEngine = errors.New("taskpkg: nil concealment engine")

// GapAnalysis is a snapshot of the model used at the start of the current or
// most recent gap. Models are copied, so callers cannot mutate engine state.
type GapAnalysis struct {
	Period            int
	PeriodCorrelation float64
	Periodic          bool
	Models            [2]ARModel
	ResidualRMS       [2]float64
}

type channelState struct {
	trueHistory     ringBuffer
	residualHistory ringBuffer
	synthesis       ringBuffer
	model           ARModel
	residualRMS     float64
	noise           noiseSource
	noiseBuffer     []float64
	noisePos        int
}

// PLC is a stateful stereo packet-loss concealment engine.
//
// Real samples update only the true-history statistics. Synthetic samples
// advance the predictor synthesis state but never enter the true history used
// to estimate autocorrelation. Left and right share one period decision but
// retain independent predictors, residual energies, noise streams, and
// synthesis state. A PLC is not safe for concurrent use.
type PLC struct {
	cfg Config

	channel [2]channelState

	gapActive   bool
	missingRun  int
	recoveryPos int

	analysis GapAnalysis
}

// Concealer is an alias for PLC.
type Concealer = PLC

// New creates a concealment engine. The caller must keep the returned engine
// for the lifetime of one playback stream.
func New(cfg Config) (*PLC, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	p := &PLC{cfg: cfg}
	p.resetChannels()
	return p, nil
}

// NewConcealer is an alias for New.
func NewConcealer(cfg Config) (*PLC, error) {
	return New(cfg)
}

// Config returns the immutable engine configuration.
func (p *PLC) Config() Config {
	if p == nil {
		return Config{}
	}
	return p.cfg
}

// Reset clears audio, analysis, fade, and random-generator state while
// retaining the configuration. A reset engine produces the same output as a
// freshly constructed engine for the same input and seed.
func (p *PLC) Reset() {
	if p == nil {
		return
	}
	p.gapActive = false
	p.missingRun = 0
	p.recoveryPos = 0
	p.analysis = GapAnalysis{}
	p.resetChannels()
}

func (p *PLC) resetChannels() {
	for ch := range p.channel {
		seed := p.cfg.Seed
		if ch == 1 {
			seed = ^seed + 0x5deece66d
		}
		p.channel[ch] = channelState{
			trueHistory:     newRingBuffer(p.cfg.Window + p.cfg.Order),
			residualHistory: newRingBuffer(historyCapacity(p.cfg.Window, p.cfg.MaxPeriod)),
			synthesis:       newRingBuffer(synthesisCapacity(p.cfg.Order)),
			noise:           newNoiseSource(seed),
		}
	}
}

// InGap reports whether the engine is currently filling loss or performing
// its recovery crossfade.
func (p *PLC) InGap() bool {
	return p != nil && p.gapActive
}

// MissingSamples returns the number of missing samples in the current gap.
// It is reset when recovery completes.
func (p *PLC) MissingSamples() int {
	if p == nil {
		return 0
	}
	return p.missingRun
}

// LastGapAnalysis returns the predictor and period analysis captured when the
// current gap began. Before the first gap it is the zero value.
func (p *PLC) LastGapAnalysis() GapAnalysis {
	if p == nil {
		return GapAnalysis{}
	}
	out := p.analysis
	for ch := range out.Models {
		out.Models[ch] = cloneModel(out.Models[ch])
	}
	return out
}

// ProcessStereo consumes one planar stereo block. valid is an optional
// per-frame mask: true means the frame is real PCM, false means the frame was
// lost. A nil mask means every frame is real. The returned left and right
// slices always have exactly len(left) samples, including any recovery
// crossfade samples.
//
// left and right must have equal length. Programmer errors in buffer lengths
// panic; use ProcessStereoInto when an error return is preferable.
func (p *PLC) ProcessStereo(left, right []float64, valid []bool) ([]float64, []float64) {
	if p == nil {
		panic(errNilEngine)
	}
	n := len(left)
	if len(right) != n || (valid != nil && len(valid) != n) {
		panic("taskpkg: stereo buffers and validity mask must have equal lengths")
	}
	outLeft := make([]float64, n)
	outRight := make([]float64, n)
	if err := p.processStereoInto(outLeft, outRight, left, right, valid); err != nil {
		panic(err)
	}
	return outLeft, outRight
}

// ProcessStereoInto writes into caller-owned destination buffers. Destination
// slices may be the same slices as the sources; each sample is consumed before
// the corresponding output is written.
func (p *PLC) ProcessStereoInto(dstLeft, dstRight, left, right []float64, valid []bool) error {
	if p == nil {
		return errNilEngine
	}
	n := len(left)
	if len(right) != n {
		return fmt.Errorf("taskpkg: left has %d samples, right has %d", n, len(right))
	}
	if valid != nil && len(valid) != n {
		return fmt.Errorf("taskpkg: validity mask has %d entries, need %d", len(valid), n)
	}
	if len(dstLeft) != n || len(dstRight) != n {
		return fmt.Errorf("taskpkg: destination lengths must both be %d", n)
	}
	return p.processStereoInto(dstLeft, dstRight, left, right, valid)
}

func (p *PLC) processStereoInto(dstLeft, dstRight, left, right []float64, valid []bool) error {
	for i := range left {
		present := valid == nil || valid[i]
		if present {
			dstLeft[i], dstRight[i] = p.processReal(left[i], right[i])
		} else {
			dstLeft[i], dstRight[i] = p.processLost()
		}
	}
	return nil
}

// ConcealStereo emits exactly frames synthetic stereo samples. It starts a
// gap if one is not active. This is convenient when a host already knows that
// an entire block is missing.
func (p *PLC) ConcealStereo(frames int) ([]float64, []float64) {
	if p == nil {
		panic(errNilEngine)
	}
	if frames < 0 {
		panic("taskpkg: negative frame count")
	}
	left := make([]float64, frames)
	right := make([]float64, frames)
	for i := 0; i < frames; i++ {
		left[i], right[i] = p.processLost()
	}
	return left, right
}

// FeedStereo presents a block known to contain only real PCM. It is
// equivalent to ProcessStereo with a nil mask. If a gap is active, the first
// Crossfade real samples are blended and returned to the caller.
func (p *PLC) FeedStereo(left, right []float64) ([]float64, []float64) {
	return p.ProcessStereo(left, right, nil)
}

// ProcessMono is a single-channel convenience wrapper. It uses the left
// channel state and a silent right channel.
func (p *PLC) ProcessMono(samples []float64, valid []bool) []float64 {
	if p == nil {
		panic(errNilEngine)
	}
	right := make([]float64, len(samples))
	outLeft, _ := p.ProcessStereo(samples, right, valid)
	return outLeft
}

// ProcessInterleavedStereo consumes interleaved L,R,L,R PCM and returns
// interleaved PCM with the same number of frames. It panics if the sample
// count is odd or the optional per-frame mask has a different length.
func (p *PLC) ProcessInterleavedStereo(data []float64, valid []bool) []float64 {
	if p == nil {
		panic(errNilEngine)
	}
	if len(data)%2 != 0 {
		panic("taskpkg: interleaved stereo data must have an even sample count")
	}
	frames := len(data) / 2
	if valid != nil && len(valid) != frames {
		panic("taskpkg: validity mask must match the interleaved frame count")
	}
	left := make([]float64, frames)
	right := make([]float64, frames)
	for i := 0; i < frames; i++ {
		left[i] = data[2*i]
		right[i] = data[2*i+1]
	}
	outLeft, outRight := p.ProcessStereo(left, right, valid)
	for i := 0; i < frames; i++ {
		data[2*i] = outLeft[i]
		data[2*i+1] = outRight[i]
	}
	return data
}

func (p *PLC) processLost() (float64, float64) {
	if !p.gapActive || p.recoveryPos > 0 {
		// A real sample after a gap starts a recovery. If loss returns before
		// that crossfade finishes, the prior gap was interrupted; re-analyze
		// the newly available real samples and start a fresh envelope.
		p.beginGap()
		p.gapActive = true
	}

	gain := p.gapGain(p.missingRun)
	left := p.advancePrediction(0) * gain
	right := p.advancePrediction(1) * gain
	p.missingRun++
	return left, right
}

func (p *PLC) processReal(left, right float64) (float64, float64) {
	if p.gapActive {
		if p.recoveryPos < p.cfg.Crossfade {
			predLeft := p.advancePrediction(0)
			predRight := p.advancePrediction(1)
			realWeight := float64(p.recoveryPos+1) / float64(p.cfg.Crossfade)
			predWeight := 1 - realWeight
			envelope := p.gapGain(p.missingRun + p.recoveryPos)

			outLeft := predLeft*envelope*predWeight + left*realWeight
			outRight := predRight*envelope*predWeight + right*realWeight
			p.appendReal(0, left)
			p.appendReal(1, right)
			p.recoveryPos++
			if p.recoveryPos >= p.cfg.Crossfade {
				p.finishGap()
			}
			return outLeft, outRight
		}
		p.finishGap()
	}

	p.appendReal(0, left)
	p.appendReal(1, right)
	return left, right
}

func (p *PLC) finishGap() {
	p.gapActive = false
	p.missingRun = 0
	p.recoveryPos = 0
}

func (p *PLC) gapGain(missing int) float64 {
	if missing <= 0 || p.cfg.Decay == 1 {
		return 1
	}
	if p.cfg.Decay == 0 {
		return 0
	}
	return math.Pow(p.cfg.Decay, float64(missing))
}

func (p *PLC) beginGap() {
	var residuals [2][]float64
	for ch := range p.channel {
		cs := &p.channel[ch]
		trueSamples := cs.trueHistory.snapshot()
		model := FitAR(trueSamples, p.cfg.Window, p.cfg.Order)
		residual := Residual(trueSamples, model.Coefficients)
		if len(residual) > p.cfg.Window {
			residual = residual[len(residual)-p.cfg.Window:]
		}
		cs.model = model
		cs.residualHistory.reset()
		for _, v := range residual {
			cs.residualHistory.append(v)
		}
		cs.residualRMS = rms(residual)
		cs.synthesis.reset()
		for _, v := range tailValues(trueSamples, len(model.Coefficients)) {
			cs.synthesis.append(v)
		}
		cs.noiseBuffer = nil
		cs.noisePos = 0
		residuals[ch] = residual

		p.analysis.Models[ch] = cloneModel(model)
		p.analysis.ResidualRMS[ch] = cs.residualRMS
	}

	period, score := BestPeriod(residuals[:], p.cfg.MinPeriod, p.cfg.MaxPeriod)
	p.analysis.Period = period
	p.analysis.PeriodCorrelation = score
	p.analysis.Periodic = period > 0 && score > p.cfg.MinCorrelation
	p.missingRun = 0
	p.recoveryPos = 0
}

func (p *PLC) advancePrediction(ch int) float64 {
	cs := &p.channel[ch]
	predicted := predict(cs.model.Coefficients, &cs.synthesis)

	var excitation float64
	if p.analysis.Periodic && cs.residualHistory.count >= p.analysis.Period {
		excitation = cs.residualHistory.fromEnd(p.analysis.Period - 1)
	} else {
		if len(cs.noiseBuffer) == 0 || cs.noisePos >= len(cs.noiseBuffer) {
			cs.noiseBuffer = cs.noise.makeNoiseBlock(cs.residualRMS)
			cs.noisePos = 0
		}
		excitation = cs.noiseBuffer[cs.noisePos]
		cs.noisePos++
	}

	sample := predicted + excitation
	if math.IsNaN(sample) || math.IsInf(sample, 0) {
		sample = 0
	}
	cs.synthesis.append(sample)
	cs.residualHistory.append(excitation)
	return sample
}

func (p *PLC) appendReal(ch int, sample float64) {
	cs := &p.channel[ch]
	cs.trueHistory.append(sample)
	cs.synthesis.append(sample)
}

func historyCapacity(window, maxPeriod int) int {
	capacity := window
	if maxPeriod+1 > capacity {
		capacity = maxPeriod + 1
	}
	if capacity < 1 {
		capacity = 1
	}
	return capacity
}

func synthesisCapacity(order int) int {
	if order < 1 {
		return 1
	}
	return order
}

func tailValues(x []float64, n int) []float64 {
	if n <= 0 || len(x) == 0 {
		return nil
	}
	if n > len(x) {
		n = len(x)
	}
	return x[len(x)-n:]
}

func cloneModel(model ARModel) ARModel {
	model.Coefficients = append([]float64(nil), model.Coefficients...)
	model.Reflection = append([]float64(nil), model.Reflection...)
	return model
}
