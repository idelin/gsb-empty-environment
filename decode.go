package layerpack

import (
	"bytes"
	"hash/crc32"
	"io"
)

// Decode reads a container produced by Encode, verifies its version,
// lengths and checksums, and writes the recovered payload to w. All
// budgets are enforced; the output quota bounds every layer's expansion
// and is checked before the layer buffer is allocated.
func Decode(w io.Writer, r io.Reader, lim Limits) error {
	raw, err := readBounded(r, lim.MaxInputBytes)
	if err != nil {
		return err
	}
	if len(raw) < headerLen {
		return ErrFormat
	}
	if !bytes.Equal(raw[:4], magic[:]) {
		return ErrFormat
	}
	if raw[4] != formatVersion {
		return ErrVersion
	}
	n := int(raw[5])
	if n == 0 {
		return ErrFormat
	}
	m := newMeter(lim)
	if err := m.chargeLayers(n); err != nil {
		return err
	}
	if len(raw) < headerLen+n*recordLen {
		return ErrFormat
	}

	recs := make([]record, n)
	off := headerLen
	for i := range recs {
		recs[i] = parseRecord(raw[off : off+recordLen])
		off += recordLen
	}
	payloads := make([][]byte, n)
	for i, rec := range recs {
		if rec.compLen > uint64(len(raw)-off) {
			return ErrFormat
		}
		payloads[i] = raw[off : off+int(rec.compLen)]
		off += int(rec.compLen)
	}
	if off != len(raw) {
		return ErrFormat // trailing bytes: declared lengths must be exact
	}

	var data []byte
	var totalRaw, totalComp int64
	for i := n - 1; i >= 0; i-- {
		rec := recs[i]
		// Shared output budget, charged cumulatively across layers and
		// always before the layer buffer is allocated.
		if err := m.chargeOutput(int64(rec.rawLen)); err != nil {
			return err
		}
		if err := checkRatio(lim, int64(rec.rawLen), int64(rec.compLen)); err != nil {
			return err
		}
		totalRaw += int64(rec.rawLen)
		totalComp += int64(rec.compLen)
		codec, ok := codecByID(rec.codec)
		if !ok {
			return ErrFormat
		}
		var buf bytes.Buffer
		buf.Grow(int(rec.rawLen)) // allocation happens only after the check
		if err := codec.decompressInto(&buf, payloads[i], int64(rec.rawLen)); err != nil {
			return err
		}
		if uint64(buf.Len()) != rec.rawLen {
			return ErrFormat
		}
		if crc32.ChecksumIEEE(buf.Bytes()) != rec.crc {
			return ErrChecksum
		}
		data = buf.Bytes()
	}
	if err := checkRatio(lim, totalRaw, totalComp); err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}
