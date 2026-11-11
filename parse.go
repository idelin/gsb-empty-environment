package jpegrotate

import (
	"errors"
	"fmt"
	"io"
)

// zigzag maps an index in zigzag scan order to the natural (row-major)
// coefficient index.
var zigzag = [64]int{
	0, 1, 8, 16, 9, 2, 3, 10,
	17, 24, 32, 25, 18, 11, 4, 5,
	12, 19, 26, 33, 40, 48, 41, 34,
	27, 20, 13, 6, 7, 14, 21, 28,
	35, 42, 49, 56, 57, 50, 43, 36,
	29, 22, 15, 23, 30, 37, 44, 51,
	58, 59, 52, 45, 38, 31, 39, 46,
	53, 60, 61, 54, 47, 55, 62, 63,
}

// huffTable is a canonical Huffman table as defined by a DHT segment.
type huffTable struct {
	counts [16]uint8 // number of codes of length 1..16
	values []uint8   // symbols in order of increasing code length

	// Decoding acceleration (Annex C decoding procedure).
	mincode, maxcode [16]int32
	valptr           [16]int32

	// Encoding tables: code and length per symbol.
	encCodes [256]uint16
	encSizes [256]uint8
}

func (h *huffTable) build() {
	var code int32
	k := 0
	for l := 0; l < 16; l++ {
		if h.counts[l] == 0 {
			h.maxcode[l] = -1
		} else {
			h.valptr[l] = int32(k)
			h.mincode[l] = code
			k += int(h.counts[l])
			code += int32(h.counts[l])
			h.maxcode[l] = code - 1
		}
		code <<= 1
	}
	code = 0
	k = 0
	for l := 0; l < 16; l++ {
		for i := 0; i < int(h.counts[l]); i++ {
			sym := h.values[k]
			h.encCodes[sym] = uint16(code)
			h.encSizes[sym] = uint8(l + 1)
			code++
			k++
		}
		code <<= 1
	}
}

// parseJPEG parses a baseline JPEG stream and decodes its entropy-coded
// data into quantized DCT coefficients.
func parseJPEG(r io.Reader) (*jpegImage, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return nil, errors.New("jpegrotate: not a JPEG stream (missing SOI)")
	}
	j := &jpegImage{}
	huff := map[int]*huffTable{}
	pos := 2
	scanned := false
	for pos < len(data) {
		if data[pos] != 0xFF {
			return nil, fmt.Errorf("jpegrotate: expected marker at offset %d", pos)
		}
		mstart := pos
		for pos < len(data) && data[pos] == 0xFF {
			pos++
		}
		if pos >= len(data) {
			break
		}
		marker := data[pos]
		pos++
		switch {
		case marker == 0xD8 || marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7):
			// Standalone markers: stray SOI, TEM, RSTn outside a scan.
		case marker == 0xD9: // EOI
			pos = len(data)
		case marker == 0xDA: // SOS
			if scanned {
				return nil, errors.New("jpegrotate: multiple scans are not supported")
			}
			if pos+2 > len(data) {
				return nil, io.ErrUnexpectedEOF
			}
			length := int(data[pos])<<8 | int(data[pos+1])
			if pos+length > len(data) {
				return nil, io.ErrUnexpectedEOF
			}
			if err := j.parseSOS(data[pos:pos+length], huff); err != nil {
				return nil, err
			}
			pos += length
			ent, rst, next, err := scanEntropy(data, pos)
			if err != nil {
				return nil, err
			}
			j.entropy, j.rstCount, pos = ent, rst, next
			scanned = true
		default:
			if pos+2 > len(data) {
				return nil, io.ErrUnexpectedEOF
			}
			length := int(data[pos])<<8 | int(data[pos+1])
			if length < 2 || pos+length > len(data) {
				return nil, io.ErrUnexpectedEOF
			}
			seg := data[pos : pos+length]
			switch marker {
			case 0xDB: // DQT
				if err := j.parseDQT(seg); err != nil {
					return nil, err
				}
			case 0xC4: // DHT
				if err := parseDHT(seg, huff); err != nil {
					return nil, err
				}
				j.dhtRaw = append(j.dhtRaw, data[mstart:pos+length])
			case 0xC0, 0xC1: // SOF0 / SOF1 (baseline & extended sequential)
				if j.sofMarker != 0 {
					return nil, errors.New("jpegrotate: multiple frames are not supported")
				}
				j.sofMarker = marker
				if err := j.parseSOF(seg); err != nil {
					return nil, err
				}
			case 0xC2, 0xC3, 0xC5, 0xC6, 0xC7, 0xC9, 0xCA, 0xCB, 0xCD, 0xCE, 0xCF:
				return nil, fmt.Errorf("jpegrotate: unsupported SOF%d (only baseline sequential is supported)", marker-0xC0)
			case 0xDD: // DRI
				if length < 4 {
					return nil, errors.New("jpegrotate: malformed DRI segment")
				}
				j.restartInterval = int(seg[2])<<8 | int(seg[3])
			default:
				if (marker >= 0xE0 && marker <= 0xEF) || marker == 0xFE {
					// APPn / COM: copied verbatim.
					j.appSegs = append(j.appSegs, data[mstart:pos+length])
				}
			}
			pos += length
		}
	}
	if j.sofMarker == 0 {
		return nil, errors.New("jpegrotate: missing SOF marker")
	}
	if !scanned {
		return nil, errors.New("jpegrotate: missing SOS marker")
	}
	return j, j.decodeScan()
}

