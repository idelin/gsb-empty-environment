package netsec

import (
	"encoding/json"
	"fmt"
)

type chainDocument struct {
	Version     int          `json:"version"`
	Header      Header       `json:"header"`
	Generations []Generation `json:"generations"`
}

// MarshalJSON implements json.Marshaler without exposing mutable internal
// slices.
func (c *Chain) MarshalJSON() ([]byte, error) {
	if c == nil {
		return []byte("null"), nil
	}
	return json.Marshal(chainDocument{
		Version:     chainVersion,
		Header:      c.header,
		Generations: c.Generations(),
	})
}

// UnmarshalJSON implements json.Unmarshaler. It restores the complete chain
// without validating signatures; call Verify before trusting it.
func (c *Chain) UnmarshalJSON(data []byte) error {
	if c == nil {
		return fmt.Errorf("netsec: nil chain receiver")
	}
	var doc chainDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("netsec: decode chain: %w", err)
	}
	if doc.Version != chainVersion {
		return fmt.Errorf("netsec: unsupported chain version %d", doc.Version)
	}
	if len(doc.Generations) == 0 {
		return ErrEmptyChain
	}
	c.header = doc.Header.normalized()
	c.generations = make([]Generation, len(doc.Generations))
	for i, g := range doc.Generations {
		c.generations[i] = g.clone()
	}
	return nil
}

// ParseChain decodes a chain previously produced by MarshalJSON. The decoded
// chain is not validated until Verify is called.
func ParseChain(data []byte) (*Chain, error) {
	var c Chain
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}
