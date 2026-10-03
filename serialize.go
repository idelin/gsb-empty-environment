package bitmapckpt

import (
	"encoding/binary"
	"errors"
	"io"
	"math/bits"
)

var errCorrupt = errors.New("bitmapckpt: corrupt container data")

func writeU16(w io.Writer, v uint16) error {
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], v)
	_, err := w.Write(b[:])
	return err
}

func writeU32(w io.Writer, v uint32) error {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	_, err := w.Write(b[:])
	return err
}

func writeU64(w io.Writer, v uint64) error {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	_, err := w.Write(b[:])
	return err
}

func readU8(r io.Reader) (uint8, error) {
	var b [1]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return b[0], nil
}

func readU16(r io.Reader) (uint16, error) {
	var b [2]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(b[:]), nil
}

func readU32(r io.Reader) (uint32, error) {
	var b [4]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b[:]), nil
}

func readU64(r io.Reader) (uint64, error) {
	var b [8]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(b[:]), nil
}

func (a *arrayContainer) writeTo(w io.Writer) error {
	if err := writeU32(w, uint32(len(a.data))); err != nil {
		return err
	}
	for _, v := range a.data {
		if err := writeU16(w, v); err != nil {
			return err
		}
	}
	return nil
}

func (b *bitmapContainer) writeTo(w io.Writer) error {
	var buf [8]byte
	for i := 0; i < bitmapWords; i++ {
		binary.LittleEndian.PutUint64(buf[:], b.words[i])
		if _, err := w.Write(buf[:]); err != nil {
			return err
		}
	}
	return nil
}

func (r *runContainer) writeTo(w io.Writer) error {
	if err := writeU32(w, uint32(len(r.runs))); err != nil {
		return err
	}
	for _, iv := range r.runs {
		if err := writeU16(w, iv.start); err != nil {
			return err
		}
		if err := writeU16(w, iv.last); err != nil {
			return err
		}
	}
	return nil
}

func readContainer(t containerType, r io.Reader) (container, error) {
	switch t {
	case typeArray:
		n, err := readU32(r)
		if err != nil {
			return nil, err
		}
		if n > containerSize {
			return nil, errCorrupt
		}
		a := &arrayContainer{data: make([]uint16, 0, n)}
		for i := uint32(0); i < n; i++ {
			v, err := readU16(r)
			if err != nil {
				return nil, err
			}
			a.data = append(a.data, v)
		}
		return a, nil
	case typeBitmap:
		b := &bitmapContainer{}
		for i := 0; i < bitmapWords; i++ {
			w, err := readU64(r)
			if err != nil {
				return nil, err
			}
			b.words[i] = w
			b.card += bits.OnesCount64(w)
		}
		return b, nil
	case typeRun:
		n, err := readU32(r)
		if err != nil {
			return nil, err
		}
		if n > containerSize {
			return nil, errCorrupt
		}
		rc := &runContainer{runs: make([]interval, 0, n)}
		for i := uint32(0); i < n; i++ {
			s, err := readU16(r)
			if err != nil {
				return nil, err
			}
			l, err := readU16(r)
			if err != nil {
				return nil, err
			}
			if l < s {
				return nil, errCorrupt
			}
			rc.runs = append(rc.runs, interval{s, l})
		}
		rc.card = cardOfRuns(rc.runs)
		return rc, nil
	}
	return nil, errCorrupt
}
