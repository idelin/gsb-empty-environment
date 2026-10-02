package deflate

import "errors"

var (
	errUnexpectedEOF = errors.New("deflate: unexpected end of input")
	errInvalidCode   = errors.New("deflate: invalid huffman code")
	errInvalidBlock  = errors.New("deflate: invalid block type")
	errInvalidStored = errors.New("deflate: invalid stored block length")
	errInvalidDist   = errors.New("deflate: invalid distance")
)

// Decompress inflates a deflate stream produced by Compress (or any other
// RFC 1951 compliant compressor). Stored, fixed-Huffman and dynamic-Huffman
// blocks are all recognized and decoded.
func Decompress(data []byte) ([]byte, error) {
	return inflate(data, nil)
}

// BlockTypes parses the stream and returns the BTYPE (0=stored, 1=static,
// 2=dynamic) of every block, in order. It fully inflates the stream, so a
// corrupt stream yields the blocks seen so far plus an error.
func BlockTypes(data []byte) ([]int, error) {
	var types []int
	_, err := inflate(data, &types)
	return types, err
}

var (
	fixedLitDec, fixedLitErr   = newHuffDecoder(fixedLitLenLengths())
	fixedDistDec, fixedDistErr = newHuffDecoder(fixedDistLengths())
)

func inflate(data []byte, trace *[]int) ([]byte, error) {
	r := &bitReader{data: data}
	var out []byte
	for {
		bfinal, err := r.readBits(1)
		if err != nil {
			return nil, err
		}
		btype, err := r.readBits(2)
		if err != nil {
			return nil, err
		}
		if trace != nil {
			*trace = append(*trace, int(btype))
		}
		switch btype {
		case 0:
			out, err = readStored(r, out)
		case 1:
			if fixedLitErr != nil || fixedDistErr != nil {
				return nil, errInvalidCode
			}
			out, err = readCompressed(r, out, fixedLitDec, fixedDistDec)
		case 2:
			var litDec, distDec *huffDecoder
			litDec, distDec, err = readDynamicHeader(r)
			if err == nil {
				out, err = readCompressed(r, out, litDec, distDec)
			}
		default:
			return nil, errInvalidBlock
		}
		if err != nil {
			return nil, err
		}
		if bfinal == 1 {
			return out, nil
		}
	}
}

// readStored decodes a stored (uncompressed) block: LEN/NLEN header followed
// by LEN literal bytes.
func readStored(r *bitReader, out []byte) ([]byte, error) {
	r.alignByte()
	if r.pos+4 > len(r.data) {
		return nil, errUnexpectedEOF
	}
	n := int(r.data[r.pos]) | int(r.data[r.pos+1])<<8
	nlen := int(r.data[r.pos+2]) | int(r.data[r.pos+3])<<8
	r.pos += 4
	if n != nlen^0xFFFF {
		return nil, errInvalidStored
	}
	if r.pos+n > len(r.data) {
		return nil, errUnexpectedEOF
	}
	out = append(out, r.data[r.pos:r.pos+n]...)
	r.pos += n
	return out, nil
}

// readDynamicHeader parses the dynamic block header and builds the two
// Huffman decoders.
func readDynamicHeader(r *bitReader) (*huffDecoder, *huffDecoder, error) {
	hlit, err := r.readBits(5)
	if err != nil {
		return nil, nil, err
	}
	hdist, err := r.readBits(5)
	if err != nil {
		return nil, nil, err
	}
	hclen, err := r.readBits(4)
	if err != nil {
		return nil, nil, err
	}
	hlit += 257
	hdist++
	hclen += 4

	var clLens [19]uint8
	for i := uint32(0); i < hclen; i++ {
		v, err := r.readBits(3)
		if err != nil {
			return nil, nil, err
		}
		clLens[clOrder[i]] = uint8(v)
	}
	clDec, err := newHuffDecoder(clLens[:])
	if err != nil {
		return nil, nil, err
	}

	total := int(hlit + hdist)
	lens := make([]uint8, 0, total)
	for len(lens) < total {
		sym, err := clDec.decode(r)
		if err != nil {
			return nil, nil, err
		}
		switch {
		case sym < 16:
			lens = append(lens, uint8(sym))
		case sym == 16:
			if len(lens) == 0 {
				return nil, nil, errInvalidCode
			}
			rep, err := r.readBits(2)
			if err != nil {
				return nil, nil, err
			}
			prev := lens[len(lens)-1]
			for k := uint32(0); k < rep+3; k++ {
				lens = append(lens, prev)
			}
		case sym == 17:
			rep, err := r.readBits(3)
			if err != nil {
				return nil, nil, err
			}
			for k := uint32(0); k < rep+3; k++ {
				lens = append(lens, 0)
			}
		default: // sym == 18
			rep, err := r.readBits(7)
			if err != nil {
				return nil, nil, err
			}
			for k := uint32(0); k < rep+11; k++ {
				lens = append(lens, 0)
			}
		}
		if len(lens) > total {
			return nil, nil, errInvalidCode
		}
	}
	litLens := lens[:hlit]
	distLens := lens[hlit:]
	if litLens[256] == 0 {
		return nil, nil, errInvalidCode // no end-of-block code
	}
	litDec, err := newHuffDecoder(litLens)
	if err != nil {
		return nil, nil, err
	}
	distDec, err := newHuffDecoder(distLens)
	if err != nil {
		return nil, nil, err
	}
	return litDec, distDec, nil
}

// readCompressed decodes the symbol stream of one Huffman block until the
// end-of-block code (256).
func readCompressed(r *bitReader, out []byte, litDec, distDec *huffDecoder) ([]byte, error) {
	for {
		sym, err := litDec.decode(r)
		if err != nil {
			return nil, err
		}
		switch {
		case sym < 256:
			out = append(out, byte(sym))
		case sym == 256:
			return out, nil
		default:
			li := int(sym) - 257
			if li >= 29 {
				return nil, errInvalidCode
			}
			length := int(lengthBase[li])
			if eb := lengthExtra[li]; eb > 0 {
				v, err := r.readBits(uint(eb))
				if err != nil {
					return nil, err
				}
				length += int(v)
			}
			dsym, err := distDec.decode(r)
			if err != nil {
				return nil, err
			}
			if dsym >= 30 {
				return nil, errInvalidDist
			}
			dist := int(distBase[dsym])
			if db := distExtra[dsym]; db > 0 {
				v, err := r.readBits(uint(db))
				if err != nil {
					return nil, err
				}
				dist += int(v)
			}
			if dist > len(out) {
				return nil, errInvalidDist
			}
			// Byte-by-byte copy handles overlapping matches (dist < length).
			for k := 0; k < length; k++ {
				out = append(out, out[len(out)-dist])
			}
		}
	}
}
