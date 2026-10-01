// Package red implements a RED (Random Early Detection) queue discipline
// with an injectable random source keyed by event identity.
//
// Design invariants:
//   - The random source is injectable (Source interface).
//   - Average-queue-length EWMA updates (UpdateAverage) and idle decay
//     (DecayIdle) are separate operations.
//   - Idle decay of the average and packet-size normalization of the drop
//     probability are strictly separated.
//   - Below MinThBytes there is never a probabilistic drop; at or above
//     MaxThBytes every packet is force-dropped.
package red

import (
	"errors"
	"fmt"
	"math"
)

// Params holds the RED configuration. All thresholds are in bytes.
type Params struct {
	MinThBytes     float64 // average queue length where probabilistic dropping starts
	MaxThBytes     float64 // average queue length where dropping becomes forced
	MaxProb        float64 // drop probability at MaxThBytes, before packet-size normalization
	Weight         float64 // EWMA weight wq for the average queue length
	CapacityBytes  int     // hard queue capacity; excess arrivals are tail-dropped
	MaxPacketBytes int     // reference packet size for drop-probability normalization
}

// Validate checks that the parameters are well formed.
func (p Params) Validate() error {
	switch {
	case p.MinThBytes < 0:
		return errors.New("red: MinThBytes must be >= 0")
	case p.MaxThBytes <= p.MinThBytes:
		return errors.New("red: MaxThBytes must be > MinThBytes")
	case p.MaxProb <= 0 || p.MaxProb > 1:
		return errors.New("red: MaxProb must be in (0, 1]")
	case p.Weight <= 0 || p.Weight >= 1:
		return errors.New("red: Weight must be in (0, 1)")
	case p.CapacityBytes <= 0:
		return errors.New("red: CapacityBytes must be > 0")
	case p.MaxPacketBytes <= 0:
		return errors.New("red: MaxPacketBytes must be > 0")
	}
	return nil
}

// Source supplies randomness for drop decisions. Implementations must be a
// pure function of (seed, identity) so that replaying or chunking the event
// stream never changes the sample drawn for a given event.
type Source interface {
	Uint64(identity uint64) uint64
}

// KeyedSource derives a deterministic random stream per event identity by
// running the splitmix64 finalizer over (Seed, identity). The PRNG state is
// therefore split by event identity: the sample for event X does not depend
// on how many other events were sampled before it.
type KeyedSource struct{ Seed uint64 }

