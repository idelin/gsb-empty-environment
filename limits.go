package layerpack

import "errors"

var (
	ErrBudgetInput  = errors.New("layerpack: input budget exceeded")
	ErrBudgetOutput = errors.New("layerpack: output budget exceeded")
	ErrBudgetLayers = errors.New("layerpack: layer budget exceeded")
	ErrBudgetRatio  = errors.New("layerpack: expansion ratio budget exceeded")
	ErrChecksum     = errors.New("layerpack: checksum mismatch")
	ErrVersion      = errors.New("layerpack: unsupported format version")
	ErrFormat       = errors.New("layerpack: malformed container")
)

// Limits describes the resource budgets for one encode or decode operation.
// Budgets are shared across all layers of a container: every layer's output
// is charged against the same MaxOutputBytes pool, so layers that are each
// individually under the limit are still rejected when their cumulative
// consumption exceeds it.
type Limits struct {
	// MaxInputBytes bounds the bytes read from the source: the raw input
	// when encoding, the whole container when decoding.
	MaxInputBytes int64
	// MaxOutputBytes bounds the total bytes produced across all layers:
	// the sum of compressed layer outputs when encoding, the sum of
	// decompressed layer outputs when decoding.
	MaxOutputBytes int64
	// MaxLayers bounds the number of compression layers in a container.
	MaxLayers int
	// MaxRatio bounds the expansion ratio (decompressed/compressed bytes),
	// enforced per layer and on the cumulative totals when decoding.
	MaxRatio float64
	// AllowHighRatio explicitly opts in to containers whose expansion
	// ratio exceeds MaxRatio (e.g. highly compressible legitimate data).
	AllowHighRatio bool
}

// DefaultLimits returns conservative budgets suitable for untrusted input.
func DefaultLimits() Limits {
	return Limits{
		MaxInputBytes:  64 << 20,
		MaxOutputBytes: 256 << 20,
		MaxLayers:      8,
		MaxRatio:       100,
	}
}

// meter tracks the remaining shared budget for one operation.
type meter struct {
	inLeft     int64
	outLeft    int64
	layersLeft int
}

func newMeter(l Limits) meter {
	return meter{inLeft: l.MaxInputBytes, outLeft: l.MaxOutputBytes, layersLeft: l.MaxLayers}
}

func (m *meter) chargeInput(n int64) error {
	if n > m.inLeft {
		return ErrBudgetInput
	}
	m.inLeft -= n
	return nil
}

// chargeOutput consumes n bytes of the shared output budget. It is always
// called before the corresponding buffer is allocated.
func (m *meter) chargeOutput(n int64) error {
	if n > m.outLeft {
		return ErrBudgetOutput
	}
	m.outLeft -= n
	return nil
}

func (m *meter) chargeLayers(n int) error {
	if n > m.layersLeft {
		return ErrBudgetLayers
	}
	m.layersLeft -= n
	return nil
}

// checkRatio verifies that expanding comp bytes into raw bytes stays within
// the configured ratio budget, unless high ratios were explicitly allowed.
func checkRatio(l Limits, raw, comp int64) error {
	if l.AllowHighRatio || raw == 0 {
		return nil
	}
	if comp <= 0 {
		return ErrBudgetRatio
	}
	if float64(raw) > l.MaxRatio*float64(comp) {
		return ErrBudgetRatio
	}
	return nil
}
