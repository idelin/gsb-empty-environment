// Package reservation reads legacy reservation metadata. Decoding is not authentication.
package reservation

import (
	"bytes"
	"encoding/binary"
	"errors"
)

type Legacy struct {
	Device [8]byte
	Epoch  uint32
	Start  uint32
	Count  uint16
}

func (r Legacy) MarshalBinary() ([]byte, error) {
	if r.Count == 0 || uint64(r.Start)+uint64(r.Count) > 1<<32 {
		return nil, errors.New("invalid reservation interval")
	}
	b := make([]byte, 22)
	copy(b, []byte{'N', 'B', 1, 0})
	copy(b[4:12], r.Device[:])
	binary.LittleEndian.PutUint32(b[12:], r.Epoch)
	binary.LittleEndian.PutUint32(b[16:], r.Start)
	binary.BigEndian.PutUint16(b[20:], r.Count)
	return b, nil
}
func ParseLegacy(b []byte) (Legacy, error) {
	var r Legacy
	if len(b) != 22 || !bytes.Equal(b[:4], []byte{'N', 'B', 1, 0}) {
		return r, errors.New("invalid legacy record")
	}
	copy(r.Device[:], b[4:12])
	r.Epoch = binary.LittleEndian.Uint32(b[12:])
	r.Start = binary.LittleEndian.Uint32(b[16:])
	r.Count = binary.BigEndian.Uint16(b[20:])
	if _, err := r.MarshalBinary(); err != nil {
		return Legacy{}, err
	}
	return r, nil
}
