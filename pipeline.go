package tsxform

import (
	"container/heap"
	"encoding/json"
	"fmt"
	"math/big"
)

// Config bundles the transform parameters.
type Config struct {
	MTU          int64
	BytesPerTick int64
	TimeRate     Ratio
	ByteRate     Ratio
}

// Pipeline couples a Scaler with a Sim so input can be fed incrementally,
// paused, checkpointed and restored.
type Pipeline struct {
	Sc  *Scaler
	Sim *Sim
}

// NewPipeline builds a pipeline from a config.
func NewPipeline(cfg Config) (*Pipeline, error) {
	sc, err := NewScaler(cfg.MTU, cfg.TimeRate, cfg.ByteRate)
	if err != nil {
		return nil, err
	}
	sm, err := NewSim(cfg.BytesPerTick)
	if err != nil {
		return nil, err
	}
	return &Pipeline{Sc: sc, Sim: sm}, nil
}

// Feed scales packets and submits their fragments to the link.
func (p *Pipeline) Feed(pkts ...Packet) error {
	for _, pkt := range pkts {
		frags, err := p.Sc.Emit(pkt)
		if err != nil {
			return err
		}
		for _, f := range frags {
			p.Sim.Submit(f)
		}
	}
	return nil
}

// SetTimeRate changes the time rate at the given input tick.
func (p *Pipeline) SetTimeRate(r Ratio, at int64) error { return p.Sc.SetTimeRate(r, at) }

// SetByteRate changes the byte rate.
func (p *Pipeline) SetByteRate(r Ratio) { p.Sc.SetByteRate(r) }

// RunTo advances the link model up to tick end.
func (p *Pipeline) RunTo(end int64) { p.Sim.RunTo(end) }

// Finish drains the pipeline.
func (p *Pipeline) Finish() { p.Sim.Finish() }

// Output returns the transmitted records in departure order.
func (p *Pipeline) Output() []Record { return p.Sim.Out() }

// Transform runs a whole trace in one shot.
func Transform(pkts []Packet, cfg Config) ([]Record, error) {
	pl, err := NewPipeline(cfg)
	if err != nil {
		return nil, err
	}
	if err := pl.Feed(pkts...); err != nil {
		return nil, err
	}
	pl.Finish()
	return pl.Output(), nil
}

type segSnap struct {
	At     int64  `json:"at"`
	Rate   Ratio  `json:"rate"`
	Anchor string `json:"anchor"`
}

type streamSnap struct {
	LastIn       int64 `json:"lastIn"`
	HasInput     bool  `json:"hasInput"`
	LastTick     int64 `json:"lastTick"`
	CumBytes     int64 `json:"cumBytes"`
	ByteBaseCum  int64 `json:"byteBaseCum"`
	ByteBaseEmit int64 `json:"byteBaseEmit"`
	Emitted      int64 `json:"emitted"`
	ByteRate     Ratio `json:"byteRate"`
}

type scalerSnap struct {
	MTU      int64              `json:"mtu"`
	Segs     []segSnap          `json:"segs"`
	ByteRate Ratio              `json:"byteRate"`
	Streams  map[int]streamSnap `json:"streams"`
	NextSeq  int64              `json:"nextSeq"`
	NextPkt  int64              `json:"nextPkt"`
	Clamped  int64              `json:"clamped"`
}

type txSnap struct {
	Frag   Fragment `json:"frag"`
	Depart int64    `json:"depart"`
	Done   int64    `json:"done"`
}

type simSnap struct {
	BW       int64      `json:"bw"`
	Now      int64      `json:"now"`
	Arrivals []Fragment `json:"arrivals"`
	Queue    []Fragment `json:"queue"`
	Inflight *txSnap    `json:"inflight,omitempty"`
	Out      []Record   `json:"out"`
}

type pipelineSnap struct {
	Scaler scalerSnap `json:"scaler"`
	Sim    simSnap    `json:"sim"`
}

