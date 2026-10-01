package pktscale

import (
	"encoding/json"
	"io"
	"math/big"
)

// Checkpoint is the fully recoverable scaler state. Rationals are stored
// as exact "num/den" strings, so save/load is lossless: processing
// interrupted at any record boundary, checkpointed, and resumed later
// (or fed in differently-sized chunks) yields identical output to a
// single uninterrupted run.
type Checkpoint struct {
	TimeRate string                   `json:"timeRate"`
	ByteRate string                   `json:"byteRate"`
	MTU      int64                    `json:"mtu"`
	FragGap  int64                    `json:"fragGap"`
	BaseIn   int64                    `json:"baseIn"`
	BaseOut  string                   `json:"baseOut"`
	Streams  map[int]streamCheckpoint `json:"streams"`
}

type streamCheckpoint struct {
	LastIn   int64  `json:"lastIn"`
	ByteAcc  string `json:"byteAcc"`
	ByteEmit int64  `json:"byteEmit"`
	NextMin  int64  `json:"nextMin"`
	PktID    int64  `json:"pktID"`
	Residual string `json:"residual"`
}

// Save writes the scaler state as JSON.
func (s *Scaler) Save(w io.Writer) error {
	cp := Checkpoint{
		TimeRate: s.timeRate.RatString(),
		ByteRate: s.byteRate.RatString(),
		MTU:      s.mtu,
		FragGap:  s.fragGap,
		BaseIn:   s.gBaseIn,
		BaseOut:  s.gBaseOut.RatString(),
		Streams:  make(map[int]streamCheckpoint, len(s.streams)),
	}
	for id, st := range s.streams {
		cp.Streams[id] = streamCheckpoint{
			LastIn:   st.lastIn,
			ByteAcc:  st.byteAcc.RatString(),
			ByteEmit: st.byteEmit,
			NextMin:  st.nextMin,
			PktID:    st.pktID,
			Residual: st.residual.RatString(),
		}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(cp)
}

// Load restores a scaler from a checkpoint written by Save.
func Load(r io.Reader) (*Scaler, error) {
	var cp Checkpoint
	if err := json.NewDecoder(r).Decode(&cp); err != nil {
		return nil, err
	}
	tr, ok := new(big.Rat).SetString(cp.TimeRate)
	if !ok {
		return nil, errBadRat(cp.TimeRate)
	}
	br, ok := new(big.Rat).SetString(cp.ByteRate)
	if !ok {
		return nil, errBadRat(cp.ByteRate)
	}
	s, err := NewScaler(tr, br, cp.MTU, cp.FragGap)
	if err != nil {
		return nil, err
	}
	s.gBaseIn = cp.BaseIn
	if _, ok := s.gBaseOut.SetString(cp.BaseOut); !ok {
		return nil, errBadRat(cp.BaseOut)
	}
	for id, sc := range cp.Streams {
		st := &streamState{
			lastIn:   sc.LastIn,
			byteAcc:  new(big.Rat),
			byteEmit: sc.ByteEmit,
			nextMin:  sc.NextMin,
			pktID:    sc.PktID,
			residual: new(big.Rat),
		}
		if _, ok := st.byteAcc.SetString(sc.ByteAcc); !ok {
			return nil, errBadRat(sc.ByteAcc)
		}
		if _, ok := st.residual.SetString(sc.Residual); !ok {
			return nil, errBadRat(sc.Residual)
		}
		s.streams[id] = st
	}
	return s, nil
}

type ratError string

func errBadRat(v string) error   { return ratError("pktscale: bad rational in checkpoint: " + v) }
func (e ratError) Error() string { return string(e) }
