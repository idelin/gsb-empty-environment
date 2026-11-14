package netsec

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// Config controls spatial and temporal generalization, sampling,
// perturbation, suppression, k-anonymity and publication risk.
type Config struct {
	// K is the minimum equivalence-class size for every quasi-identifier
	// combination. It must be at least 2.
	K int

	// SpatialCellMeters is the edge length of the generalization grid.
	SpatialCellMeters float64

	// TimeBucket is the temporal generalization granularity.
	TimeBucket time.Duration

	// MaxSamplePoints caps the number of points retained per trajectory.
	// First and last observations are retained.
	MaxSamplePoints int

	// MinSampleInterval removes points that are too close in time. Zero keeps
	// all points unless MaxSamplePoints is exceeded.
	MinSampleInterval time.Duration

	// PerturbationMeters is the maximum uniform spatial displacement applied
	// after generalization. Perturbation is clamped inside the grid cell, so it
	// does not change derived location quasi-identifiers. Zero disables it.
	PerturbationMeters float64

	// TemporalJitter is a uniform displacement inside each time bucket. It is
	// clamped to the bucket, so the generalized time is unchanged. Zero
	// disables it.
	TemporalJitter time.Duration

	// MinLocationSupport suppresses points in a generalized cell visited by
	// fewer than this many trajectories. Zero means K.
	MinLocationSupport int

	// MinTrajectorySupport suppresses a trajectory whose generalized path is
	// shared by fewer than this many trajectories. Zero means K. Set to 1 to
	// disable this check.
	MinTrajectorySupport int

	// MinPatternSupport suppresses a regular temporal pattern shared by fewer
	// than this many trajectories. Zero means K.
	MinPatternSupport int

	// RegularRepeatDays is the number of distinct dates on which the same
	// time-of-day bucket must occur before it is treated as a regular pattern.
	RegularRepeatDays int

	// MinPointsAfterSuppression suppresses trajectories that retain fewer
	// points after rare-location removal.
	MinPointsAfterSuppression int

	// MaxRisk is the largest acceptable re-identification probability (and
	// overall risk score). Zero means 1/K.
	MaxRisk float64

	// QICombinations explicitly selects the quasi-identifier combinations to
	// check. The full set is always checked in addition to these combinations.
	// When empty, single attributes, pairs and the full set are checked.
	QICombinations [][]string

	// PreserveIDs keeps sanitized record IDs. When false, stable pseudonymous
	// IDs are generated.
	PreserveIDs bool

	// Seed makes suppression, sampling and perturbation deterministic.
	Seed int64

	// MaxIterations bounds the suppression refinement loop.
	MaxIterations int
}

// DefaultConfig returns a conservative publication policy. Callers should
// normally lower K or increase the grid size for small demonstration datasets.
func DefaultConfig() Config {
	return Config{
		K:                         5,
		SpatialCellMeters:         250,
		TimeBucket:                time.Hour,
		MaxSamplePoints:           64,
		PerturbationMeters:        25,
		MinLocationSupport:        5,
		MinTrajectorySupport:      5,
		MinPatternSupport:         5,
		RegularRepeatDays:         2,
		MinPointsAfterSuppression: 2,
		MaxRisk:                   0.2,
		Seed:                      1,
		MaxIterations:             64,
	}
}

