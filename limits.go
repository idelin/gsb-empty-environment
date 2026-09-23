package lcc

import (
	"errors"
	"fmt"
	"math/bits"
)

const (
	FormatVersion uint16 = 1

	codecRLE       uint16 = 1
	headerSize            = 16
	recordSize            = 16
	maxPayloadSize        = uint64(1<<32 - 1)
	maxLayerCount         = 1<<16 - 1
)

// Limits are shared by every layer in one encode or decode operation.
// MaxExpansionBasisPoints is one hundred times the allowed decoded/encoded
// ratio: 1000 permits 10x expansion, and 2500 permits 25x expansion.
type Limits struct {
	MaxInputBytes           int64
	MaxOutputBytes          int64
	MaxLayers               int
	MaxExpansionBasisPoints int64
}

func DefaultLimits() Limits {
	return Limits{
		MaxInputBytes:           1 << 20,
		MaxOutputBytes:          10 << 20,
		MaxLayers:               64,
		MaxExpansionBasisPoints: 1000,
	}
}

func validateLimits(l Limits) error {
	if l.MaxInputBytes <= 0 || l.MaxOutputBytes <= 0 ||
		l.MaxLayers <= 0 || l.MaxExpansionBasisPoints <= 0 {
		return errors.New("all limits must be positive")
	}
	if l.MaxLayers > maxLayerCount {
		return fmt.Errorf("max layers %d exceeds uint16 format limit", l.MaxLayers)
	}
	return nil
}

func withinRatio(decoded, encoded uint64, basisPoints int64) bool {
	if decoded == 0 {
		return true
	}
	if encoded == 0 {
		return false
	}
	hi, lo := bits.Mul64(encoded, uint64(basisPoints))
	if hi != 0 {
		return true
	}
	return decoded*100 <= lo
}

func reserve(want, remaining int64, kind string) error {
	if want < 0 || want > remaining {
		return fmt.Errorf("%s budget exceeded: need %d, have %d", kind, want, remaining)
	}
	return nil
}
