package taskpkg

import (
	"errors"
	"fmt"
	"math"
)

// PairEvidence is one channel pair's evidence in one fixed window.
type PairEvidence struct {
	A           int       `json:"a"`
	B           int       `json:"b"`
	Window      int       `json:"window"`
	Correlation float64   `json:"correlation"`
	EnergyRatio float64   `json:"energy_ratio"`
	RMSA        float64   `json:"rms_a"`
	RMSB        float64   `json:"rms_b"`
	SilentA     bool      `json:"silent_a"`
	SilentB     bool      `json:"silent_b"`
	State       PairState `json:"state"`
	Sign        int       `json:"sign,omitempty"`
	HasEdge     bool      `json:"has_edge"`
}

// WindowReport contains the independent evidence from one fixed window.
type WindowReport struct {
	Index       int            `json:"index"`
	StartSample int            `json:"start_sample"`
	EndSample   int            `json:"end_sample"`
	Pairs       []PairEvidence `json:"pairs"`
}

// PairSummary is the cross-window confirmation state for a channel pair.
type PairSummary struct {
	A              int       `json:"a"`
	B              int       `json:"b"`
	Sign           int       `json:"sign,omitempty"`
	CurrentSign    int       `json:"current_sign,omitempty"`
	State          PairState `json:"state"`
	LastState      PairState `json:"last_state"`
	MaxConsecutive int       `json:"max_consecutive"`
	StreakWindows  int       `json:"streak_windows"`
	FirstWindow    int       `json:"first_window"`
	LastWindow     int       `json:"last_window"`
	Correlation    float64   `json:"correlation"`
	EnergyRatio    float64   `json:"energy_ratio"`
	Confirmed      bool      `json:"confirmed"`
	Active         bool      `json:"active"`
}

// Report is diagnostic only. It deliberately contains no corrected audio or
// inversion action.
type Report struct {
	Config      Config         `json:"config"`
	Channels    []string       `json:"channels"`
	Windows     []WindowReport `json:"windows"`
	Pairs       []PairSummary  `json:"pairs"`
	Edges       []SignedEdge   `json:"edges"`
	Consistent  bool           `json:"consistent"`
	Assignment  []int          `json:"assignment,omitempty"`
	Conflict    *ConflictCycle `json:"conflict,omitempty"`
	Diagnostics []Diagnostic   `json:"diagnostics,omitempty"`
}

// Diagnostic is a human-oriented note. Codes are stable and intended for
// host-side filtering.
type Diagnostic struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	A        int    `json:"a"`
	B        int    `json:"b"`
	Window   int    `json:"window"`
}

// Analyze channels over non-overlapping (or hop-configured) fixed windows.
// Partial trailing data is ignored because it is not a complete fixed window.
func Analyze(channelNames []string, samples [][]float64, cfg Config) (Report, error) {
	if len(channelNames) == 0 && len(samples) > 0 {
		channelNames = make([]string, len(samples))
		for i := range channelNames {
			channelNames[i] = fmt.Sprintf("ch%d", i)
		}
	}
	if len(channelNames) != len(samples) {
		return Report{}, fmt.Errorf("taskpkg: got %d channel names, want %d sample channels", len(channelNames), len(samples))
	}
	analyzer, err := NewAnalyzer(channelNames, cfg)
	if err != nil {
		return Report{}, err
	}
	if err := analyzer.Push(samples); err != nil {
		return Report{}, err
	}
	return analyzer.Finish(), nil
}

// AnalyzeSamples is Analyze with generated channel names.
func AnalyzeSamples(samples [][]float64, cfg Config) (Report, error) {
	return Analyze(nil, samples, cfg)
}

// Channel is a named sample channel for AnalyzeChannels.
type Channel struct {
	Name    string
	Samples []float64
}

// AnalyzeChannels is the named-channel form of Analyze.
func AnalyzeChannels(channels []Channel, cfg Config) (Report, error) {
	names := make([]string, len(channels))
	samples := make([][]float64, len(channels))
	for i := range channels {
		names[i] = channels[i].Name
		samples[i] = channels[i].Samples
	}
	return Analyze(names, samples, cfg)
}

