// Package taskpkg implements a multichannel linked dynamic range compressor
// with a fixed audio lookahead.
package taskpkg

import (
	"errors"
	"fmt"
	"math"
	"sync"
)

// DefaultSilenceFloorDB is used when Config.SilenceFloorDB is zero.
const DefaultSilenceFloorDB = -200.0

// Params contains the parameters that may change while audio is streaming.
// Time constants are in seconds. A zero time constant means instantaneous
// attack or release.
type Params struct {
	ThresholdDB    float64
	Ratio          float64
	KneeDB         float64
	AttackSeconds  float64
	ReleaseSeconds float64
}

// Config contains the immutable stream configuration and the initial
// parameters.
type Config struct {
	SampleRate       float64
	Channels         int
	LookaheadSamples int
	SilenceFloorDB   float64
	ThresholdDB      float64
	Ratio            float64
	KneeDB           float64
	AttackSeconds    float64
	ReleaseSeconds   float64
}

// Parameters returns the mutable portion of the configuration.
func (c Config) Parameters() Params {
	return Params{
		ThresholdDB:    c.ThresholdDB,
		Ratio:          c.Ratio,
		KneeDB:         c.KneeDB,
		AttackSeconds:  c.AttackSeconds,
		ReleaseSeconds: c.ReleaseSeconds,
	}
}

// StaticGainDB computes the compressor gain for an input level in dBFS.
//
// With x = inputDB - thresholdDB and a positive knee width K:
//
//	x <= -K/2: 0
//	x >=  K/2: (1/R - 1) * x
//	otherwise: (1/R - 1) * (x + K/2)^2 / (2K)
//
// A nonpositive knee width selects a hard knee. Nonpositive ratios are
// rejected by constructors; this function defensively returns 0 dB for them
// so that a malformed parameter cannot introduce a division by zero.
func StaticGainDB(inputDB, thresholdDB, ratio, kneeDB float64) float64 {
	if math.IsNaN(inputDB) || math.IsNaN(thresholdDB) ||
		math.IsNaN(ratio) || math.IsNaN(kneeDB) ||
		ratio <= 0 || math.IsInf(ratio, 0) {
		return 0
	}

	x := inputDB - thresholdDB
	if math.IsNaN(x) {
		return 0
	}
	if math.IsInf(x, 0) {
		if x < 0 {
			return 0
		}
		if math.IsInf(ratio, 0) {
			return 0
		}
		return (1/ratio - 1) * x
	}

	if kneeDB <= 0 || math.IsInf(kneeDB, 0) {
		if x <= 0 {
			return 0
		}
		return (1/ratio - 1) * x
	}

	halfKnee := kneeDB / 2
	if x <= -halfKnee {
		return 0
	}
	if x >= halfKnee {
		return (1/ratio - 1) * x
	}

	offset := x + halfKnee
	return (1/ratio - 1) * offset * offset / (2 * kneeDB)
}

// StaticGainDB applies Params to an input level in dBFS.
func (p Params) StaticGainDB(inputDB float64) float64 {
	return StaticGainDB(inputDB, p.ThresholdDB, p.Ratio, p.KneeDB)
}

// LevelDB converts a nonnegative peak amplitude to dBFS while keeping
// digital silence finite. A zero or non-finite peak maps to floorDB. Finite
// levels below the floor are also clamped to floorDB.
func LevelDB(peak, floorDB float64) float64 {
	if math.IsNaN(floorDB) || math.IsInf(floorDB, 0) {
		floorDB = DefaultSilenceFloorDB
	}
	if peak <= 0 || math.IsNaN(peak) || math.IsInf(peak, 0) {
		return floorDB
	}
	db := 20 * math.Log10(peak)
	if db < floorDB {
		return floorDB
	}
	return db
}

// SmoothingCoefficient returns the pole of a first-order exponential
// smoother for the supplied time constant and sample rate. The response
// reaches 1 - 1/e of a step after one time constant.
func SmoothingCoefficient(seconds, sampleRate float64) float64 {
	if seconds <= 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) ||
		sampleRate <= 0 || math.IsNaN(sampleRate) || math.IsInf(sampleRate, 0) {
		return 0
	}
	product := seconds * sampleRate
	if math.IsInf(product, 0) {
		return 1
	}
	if product <= 0 {
		return 0
	}
	return math.Exp(-1 / product)
}

// Compressor is a stateful, sample-clocked linked compressor. Audio is
// delayed by LookaheadSamples, while each current input sample determines
// the gain applied to the delayed audio sample at the output.
//
// Process calls are serialized by an internal lock, but all audio processing
// should normally be performed from one goroutine.
type Compressor struct {
	mu sync.RWMutex

	sampleRate       float64
	channels         int
	lookaheadSamples int
	silenceFloorDB   float64

	params       Params
	attackCoeff  float64
	releaseCoeff float64
	envelopeDB   float64

	delay    [][]float64
	writePos int
}

