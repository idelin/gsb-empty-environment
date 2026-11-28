package zerobubble

import (
	"fmt"
	"math"
)

const timeEpsilon = 1e-9

// Config describes a homogeneous pipeline. Stages may override the duration
// and memory values for individual stages through StageCosts.
//
// The supplied memory limits are expressed in the same unit as MemF and MemW.
// A single MemoryLimits value is replicated to every stage; otherwise exactly
// one value per stage must be supplied.
type Config struct {
	P int // pipeline stages
	M int // microbatches

	TF    float64 // forward duration
	TB    float64 // activation-gradient (B) duration
	TW    float64 // weight-gradient (W) duration
	TComm float64 // one-way P2P latency

	MemF int64 // activation memory held from F start through B completion
	MemW int64 // memory retained from B completion through W completion

	MemoryLimits []int64
	// MemLimit is an alias for MemoryLimits. Set only one of the two fields.
	MemLimit []int64

	// StageCosts, when non-empty, must contain P entries. Every duration in an
	// entry must be positive; memory values must be non-negative.
	StageCosts []StageCost
}

// StageCost overrides the homogeneous Config values for one stage.
type StageCost struct {
	TF   float64
	TB   float64
	TW   float64
	MemF int64
	MemW int64
}

// StageRuntime is the resolved per-stage configuration used by the scheduler.
type StageRuntime struct {
	TF       float64
	TB       float64
	TW       float64
	MemF     int64
	MemW     int64
	MemLimit int64
}

// ResolvedConfig is the validated, per-stage form of Config.
type ResolvedConfig struct {
	P        int
	M        int
	TComm    float64
	Stages   []StageRuntime
	Original Config
}

// Resolve validates a Config and expands it to one runtime record per stage.
func (c Config) Resolve() (ResolvedConfig, error) {
	if c.P <= 0 {
		return ResolvedConfig{}, fmt.Errorf("P must be positive, got %d", c.P)
	}
	if c.M <= 0 {
		return ResolvedConfig{}, fmt.Errorf("M must be positive, got %d", c.M)
	}
	if !positive(c.TF) {
		return ResolvedConfig{}, fmt.Errorf("TF must be positive, got %g", c.TF)
	}
	if !positive(c.TB) {
		return ResolvedConfig{}, fmt.Errorf("TB must be positive, got %g", c.TB)
	}
	if !positive(c.TW) {
		return ResolvedConfig{}, fmt.Errorf("TW must be positive, got %g", c.TW)
	}
	if c.TComm < 0 || math.IsNaN(c.TComm) || math.IsInf(c.TComm, 0) {
		return ResolvedConfig{}, fmt.Errorf("TComm must be finite and non-negative, got %g", c.TComm)
	}
	if c.MemF < 0 || c.MemW < 0 {
		return ResolvedConfig{}, fmt.Errorf("memories must be non-negative, got MemF=%d MemW=%d", c.MemF, c.MemW)
	}
	if len(c.StageCosts) != 0 && len(c.StageCosts) != c.P {
		return ResolvedConfig{}, fmt.Errorf("StageCosts length must be 0 or P=%d, got %d", c.P, len(c.StageCosts))
	}

	limitSpec := c.MemoryLimits
	if len(limitSpec) == 0 {
		limitSpec = c.MemLimit
	} else if len(c.MemLimit) != 0 {
		return ResolvedConfig{}, fmt.Errorf("set only one of MemoryLimits and MemLimit")
	}
	limits, err := expandLimits(limitSpec, c.P)
	if err != nil {
		return ResolvedConfig{}, err
	}

	stages := make([]StageRuntime, c.P)
	for i := 0; i < c.P; i++ {
		cost := StageCost{TF: c.TF, TB: c.TB, TW: c.TW, MemF: c.MemF, MemW: c.MemW}
		if len(c.StageCosts) != 0 {
			cost = c.StageCosts[i]
			if !positive(cost.TF) || !positive(cost.TB) || !positive(cost.TW) {
				return ResolvedConfig{}, fmt.Errorf("stage %d: TF/TB/TW must be positive", i)
			}
			if cost.MemF < 0 || cost.MemW < 0 {
				return ResolvedConfig{}, fmt.Errorf("stage %d: memories must be non-negative", i)
			}
		}
		limit := limits[i]
		minimum := maxInt64(cost.MemF, cost.MemW)
		if limit < minimum {
			return ResolvedConfig{}, fmt.Errorf(
				"stage %d: memLimit=%d is below the minimum resident requirement max(memF,memW)=%d",
				i, limit, minimum,
			)
		}
		stages[i] = StageRuntime{
			TF:       cost.TF,
			TB:       cost.TB,
			TW:       cost.TW,
			MemF:     cost.MemF,
			MemW:     cost.MemW,
			MemLimit: limit,
		}
	}

	return ResolvedConfig{P: c.P, M: c.M, TComm: c.TComm, Stages: stages, Original: c}, nil
}

func expandLimits(in []int64, p int) ([]int64, error) {
	out := make([]int64, p)
	switch len(in) {
	case 0:
		return nil, fmt.Errorf("MemoryLimits must contain one value or P=%d values", p)
	case 1:
		if in[0] < 0 {
			return nil, fmt.Errorf("MemoryLimits must be non-negative")
		}
		for i := range out {
			out[i] = in[0]
		}
		return out, nil
	case p:
		for i, v := range in {
			if v < 0 {
				return nil, fmt.Errorf("stage %d: MemoryLimits must be non-negative", i)
			}
			out[i] = v
		}
		return out, nil
	default:
		return nil, fmt.Errorf("MemoryLimits must contain 1 or P=%d values, got %d", p, len(in))
	}
}

func positive(v float64) bool {
	return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0)
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
