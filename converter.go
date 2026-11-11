package h264bs

import (
	"bytes"
	"fmt"
	"sync"
)

// Converter converts H.264 NAL units between Annex B byte-stream and
// AVCC length-prefixed formats. It remembers the latest SPS/PPS NAL
// units (keyed by parameter-set id) and rebuilds its
// AVCDecoderConfigurationRecord whenever a parameter set changes.
type Converter struct {
	mu         sync.Mutex
	lengthSize int

	sps map[uint][]byte
	pps map[uint][]byte

	config []byte
}

// NewConverter creates a Converter with the given AVCC length-prefix
// width (1, 2 or 4 bytes).
func NewConverter(lengthSize int) (*Converter, error) {
	if lengthSize != 1 && lengthSize != 2 && lengthSize != 4 {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidLengthSize, lengthSize)
	}
	return &Converter{
		lengthSize: lengthSize,
		sps:        make(map[uint][]byte),
		pps:        make(map[uint][]byte),
	}, nil
}

// observe records a parameter-set NAL unit. It reports whether the
// stored parameter sets changed, which triggers an avcC rebuild.
func (c *Converter) observe(nal []byte) (bool, error) {
	nalType, err := NALType(nal)
	if err != nil {
		return false, err
	}
	switch nalType {
	case NALTypeSPS:
		sps, err := ParseSPS(nal)
		if err != nil {
			return false, err
		}
		if existing, ok := c.sps[sps.SPSID]; ok && bytes.Equal(existing, nal) {
			return false, nil
		}
		c.sps[sps.SPSID] = append([]byte(nil), nal...)
		return true, nil
	case NALTypePPS:
		ppsID, err := ParsePPSID(nal)
		if err != nil {
			return false, err
		}
		if existing, ok := c.pps[ppsID]; ok && bytes.Equal(existing, nal) {
			return false, nil
		}
		c.pps[ppsID] = append([]byte(nil), nal...)
		return true, nil
	default:
		return false, nil
	}
}

func (c *Converter) rebuildConfigLocked() error {
	if len(c.sps) == 0 {
		c.config = nil
		return nil
	}
	spsNALs := sortedValues(c.sps)
	ppsNALs := sortedValues(c.pps)
	cfg, err := BuildDecoderConfig(spsNALs, ppsNALs, c.lengthSize)
	if err != nil {
		return err
	}
	c.config = cfg
	return nil
}

func sortedValues(m map[uint][]byte) [][]byte {
	ids := make([]uint, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j-1] > ids[j]; j-- {
			ids[j-1], ids[j] = ids[j], ids[j-1]
		}
	}
	out := make([][]byte, 0, len(ids))
	for _, id := range ids {
		out = append(out, m[id])
	}
	return out
}

// ConvertAnnexB converts an Annex B byte stream to AVCC and updates the
// cached avcC when an SPS/PPS change is detected. The second return
// value reports whether the configuration changed.
func (c *Converter) ConvertAnnexB(stream []byte) ([]byte, bool, error) {
	nals := SplitAnnexB(stream)
	c.mu.Lock()
	defer c.mu.Unlock()

	changed := false
	for _, nal := range nals {
		mod, err := c.observe(nal)
		if err != nil {
			return nil, false, err
		}
		changed = changed || mod
	}
	if changed {
		if err := c.rebuildConfigLocked(); err != nil {
			return nil, false, err
		}
	}

	out, err := PackAVCC(nals, c.lengthSize)
	if err != nil {
		return nil, false, err
	}
	return out, changed, nil
}

// ConvertAVCC converts an AVCC stream to Annex B and updates the cached
// avcC when an SPS/PPS change is detected. The second return value
// reports whether the configuration changed.
func (c *Converter) ConvertAVCC(stream []byte) ([]byte, bool, error) {
	nals, err := SplitAVCC(stream, c.lengthSize)
	if err != nil {
		return nil, false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	changed := false
	for _, nal := range nals {
		mod, err := c.observe(nal)
		if err != nil {
			return nil, false, err
		}
		changed = changed || mod
	}
	if changed {
		if err := c.rebuildConfigLocked(); err != nil {
			return nil, false, err
		}
	}

	return PackAnnexB(nals), changed, nil
}

// Config returns a copy of the current AVCDecoderConfigurationRecord,
// or nil if no SPS has been observed yet.
func (c *Converter) Config() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.config == nil {
		return nil
	}
	return append([]byte(nil), c.config...)
}

// LengthSize returns the configured AVCC length-prefix width.
func (c *Converter) LengthSize() int {
	return c.lengthSize
}