// NewCompressor validates cfg and creates a compressor at unity gain with
// an empty lookahead delay line.
func NewCompressor(cfg Config) (*Compressor, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	floor := cfg.SilenceFloorDB
	if floor == 0 {
		floor = DefaultSilenceFloorDB
	}

	params := cfg.Parameters()
	c := &Compressor{
		sampleRate:       cfg.SampleRate,
		channels:         cfg.Channels,
		lookaheadSamples: cfg.LookaheadSamples,
		silenceFloorDB:   floor,
		params:           params,
		attackCoeff:      SmoothingCoefficient(params.AttackSeconds, cfg.SampleRate),
		releaseCoeff:     SmoothingCoefficient(params.ReleaseSeconds, cfg.SampleRate),
		delay:            make([][]float64, cfg.Channels),
	}
	for ch := range c.delay {
		c.delay[ch] = make([]float64, cfg.LookaheadSamples)
	}
	return c, nil
}

// New is an alias for NewCompressor.
func New(cfg Config) (*Compressor, error) {
	return NewCompressor(cfg)
}

// MustNewCompressor panics if cfg is invalid.
func MustNewCompressor(cfg Config) *Compressor {
	c, err := NewCompressor(cfg)
	if err != nil {
		panic(err)
	}
	return c
}

func validateConfig(cfg Config) error {
	if cfg.SampleRate <= 0 || math.IsNaN(cfg.SampleRate) || math.IsInf(cfg.SampleRate, 0) {
		return errors.New("sample rate must be finite and greater than zero")
	}
	if cfg.Channels < 1 {
		return errors.New("channel count must be at least one")
	}
	if cfg.LookaheadSamples < 0 {
		return errors.New("lookahead length must not be negative")
	}
	if math.IsNaN(cfg.SilenceFloorDB) || math.IsInf(cfg.SilenceFloorDB, 0) {
		return errors.New("silence floor must be finite")
	}
	return validateParams(cfg.Parameters())
}

func validateParams(p Params) error {
	if math.IsNaN(p.ThresholdDB) || math.IsInf(p.ThresholdDB, 0) {
		return errors.New("threshold must be finite")
	}
	if p.Ratio <= 0 || math.IsNaN(p.Ratio) || math.IsInf(p.Ratio, 0) {
		return errors.New("ratio must be finite and greater than zero")
	}
	if p.KneeDB < 0 || math.IsNaN(p.KneeDB) || math.IsInf(p.KneeDB, 0) {
		return errors.New("knee width must be finite and not negative")
	}
	if p.AttackSeconds < 0 || math.IsNaN(p.AttackSeconds) || math.IsInf(p.AttackSeconds, 0) {
		return errors.New("attack time must be finite and not negative")
	}
	if p.ReleaseSeconds < 0 || math.IsNaN(p.ReleaseSeconds) || math.IsInf(p.ReleaseSeconds, 0) {
		return errors.New("release time must be finite and not negative")
	}
	return nil
}

// SetParams replaces the mutable parameters. It leaves the delay line,
// write position, and current envelope untouched, so an update cannot drop
// audio samples or change the configured latency. The new parameters apply
// to detection samples processed after this call.
func (c *Compressor) SetParams(p Params) error {
	if err := validateParams(p); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.params = p
	c.attackCoeff = SmoothingCoefficient(p.AttackSeconds, c.sampleRate)
	c.releaseCoeff = SmoothingCoefficient(p.ReleaseSeconds, c.sampleRate)
	return nil
}

// UpdateParams is an alias for SetParams.
func (c *Compressor) UpdateParams(p Params) error {
	return c.SetParams(p)
}

// Params returns a snapshot of the current mutable parameters.
func (c *Compressor) Params() Params {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.params
}

// LatencySamples returns the fixed audio lookahead in samples.
func (c *Compressor) LatencySamples() int {
	return c.lookaheadSamples
}

// LatencySeconds returns the fixed audio lookahead in seconds.
func (c *Compressor) LatencySeconds() float64 {
	return float64(c.lookaheadSamples) / c.sampleRate
}

// Channels returns the configured channel count.
func (c *Compressor) Channels() int {
	return c.channels
}

// SampleRate returns the configured sample rate.
func (c *Compressor) SampleRate() float64 {
	return c.sampleRate
}

// EnvelopeDB returns the current smoothed gain in dB.
func (c *Compressor) EnvelopeDB() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.envelopeDB
}

// Reset clears the delay line and returns the envelope to unity gain. The
// configuration and parameters are retained.
func (c *Compressor) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for ch := range c.delay {
		for i := range c.delay[ch] {
			c.delay[ch][i] = 0
		}
	}
	c.writePos = 0
	c.envelopeDB = 0
}