// Save serializes the complete recoverable state, including the exact
// rational accumulators, so a restored pipeline continues bit-identically.
func (p *Pipeline) Save() ([]byte, error) {
	snap := pipelineSnap{
		Scaler: scalerSnap{
			MTU:      p.Sc.mtu,
			ByteRate: p.Sc.byteRate,
			Streams:  map[int]streamSnap{},
			NextSeq:  p.Sc.nextSeq,
			NextPkt:  p.Sc.nextPkt,
			Clamped:  p.Sc.Clamped,
		},
		Sim: simSnap{
			BW:       p.Sim.bw,
			Now:      p.Sim.now,
			Arrivals: append([]Fragment(nil), p.Sim.arrivals...),
			Queue:    append([]Fragment(nil), p.Sim.queue...),
			Out:      append([]Record(nil), p.Sim.out...),
		},
	}
	for _, sg := range p.Sc.segs {
		snap.Scaler.Segs = append(snap.Scaler.Segs, segSnap{At: sg.At, Rate: sg.Rate, Anchor: sg.Anchor.RatString()})
	}
	for id, st := range p.Sc.streams {
		snap.Scaler.Streams[id] = streamSnap{
			LastIn:       st.lastIn,
			HasInput:     st.hasInput,
			LastTick:     st.lastTick,
			CumBytes:     st.cumBytes,
			ByteBaseCum:  st.byteBaseCum,
			ByteBaseEmit: st.byteBaseEmit,
			Emitted:      st.emitted,
			ByteRate:     st.byteRate,
		}
	}
	if p.Sim.inflight != nil {
		snap.Sim.Inflight = &txSnap{Frag: p.Sim.inflight.frag, Depart: p.Sim.inflight.depart, Done: p.Sim.inflight.done}
	}
	return json.Marshal(snap)
}

// LoadPipeline restores a pipeline saved with Save.
func LoadPipeline(data []byte) (*Pipeline, error) {
	var snap pipelineSnap
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("load pipeline: %v", err)
	}
	sc := &Scaler{
		mtu:      snap.Scaler.MTU,
		byteRate: snap.Scaler.ByteRate,
		streams:  map[int]*streamState{},
		nextSeq:  snap.Scaler.NextSeq,
		nextPkt:  snap.Scaler.NextPkt,
		Clamped:  snap.Scaler.Clamped,
	}
	for _, sg := range snap.Scaler.Segs {
		anchor, ok := new(big.Rat).SetString(sg.Anchor)
		if !ok {
			return nil, fmt.Errorf("load pipeline: bad rational anchor %q", sg.Anchor)
		}
		sc.segs = append(sc.segs, timeSeg{At: sg.At, Rate: sg.Rate, Anchor: anchor})
	}
	if len(sc.segs) == 0 {
		return nil, fmt.Errorf("load pipeline: no time segments in snapshot")
	}
	for id, st := range snap.Scaler.Streams {
		sc.streams[id] = &streamState{
			lastIn:       st.LastIn,
			hasInput:     st.HasInput,
			lastTick:     st.LastTick,
			cumBytes:     st.CumBytes,
			byteBaseCum:  st.ByteBaseCum,
			byteBaseEmit: st.ByteBaseEmit,
			emitted:      st.Emitted,
			byteRate:     st.ByteRate,
		}
	}
	sm := &Sim{
		bw:       snap.Sim.BW,
		now:      snap.Sim.Now,
		arrivals: append(fragHeap(nil), snap.Sim.Arrivals...),
		queue:    append([]Fragment(nil), snap.Sim.Queue...),
		out:      append([]Record(nil), snap.Sim.Out...),
	}
	heap.Init(&sm.arrivals)
	if snap.Sim.Inflight != nil {
		sm.inflight = &txState{frag: snap.Sim.Inflight.Frag, depart: snap.Sim.Inflight.Depart, done: snap.Sim.Inflight.Done}
	}
	return &Pipeline{Sc: sc, Sim: sm}, nil
}
