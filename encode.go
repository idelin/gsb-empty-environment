package jpegrotate

import (
	"bytes"
	"io"
)

// encode writes the (possibly transformed) image as a baseline JPEG
// stream, reusing the original Huffman tables and metadata segments.
func (j *jpegImage) encode(w io.Writer) error {
	buf := &bytes.Buffer{}
	buf.Write([]byte{0xFF, 0xD8}) // SOI
	for _, seg := range j.appSegs {
		buf.Write(seg)
	}
	j.writeDQT(buf)
	j.writeSOF(buf)
	for _, seg := range j.dhtRaw {
		buf.Write(seg)
	}
	if j.restartInterval > 0 {
		buf.Write([]byte{0xFF, 0xDD, 0x00, 0x04,
			byte(j.restartInterval >> 8), byte(j.restartInterval)})
	}
	j.writeSOS(buf)
	j.encodeScan(buf)
	buf.Write([]byte{0xFF, 0xD9}) // EOI
	_, err := w.Write(buf.Bytes())
	return err
}

func (j *jpegImage) writeDQT(buf *bytes.Buffer) {
	for tq := 0; tq < 4; tq++ {
		if !j.qtSet[tq] {
			continue
		}
		buf.Write([]byte{0xFF, 0xDB, 0x00, 3 + 64, byte(tq)})
		for k := 0; k < 64; k++ {
			buf.WriteByte(byte(j.qt[tq][zigzag[k]]))
		}
	}
}

func (j *jpegImage) writeSOF(buf *bytes.Buffer) {
	n := len(j.comps)
	buf.Write([]byte{0xFF, j.sofMarker, byte((8 + 3*n) >> 8), byte(8 + 3*n)})
	buf.WriteByte(8) // sample precision
	buf.Write([]byte{byte(j.height >> 8), byte(j.height),
		byte(j.width >> 8), byte(j.width), byte(n)})
	for _, c := range j.comps {
		buf.Write([]byte{byte(c.id), byte(c.h<<4 | c.v), byte(c.tq)})
	}
}

func (j *jpegImage) writeSOS(buf *bytes.Buffer) {
	n := len(j.comps)
	buf.Write([]byte{0xFF, 0xDA, byte((6 + 2*n) >> 8), byte(6 + 2*n), byte(n)})
	for _, c := range j.comps {
		buf.Write([]byte{byte(c.id), byte(c.td<<4 | c.ta)})
	}
	buf.Write([]byte{0, 63, 0}) // Ss, Se, Ah/Al
}