type pairKey struct {
	a int
	b int
}

type currentStreak struct {
	sign        int
	length      int
	startWindow int
	lastWindow  int
	corrSum     float64
	energySum   float64
}

type pairTrack struct {
	current    currentStreak
	max        currentStreak
	lastState  PairState
	lastCorr   float64
	lastEnergy float64
	lastWindow int
}

// Analyzer is a streaming host: arbitrary equal-channel chunks can be pushed.
// Window boundaries depend only on sample positions, so chunking does not alter
// the result.
type Analyzer struct {
	cfg            Config
	names          []string
	buffers        [][]float64
	samplePosition int
	startedAt      int
	windowIndex    int
	windows        []WindowReport
	tracks         map[pairKey]*pairTrack
	finished       bool
}

// NewAnalyzer creates a streaming analyzer. Channel names may be empty, in
// which case they are not used for indexing.
func NewAnalyzer(channelNames []string, cfg Config) (*Analyzer, error) {
	norm, err := cfg.normalized()
	if err != nil {
		return nil, err
	}
	if len(channelNames) == 0 {
		return nil, errors.New("taskpkg: at least one channel is required")
	}
	if hasDuplicateNonEmpty(channelNames) {
		return nil, errors.New("taskpkg: channel names must be unique when provided")
	}
	names := make([]string, len(channelNames))
	copy(names, channelNames)
	return &Analyzer{
		cfg:     norm,
		names:   names,
		buffers: make([][]float64, len(names)),
		tracks:  make(map[pairKey]*pairTrack),
	}, nil
}

func hasDuplicateNonEmpty(names []string) bool {
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if name == "" {
			continue
		}
		if seen[name] {
			return true
		}
		seen[name] = true
	}
	return false
}

// Push appends one block. Every channel in the block must have the same
// length. Pushing after Finish is an error.
func (a *Analyzer) Push(block [][]float64) error {
	if a.finished {
		return errors.New("taskpkg: analyzer already finished")
	}
	if len(block) != len(a.buffers) {
		return fmt.Errorf("taskpkg: got %d channels, want %d", len(block), len(a.buffers))
	}
	if len(block) == 0 {
		return nil
	}
	n := len(block[0])
	for i := range block {
		if len(block[i]) != n {
			return fmt.Errorf("taskpkg: channel %d has %d samples, want %d", i, len(block[i]), n)
		}
		for j, sample := range block[i] {
			if math.IsNaN(sample) || math.IsInf(sample, 0) {
				return fmt.Errorf("taskpkg: non-finite sample in channel %d at block offset %d", i, j)
			}
		}
	}
	if n == 0 {
		return nil
	}
	for i := range a.buffers {
		a.buffers[i] = append(a.buffers[i], block[i]...)
	}
	a.processAvailable()
	return nil
}

// PushChannel appends a single channel separately. It is useful for hosts
// that read channel data independently. The caller must eventually supply
// equal amounts to all channels before Finish.
func (a *Analyzer) PushChannel(channel int, samples []float64) error {
	if a.finished {
		return errors.New("taskpkg: analyzer already finished")
	}
	if channel < 0 || channel >= len(a.buffers) {
		return fmt.Errorf("taskpkg: channel index %d outside [0, %d)", channel, len(a.buffers))
	}
	for i, sample := range samples {
		if math.IsNaN(sample) || math.IsInf(sample, 0) {
			return fmt.Errorf("taskpkg: non-finite sample at offset %d", i)
		}
	}
	a.buffers[channel] = append(a.buffers[channel], samples...)
	a.processAvailable()
	return nil
}

func (a *Analyzer) processAvailable() {
	// A window can start only when every channel has reached its end. This
	// also makes independent PushChannel calls deterministic.
	minLen := a.channelMinLen()
	window := a.cfg.WindowSize
	hop := a.cfg.HopSize
	for a.startedAt+window <= minLen {
		chunk := make([][]float64, len(a.buffers))
		for i := range a.buffers {
			chunk[i] = a.buffers[i][a.startedAt : a.startedAt+window]
		}
		a.analyzeWindow(a.windowIndex, a.samplePosition+a.startedAt, chunk)
		a.windowIndex++
		a.startedAt += hop
	}
	a.trimConsumed()
}