// Process compresses a deinterleaved block whose outer index is the channel
// and whose inner index is the sample. The returned gain track is the gain
// in dB applied to each output sample. Input and output blocks have the same
// shape.
func (c *Compressor) Process(input [][]float64) (output [][]float64, gainDB []float64, err error) {
	if err := c.validateBlock(input); err != nil {
		return nil, nil, err
	}
	frames := 0
	if len(input) > 0 {
		frames = len(input[0])
	}
	output = make([][]float64, c.channels)
	for ch := range output {
		output[ch] = make([]float64, frames)
	}
	gainDB = make([]float64, frames)
	if frames == 0 {
		return output, gainDB, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	frame := make([]float64, c.channels)
	outFrame := make([]float64, c.channels)
	for i := 0; i < frames; i++ {
		for ch := 0; ch < c.channels; ch++ {
			frame[ch] = input[ch][i]
		}
		gain := c.processFrameInto(frame, outFrame)
		for ch := 0; ch < c.channels; ch++ {
			output[ch][i] = outFrame[ch]
		}
		gainDB[i] = gain
	}
	return output, gainDB, nil
}

// ProcessBlock is an alias for Process.
func (c *Compressor) ProcessBlock(input [][]float64) (output [][]float64, gainDB []float64, err error) {
	return c.Process(input)
}

// ProcessWithParams updates the parameters and then processes the block.
// The update affects every detection sample in the block and does not clear
// the lookahead delay line.
func (c *Compressor) ProcessWithParams(input [][]float64, p Params) (output [][]float64, gainDB []float64, err error) {
	if err := c.SetParams(p); err != nil {
		return nil, nil, err
	}
	return c.Process(input)
}

// ProcessSample processes one deinterleaved frame containing one sample per
// channel. It returns the delayed output frame and the dB gain applied to it.
func (c *Compressor) ProcessSample(frame []float64) (output []float64, gainDB float64, err error) {
	if len(frame) != c.channels {
		return nil, 0, fmt.Errorf("expected %d channels, got %d", c.channels, len(frame))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	output = make([]float64, c.channels)
	gainDB = c.processFrameInto(frame, output)
	return output, gainDB, nil
}

// ProcessInterleaved compresses an interleaved block. The returned gain
// track contains one dB value per frame.
func (c *Compressor) ProcessInterleaved(input []float64) (output []float64, gainDB []float64, err error) {
	if len(input)%c.channels != 0 {
		return nil, nil, fmt.Errorf("interleaved input length %d is not divisible by %d channels", len(input), c.channels)
	}
	frames := len(input) / c.channels
	output = make([]float64, len(input))
	gainDB = make([]float64, frames)
	if frames == 0 {
		return output, gainDB, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	frame := make([]float64, c.channels)
	outFrame := make([]float64, c.channels)
	for i := 0; i < frames; i++ {
		copy(frame, input[i*c.channels:(i+1)*c.channels])
		gain := c.processFrameInto(frame, outFrame)
		copy(output[i*c.channels:(i+1)*c.channels], outFrame)
		gainDB[i] = gain
	}
	return output, gainDB, nil
}

// Flush pushes LookaheadSamples silence samples through the compressor and
// returns the delayed tail of the audio that was still in the delay line.
func (c *Compressor) Flush() (output [][]float64, gainDB []float64) {
	frames := c.lookaheadSamples
	input := make([][]float64, c.channels)
	for ch := range input {
		input[ch] = make([]float64, frames)
	}
	output, gainDB, _ = c.Process(input)
	return output, gainDB
}

func (c *Compressor) validateBlock(input [][]float64) error {
	if len(input) != c.channels {
		return fmt.Errorf("expected %d channels, got %d", c.channels, len(input))
	}
	frames := -1
	for ch := 0; ch < c.channels; ch++ {
		if frames < 0 {
			frames = len(input[ch])
			continue
		}
		if len(input[ch]) != frames {
			return fmt.Errorf("channel %d has %d samples, expected %d", ch, len(input[ch]), frames)
		}
	}
	return nil
}

func (c *Compressor) processFrameInto(frame, output []float64) float64 {
	peak := 0.0
	for ch := 0; ch < c.channels; ch++ {
		sample := frame[ch]
		if math.IsNaN(sample) || math.IsInf(sample, 0) {
			sample = 0
		}
		output[ch] = sample
		abs := math.Abs(sample)
		if abs > peak {
			peak = abs
		}
	}

	targetDB := 0.0
	if peak > 0 {
		levelDB := LevelDB(peak, c.silenceFloorDB)
		targetDB = StaticGainDB(levelDB, c.params.ThresholdDB, c.params.Ratio, c.params.KneeDB)
	}

	coeff := c.releaseCoeff
	if targetDB < c.envelopeDB {
		coeff = c.attackCoeff
	}
	c.envelopeDB = coeff*c.envelopeDB + (1-coeff)*targetDB

	linearGain := math.Pow(10, c.envelopeDB/20)
	if math.IsNaN(linearGain) || math.IsInf(linearGain, 0) {
		linearGain = 0
	}

	if c.lookaheadSamples == 0 {
		for ch := 0; ch < c.channels; ch++ {
			output[ch] *= linearGain
		}
	} else {
		pos := c.writePos
		for ch := 0; ch < c.channels; ch++ {
			delayed := c.delay[ch][pos]
			c.delay[ch][pos] = output[ch]
			output[ch] = delayed * linearGain
		}
		c.writePos = (pos + 1) % c.lookaheadSamples
	}
	return c.envelopeDB
}
