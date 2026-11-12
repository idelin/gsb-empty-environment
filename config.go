package taskpkg

import (
	"errors"
	"fmt"
	"math"
)

// Config controls fixed-window phase correlation analysis.
type Config struct {
	// WindowSize is the number of samples in each analysis window.
	WindowSize int `json:"window_size"`
	// HopSize is the distance between window starts. A zero value means one
	// full, non-overlapping window.
	HopSize int `json:"hop_size"`
	// CorrelationThreshold is the inclusive lower bound on |correlation| for
	// an edge to be usable.
	CorrelationThreshold float64 `json:"correlation_threshold"`
	// MinEnergyRatio and MaxEnergyRatio are the inclusive bounds on
	// energy(A)/energy(B). Values outside the interval are marked unknown.
	MinEnergyRatio float64 `json:"min_energy_ratio"`
	MaxEnergyRatio float64 `json:"max_energy_ratio"`
	// SilenceRMS marks a channel as silent when its RMS after mean removal is
	// at or below this value. A silent endpoint makes the pair unknown.
	SilenceRMS float64 `json:"silence_rms"`
	// ConfirmationWindows is the number of consecutive usable windows with the
	// same sign required before a pair becomes graph evidence.
	ConfirmationWindows int `json:"confirmation_windows"`
}

// DefaultConfig returns a conservative diagnostic configuration.
func DefaultConfig() Config {
	return Config{
		WindowSize:           4096,
		HopSize:              0,
		CorrelationThreshold: 0.90,
		MinEnergyRatio:       0.25,
		MaxEnergyRatio:       4.0,
		SilenceRMS:           1e-8,
		ConfirmationWindows:  3,
	}
}

func (c Config) normalized() (Config, error) {
	if c.WindowSize <= 0 {
		return Config{}, errors.New("taskpkg: WindowSize must be positive")
	}
	if c.HopSize == 0 {
		c.HopSize = c.WindowSize
	}
	if c.HopSize < 0 || c.HopSize > c.WindowSize {
		return Config{}, errors.New("taskpkg: HopSize must be in [1, WindowSize]")
	}
	if math.IsNaN(c.CorrelationThreshold) || math.IsInf(c.CorrelationThreshold, 0) ||
		c.CorrelationThreshold < 0 || c.CorrelationThreshold > 1 {
		return Config{}, errors.New("taskpkg: CorrelationThreshold must be in [0, 1]")
	}
	if math.IsNaN(c.MinEnergyRatio) || math.IsInf(c.MinEnergyRatio, 0) ||
		c.MinEnergyRatio < 0 {
		return Config{}, fmt.Errorf("taskpkg: MinEnergyRatio must be finite and non-negative")
	}
	if math.IsNaN(c.MaxEnergyRatio) || math.IsInf(c.MaxEnergyRatio, -1) ||
		c.MaxEnergyRatio <= 0 || c.MaxEnergyRatio < c.MinEnergyRatio {
		return Config{}, fmt.Errorf("taskpkg: MaxEnergyRatio must be positive and >= MinEnergyRatio")
	}
	if math.IsNaN(c.SilenceRMS) || math.IsInf(c.SilenceRMS, 0) || c.SilenceRMS < 0 {
		return Config{}, errors.New("taskpkg: SilenceRMS must be finite and non-negative")
	}
	if c.ConfirmationWindows <= 0 {
		c.ConfirmationWindows = 1
	}
	return c, nil
}