func (a *Analyzer) analyzeWindow(index, start int, samples [][]float64) {
	wr := WindowReport{
		Index:       index,
		StartSample: start,
		EndSample:   start + a.cfg.WindowSize,
	}
	seen := make(map[pairKey]bool, len(a.tracks))
	for i := 0; i < len(samples); i++ {
		for j := i + 1; j < len(samples); j++ {
			metrics, err := ComparePair(samples[i], samples[j], a.cfg)
			if err != nil {
				// Input validation in Push makes this unreachable.
				panic(err)
			}
			key := pairKey{i, j}
			seen[key] = true
			ev := PairEvidence{
				A:           i,
				B:           j,
				Window:      index,
				Correlation: metrics.Correlation,
				EnergyRatio: metrics.EnergyRatio,
				RMSA:        metrics.RMSLeft,
				RMSB:        metrics.RMSRight,
				SilentA:     metrics.SilentLeft,
				SilentB:     metrics.SilentRight,
				State:       metrics.State,
				Sign:        metrics.Sign,
				HasEdge:     metrics.HasEdge,
			}
			wr.Pairs = append(wr.Pairs, ev)
			a.recordPair(index, key, ev)
		}
	}
	// A window that is unknown for a pair breaks its consecutive-hit streak.
	for key, track := range a.tracks {
		if !seen[key] {
			a.breakStreak(track)
		}
	}
	a.windows = append(a.windows, wr)
}

func (a *Analyzer) recordPair(window int, key pairKey, ev PairEvidence) {
	track := a.tracks[key]
	if track == nil {
		track = &pairTrack{}
		a.tracks[key] = track
	}
	track.lastState = ev.State
	track.lastCorr = ev.Correlation
	track.lastEnergy = ev.EnergyRatio
	track.lastWindow = window
	if !ev.HasEdge {
		a.breakStreak(track)
		return
	}
	if track.current.sign != ev.Sign {
		a.breakStreak(track)
		track.current = currentStreak{sign: ev.Sign, startWindow: window}
	}
	if track.current.length == 0 {
		track.current.startWindow = window
	}
	track.current.length++
	track.current.lastWindow = window
	track.current.corrSum += ev.Correlation
	track.current.energySum += ev.EnergyRatio
	if track.current.length > track.max.length ||
		(track.current.length == track.max.length && track.current.lastWindow > track.max.lastWindow) {
		track.max = track.current
	}
}

func (a *Analyzer) breakStreak(track *pairTrack) {
	if track.current.length > track.max.length ||
		(track.current.length == track.max.length && track.current.lastWindow > track.max.lastWindow) {
		track.max = track.current
	}
	track.current = currentStreak{}
}

func (a *Analyzer) channelMinLen() int {
	if len(a.buffers) == 0 {
		return 0
	}
	minimum := len(a.buffers[0])
	for _, buffer := range a.buffers[1:] {
		if len(buffer) < minimum {
			minimum = len(buffer)
		}
	}
	return minimum
}

func (a *Analyzer) trimConsumed() {
	keepFrom := a.startedAt
	for i := range a.buffers {
		if keepFrom > len(a.buffers[i]) {
			continue
		}
		if keepFrom > 0 {
			copy(a.buffers[i], a.buffers[i][keepFrom:])
			a.buffers[i] = a.buffers[i][:len(a.buffers[i])-keepFrom]
		}
	}
	if keepFrom > 0 {
		a.samplePosition += keepFrom
		a.startedAt -= keepFrom
	}
}

// Finish closes the stream, finalizing open streaks and solving the signed
// graph. Incomplete trailing samples are intentionally ignored.
func (a *Analyzer) Finish() Report {
	if a.finished {
		return a.buildReport()
	}
	a.finished = true
	for _, track := range a.tracks {
		if track.current.length > track.max.length ||
			(track.current.length == track.max.length && track.current.lastWindow > track.max.lastWindow) {
			track.max = track.current
		}
	}
	return a.buildReport()
}

