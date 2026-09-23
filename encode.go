package layerpack

import (
	"hash/crc32"
	"io"
)

// Encode reads the source, applies the given codecs in order (codecs[0]
// first), and writes the layered container to w. Input, output and layer
// budgets are enforced and shared across layers.
func Encode(w io.Writer, r io.Reader, codecs []Codec, lim Limits) error {
	m := newMeter(lim)
	if len(codecs) == 0 || len(codecs) > 255 {
		return ErrFormat
	}
	if err := m.chargeLayers(len(codecs)); err != nil {
		return err
	}
	input, err := readBounded(r, m.inLeft)
	if err != nil {
		return err
	}

	data := input
	recs := make([]record, 0, len(codecs))
	payloads := make([][]byte, 0, len(codecs))
	for _, c := range codecs {
		comp, err := c.compress(data)
		if err != nil {
			return err
		}
		// Charge the shared output budget before keeping the buffer.
		if err := m.chargeOutput(int64(len(comp))); err != nil {
			return err
		}
		recs = append(recs, record{
			codec:   c.ID,
			compLen: uint64(len(comp)),
			rawLen:  uint64(len(data)),
			crc:     crc32.ChecksumIEEE(data),
		})
		payloads = append(payloads, comp)
		data = comp
	}

	hdr := []byte{magic[0], magic[1], magic[2], magic[3], formatVersion, byte(len(codecs))}
	for _, rec := range recs {
		hdr = appendRecord(hdr, rec)
	}
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	for _, p := range payloads {
		if _, err := w.Write(p); err != nil {
			return err
		}
	}
	return nil
}

// readBounded reads at most max+1 bytes and reports ErrBudgetInput when the
// source holds more than max bytes.
func readBounded(r io.Reader, max int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, ErrBudgetInput
	}
	return b, nil
}