// Normalize fills zero values with defaults and derives dependent values.
func NormalizeConfig(cfg Config) (Config, error) {
	def := DefaultConfig()
	cfg.QICombinations = cloneCombinations(cfg.QICombinations)

	if cfg.K == 0 {
		cfg.K = def.K
	}
	if cfg.SpatialCellMeters == 0 {
		cfg.SpatialCellMeters = def.SpatialCellMeters
	}
	if cfg.TimeBucket == 0 {
		cfg.TimeBucket = def.TimeBucket
	}
	if cfg.MaxSamplePoints == 0 {
		cfg.MaxSamplePoints = def.MaxSamplePoints
	}
	if cfg.MinLocationSupport == 0 {
		cfg.MinLocationSupport = cfg.K
	}
	if cfg.MinTrajectorySupport == 0 {
		cfg.MinTrajectorySupport = cfg.K
	}
	if cfg.MinPatternSupport == 0 {
		cfg.MinPatternSupport = cfg.K
	}
	if cfg.RegularRepeatDays == 0 {
		cfg.RegularRepeatDays = def.RegularRepeatDays
	}
	if cfg.MinPointsAfterSuppression == 0 {
		cfg.MinPointsAfterSuppression = def.MinPointsAfterSuppression
	}
	if cfg.MaxRisk == 0 {
		cfg.MaxRisk = 1 / float64(cfg.K)
	}
	if cfg.Seed == 0 {
		cfg.Seed = def.Seed
	}
	if cfg.MaxIterations == 0 {
		cfg.MaxIterations = def.MaxIterations
	}
	if cfg.K != def.K {
		// Keep the support thresholds in step with K when the caller changed K
		// on a configuration obtained from DefaultConfig.
		if cfg.MinLocationSupport == def.MinLocationSupport {
			cfg.MinLocationSupport = cfg.K
		}
		if cfg.MinTrajectorySupport == def.MinTrajectorySupport {
			cfg.MinTrajectorySupport = cfg.K
		}
		if cfg.MinPatternSupport == def.MinPatternSupport {
			cfg.MinPatternSupport = cfg.K
		}
		if cfg.MaxRisk == def.MaxRisk {
			cfg.MaxRisk = 1 / float64(cfg.K)
		}
	}
	if math.IsNaN(cfg.MaxRisk) || math.IsInf(cfg.MaxRisk, 0) || cfg.MaxRisk > 1 {
		return Config{}, errors.New("netsec: MaxRisk must be in (0, 1]")
	}
	return cfg, cfg.validate()
}

func cloneCombinations(in [][]string) [][]string {
	if in == nil {
		return nil
	}
	out := make([][]string, len(in))
	for i := range in {
		out[i] = append([]string(nil), in[i]...)
	}
	return out
}

func (cfg Config) validate() error {
	if cfg.K < 2 {
		return fmt.Errorf("netsec: K must be at least 2, got %d", cfg.K)
	}
	if math.IsNaN(cfg.SpatialCellMeters) || math.IsInf(cfg.SpatialCellMeters, 0) || cfg.SpatialCellMeters <= 0 {
		return errors.New("netsec: SpatialCellMeters must be positive")
	}
	if cfg.TimeBucket <= 0 {
		return errors.New("netsec: TimeBucket must be positive")
	}
	if cfg.MaxSamplePoints < 1 {
		return errors.New("netsec: MaxSamplePoints must be at least 1")
	}
	if cfg.MinSampleInterval < 0 || cfg.TemporalJitter < 0 {
		return errors.New("netsec: intervals and jitter must not be negative")
	}
	if math.IsNaN(cfg.PerturbationMeters) || math.IsInf(cfg.PerturbationMeters, 0) || cfg.PerturbationMeters < 0 {
		return errors.New("netsec: PerturbationMeters must not be negative")
	}
	if cfg.MinLocationSupport < 1 {
		return errors.New("netsec: MinLocationSupport must be at least 1")
	}
	if cfg.MinTrajectorySupport < 1 {
		return errors.New("netsec: MinTrajectorySupport must be at least 1")
	}
	if cfg.MinPatternSupport < 1 {
		return errors.New("netsec: MinPatternSupport must be at least 1")
	}
	if cfg.RegularRepeatDays < 2 {
		return errors.New("netsec: RegularRepeatDays must be at least 2")
	}
	if cfg.MinPointsAfterSuppression < 1 {
		return errors.New("netsec: MinPointsAfterSuppression must be at least 1")
	}
	if cfg.MaxRisk <= 0 || cfg.MaxRisk > 1 {
		return errors.New("netsec: MaxRisk must be in (0, 1]")
	}
	if cfg.MaxIterations < 1 {
		return errors.New("netsec: MaxIterations must be positive")
	}
	for _, combo := range cfg.QICombinations {
		if len(combo) == 0 {
			return errors.New("netsec: empty quasi-identifier combination")
		}
		for _, name := range combo {
			if name == "" {
				return errors.New("netsec: empty quasi-identifier name")
			}
		}
	}
	return nil
}