// scanEntropy reads entropy-coded data starting at pos, removing byte
// stuffing (0xFF 0x00) and restart markers. It returns the cleaned data,
// the number of restart markers found, and the offset of the next marker.
func scanEntropy(data []byte, pos int) (clean []byte, rstCount, next int, err error) {
	for pos < len(data) {
		b := data[pos]
		if b != 0xFF {
			clean = append(clean, b)
			pos++
			continue
		}
		if pos+1 >= len(data) {
			return nil, 0, 0, io.ErrUnexpectedEOF
		}
		nb := data[pos+1]
		switch {
		case nb == 0x00:
			clean = append(clean, 0xFF)
			pos += 2
		case nb >= 0xD0 && nb <= 0xD7:
			rstCount++
			pos += 2
		case nb == 0xFF:
			pos++ // fill byte
		default:
			return clean, rstCount, pos, nil
		}
	}
	return clean, rstCount, pos, nil
}

func (j *jpegImage) parseDQT(seg []byte) error {
	p := 2 // skip length
	for p < len(seg) {
		pq, tq := seg[p]>>4, seg[p]&15
		p++
		if pq != 0 {
			return errors.New("jpegrotate: 16-bit quantization tables are not supported")
		}
		if tq > 3 {
			return errors.New("jpegrotate: invalid quantization table selector")
		}
		if p+64 > len(seg) {
			return io.ErrUnexpectedEOF
		}
		for i := 0; i < 64; i++ {
			j.qt[tq][zigzag[i]] = int32(seg[p+i])
		}
		j.qtSet[tq] = true
		p += 64
	}
	return nil
}

func parseDHT(seg []byte, tables map[int]*huffTable) error {
	p := 2 // skip length
	for p < len(seg) {
		tc, th := seg[p]>>4, seg[p]&15
		p++
		if tc > 1 || th > 3 {
			return errors.New("jpegrotate: invalid Huffman table class/selector")
		}
		if p+16 > len(seg) {
			return io.ErrUnexpectedEOF
		}
		h := &huffTable{}
		total := 0
		for i := 0; i < 16; i++ {
			h.counts[i] = seg[p+i]
			total += int(seg[p+i])
		}
		p += 16
		if total > 256 || p+total > len(seg) {
			return errors.New("jpegrotate: malformed DHT segment")
		}
		h.values = make([]uint8, total)
		copy(h.values, seg[p:p+total])
		p += total
		h.build()
		tables[int(tc)<<4|int(th)] = h
	}
	return nil
}

func (j *jpegImage) parseSOF(seg []byte) error {
	if len(seg) < 8 {
		return errors.New("jpegrotate: malformed SOF segment")
	}
	if seg[2] != 8 {
		return errors.New("jpegrotate: only 8-bit sample precision is supported")
	}
	j.height = int(seg[3])<<8 | int(seg[4])
	j.width = int(seg[5])<<8 | int(seg[6])
	n := int(seg[7])
	if j.width == 0 || j.height == 0 {
		return errors.New("jpegrotate: zero image dimensions")
	}
	if n < 1 || n > 4 || len(seg) < 8+3*n {
		return errors.New("jpegrotate: unsupported component count")
	}
	sum := 0
	for i := 0; i < n; i++ {
		c := &component{
			id: int(seg[8+3*i]),
			h:  int(seg[9+3*i] >> 4),
			v:  int(seg[9+3*i] & 15),
			tq: int(seg[10+3*i]),
		}
		if c.h < 1 || c.h > 4 || c.v < 1 || c.v > 4 {
			return errors.New("jpegrotate: invalid sampling factors")
		}
		if c.tq > 3 {
			return errors.New("jpegrotate: invalid quantization table selector")
		}
		if c.h > j.hmax {
			j.hmax = c.h
		}
		if c.v > j.vmax {
			j.vmax = c.v
		}
		sum += c.h * c.v
		j.comps = append(j.comps, c)
	}
	if sum > 10 {
		return errors.New("jpegrotate: too many blocks per MCU")
	}
	return nil
}

func (j *jpegImage) parseSOS(seg []byte, tables map[int]*huffTable) error {
	if len(seg) < 3 {
		return errors.New("jpegrotate: malformed SOS segment")
	}
	ns := int(seg[2])
	if ns != len(j.comps) || len(seg) < 3+2*ns+3 {
		return errors.New("jpegrotate: unsupported scan (expect a single scan with all components)")
	}
	for i := 0; i < ns; i++ {
		id := int(seg[3+2*i])
		td, ta := int(seg[4+2*i]>>4), int(seg[4+2*i]&15)
		var c *component
		for _, fc := range j.comps {
			if fc.id == id {
				c = fc
				break
			}
		}
		if c == nil {
			return fmt.Errorf("jpegrotate: scan references unknown component %d", id)
		}
		c.td, c.ta = td, ta
		c.dc = tables[td]
		c.ac = tables[1<<4|ta]
		if c.dc == nil || c.ac == nil {
			return fmt.Errorf("jpegrotate: missing Huffman table for component %d", id)
		}
	}
	ss, se, ahal := seg[3+2*ns], seg[4+2*ns], seg[5+2*ns]
	if ss != 0 || se != 63 || ahal != 0 {
		return errors.New("jpegrotate: unsupported spectral selection (not baseline)")
	}
	for _, c := range j.comps {
		if !j.qtSet[c.tq] {
			return fmt.Errorf("jpegrotate: missing quantization table %d", c.tq)
		}
	}
	return nil
}