// Uint64 returns the deterministic random word for one event identity.
func (s KeyedSource) Uint64(identity uint64) uint64 {
	z := s.Seed + 0x9E3779B97F4A7C15 + identity*0xBF58476D1CE4E5B9
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// Verdict is the outcome of one enqueue decision.
type Verdict int

const (
	// Accept means the packet is admitted to the queue.
	Accept Verdict = iota
	// DropProbabilistic means an early drop while avg is in [MinTh, MaxTh).
	DropProbabilistic
	// DropForced means a mandatory drop because avg >= MaxTh.
	DropForced
	// DropOverflow means a tail drop because the queue capacity is exceeded.
	DropOverflow
)

func (v Verdict) String() string {
	switch v {
	case Accept:
		return "accept"
	case DropProbabilistic:
		return "drop-probabilistic"
	case DropForced:
		return "drop-forced"
	case DropOverflow:
		return "drop-overflow"
	}
	return "unknown"
}

// Decision records the full basis for one enqueue decision, so every drop
// can be explained and audited after the fact.
type Decision struct {
	Verdict  Verdict
	Avg      float64 // average queue length after update, before the verdict
	BaseProb float64 // probability from the avg-vs-thresholds curve
	Prob     float64 // probability after packet-size normalization
	Draw     float64 // uniform [0,1) sample drawn for this event
	Reason   string  // human-readable basis for the verdict
}

// RED carries the average-queue-length state of one queue.
type RED struct {
	params    Params
	avg       float64
	idle      bool
	idleSince uint64
}

// New validates p and returns a RED instance with a zeroed average.
func New(p Params) (*RED, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &RED{params: p}, nil
}

// Params returns the configuration.
func (r *RED) Params() Params { return r.params }

// Average returns the current average queue length in bytes.
func (r *RED) Average() float64 { return r.avg }

// UpdateAverage applies one EWMA step toward the instantaneous queue
// length. It performs no idle decay; idle decay lives only in DecayIdle.
func (r *RED) UpdateAverage(queueBytes int) {
	r.avg += r.params.Weight * (float64(queueBytes) - r.avg)
}

// DecayIdle decays the average geometrically for idleTicks empty ticks:
// avg *= (1 - wq)^idleTicks. It never touches the instantaneous queue
// length and never touches drop probabilities.
func (r *RED) DecayIdle(idleTicks uint64) {
	if idleTicks == 0 {
		return
	}
	r.avg *= math.Pow(1-r.params.Weight, float64(idleTicks))
}

// NoteQueueEmpty records that the queue drained at tick now; the next
// Decide call will apply DecayIdle for the elapsed idle ticks.
func (r *RED) NoteQueueEmpty(now uint64) {
	r.idle = true
	r.idleSince = now
}

// BaseProbability maps the current average to a drop probability in [0,1]
// before any packet-size normalization.
func (r *RED) BaseProbability() float64 {
	switch {
	case r.avg < r.params.MinThBytes:
		return 0
	case r.avg >= r.params.MaxThBytes:
		return 1
	default:
		return r.params.MaxProb * (r.avg - r.params.MinThBytes) /
			(r.params.MaxThBytes - r.params.MinThBytes)
	}
}

// NormalizeForPacketSize scales a base drop probability by the packet size
// relative to the reference size, clamped to [0,1]. It is independent of
// the average queue length and of idle decay.
func NormalizeForPacketSize(base float64, packetBytes, maxPacketBytes int) float64 {
	p := base * float64(packetBytes) / float64(maxPacketBytes)
	if p > 1 {
		return 1
	}
	if p < 0 {
		return 0
	}
	return p
}

// uniform maps a 64-bit word to [0,1) using its top 53 bits.
func uniform(u uint64) float64 {
	return float64(u>>11) * (1.0 / 9007199254740992.0)
}

// Decide updates the average and decides whether an arriving packet is
// enqueued or dropped. eventID must uniquely and deterministically identify
// the arrival event, so the sampled Draw is stable under replay and under
// chunked processing of the same event stream.
func (r *RED) Decide(now uint64, queueBytes, packetBytes int, src Source, eventID uint64) Decision {
	if r.idle {
		r.DecayIdle(now - r.idleSince)
		r.idle = false
	}
	r.UpdateAverage(queueBytes)
	avg := r.avg
	p := r.params

	if avg >= p.MaxThBytes {
		return Decision{
			Verdict: DropForced, Avg: avg, BaseProb: 1, Prob: 1,
			Reason: fmt.Sprintf("forced drop: avg %.4f >= maxTh %.4f", avg, p.MaxThBytes),
		}
	}
	if avg < p.MinThBytes {
		return Decision{
			Verdict: Accept, Avg: avg,
			Reason: fmt.Sprintf("accept: avg %.4f < minTh %.4f (no probabilistic drop)", avg, p.MinThBytes),
		}
	}
	base := r.BaseProbability()
	prob := NormalizeForPacketSize(base, packetBytes, p.MaxPacketBytes)
	draw := uniform(src.Uint64(eventID))
	if draw < prob {
		return Decision{
			Verdict: DropProbabilistic, Avg: avg, BaseProb: base, Prob: prob, Draw: draw,
			Reason: fmt.Sprintf("early drop: draw %.6f < p %.6f (base %.6f x size %d/%d)",
				draw, prob, base, packetBytes, p.MaxPacketBytes),
		}
	}
	return Decision{
		Verdict: Accept, Avg: avg, BaseProb: base, Prob: prob, Draw: draw,
		Reason: fmt.Sprintf("accept: draw %.6f >= p %.6f", draw, prob),
	}
}