func (a *Analyzer) buildReport() Report {
	report := Report{
		Config:     a.cfg,
		Channels:   append([]string(nil), a.names...),
		Windows:    append([]WindowReport(nil), a.windows...),
		Consistent: true,
	}
	edges := make([]SignedEdge, 0)
	for i := 0; i < len(a.names); i++ {
		for j := i + 1; j < len(a.names); j++ {
			key := pairKey{i, j}
			track := a.tracks[key]
			if track == nil {
				report.Pairs = append(report.Pairs, PairSummary{
					A: i, B: j, State: PairWeakCorrelation,
				})
				continue
			}
			summary := PairSummary{
				A:              i,
				B:              j,
				Sign:           track.max.sign,
				CurrentSign:    track.current.sign,
				State:          track.lastState,
				LastState:      track.lastState,
				MaxConsecutive: track.max.length,
				StreakWindows:  track.current.length,
				FirstWindow:    track.max.startWindow,
				LastWindow:     track.max.lastWindow,
				Confirmed:      track.max.length > 0 && track.max.length >= a.cfg.ConfirmationWindows,
				Active:         track.current.length > 0 && track.current.length >= a.cfg.ConfirmationWindows,
			}
			if track.max.length > 0 {
				summary.State = PairEdge
			}
			if track.max.length > 0 {
				summary.Correlation = track.max.corrSum / float64(track.max.length)
				summary.EnergyRatio = track.max.energySum / float64(track.max.length)
			} else {
				summary.Correlation = track.lastCorr
				summary.EnergyRatio = track.lastEnergy
			}
			report.Pairs = append(report.Pairs, summary)
			if summary.Confirmed {
				edges = append(edges, SignedEdge{
					A:           i,
					B:           j,
					Sign:        track.max.sign,
					Correlation: summary.Correlation,
					EnergyRatio: summary.EnergyRatio,
					Consecutive: track.max.length,
					FirstWindow: track.max.startWindow,
					LastWindow:  track.max.lastWindow,
				})
			}
		}
	}
	report.Edges = edges
	result, err := CheckPolarity(len(a.names), edges)
	if err != nil {
		// All edges originate internally, so this indicates a programming
		// error rather than caller input.
		panic(err)
	}
	report.Consistent = result.Consistent
	report.Assignment = result.Assignment
	report.Conflict = result.Conflict
	report.Diagnostics = a.diagnosticsFor(edges, result)
	return report
}

func (a *Analyzer) diagnosticsFor(edges []SignedEdge, result PolarityResult) []Diagnostic {
	var diagnostics []Diagnostic
	if !result.Consistent && result.Conflict != nil {
		diagnostics = append(diagnostics, Diagnostic{
			Code:     "polarity_conflict",
			Severity: "error",
			Message:  fmt.Sprintf("inconsistent polarity evidence; shortest conflict cycle has %d edges", result.Conflict.Length),
		})
	}
	for i := 0; i < len(a.names); i++ {
		for j := i + 1; j < len(a.names); j++ {
			key := pairKey{i, j}
			track := a.tracks[key]
			if track == nil {
				continue
			}
			if track.lastState == PairSilent {
				diagnostics = append(diagnostics, Diagnostic{
					Code: "silent_pair", Severity: "info",
					Message: "at least one channel is silent; pair is unknown",
					A:       i, B: j, Window: track.lastWindow,
				})
				continue
			}
			if track.max.length > 0 && track.max.length < a.cfg.ConfirmationWindows {
				diagnostics = append(diagnostics, Diagnostic{
					Code: "unconfirmed_edge", Severity: "info",
					Message: fmt.Sprintf("pair has only %d consecutive hit(s), needs %d", track.max.length, a.cfg.ConfirmationWindows),
					A:       i, B: j, Window: track.max.lastWindow,
				})
			}
		}
	}
	if len(edges) == 0 {
		diagnostics = append(diagnostics, Diagnostic{
			Code: "no_graph_evidence", Severity: "warning",
			Message: "no pair met correlation, energy, and confirmation requirements",
		})
	}
	return diagnostics
}
