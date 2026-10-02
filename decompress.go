package deflate

import "errors"

var (
	errCorrupt = errors.New("deflate: corrupt input")
	errEOF     = errors.New("deflate: unexpected end of input")
)

var (
	fixedLitDec  = newHuffmanDecoder(staticLitLengths)
	fixedDistDec = newHuffmanDecoder(staticDistLengths)
)

// Decompress decodes a raw Deflate (RFC 1951) bitstream, handling
// stored (BTYPE=00), static Huffman (01) and dynamic Huffman (10) blocks.
func Decompress(data []byte) ([]byte, error) {
	r := &bitReader{data: data}
	out := make([]byte, 0, len(data)*2)
	for {
		final := r.readBits(1)
		btype := r.readBits(2)
		if r.err != nil {
			return nil, r.err
		}
		var err error
		switch btype {
		case 0:
			err = readStoredBlock(r, &out)
		case 1:
			err = readHuffmanBlock(r, &out, fixedLitDec, fixedDistDec)
		case 2:
			var litDec, distDec *huffmanDecoder
			litDec, distDec, err = readDynamicHeader(r)
			if err == nil {
				err = readHuffmanBlock(r, &out, litDec, distDec)
			}
		default:
			err = errCorrupt
		}
		if err != nil {
			return nil, err
		}
		if final == 1 {
			break
		}
	}
	return out, nil
}

func readStoredBlock(r *bitReader, out *[]byte) error {
	r.alignByte()
	if r.pos+4 > len(r.data) {
		return errEOF
	}
	n := int(r.data[r.pos]) | int(r.data[r.pos+1])<<8
	nlen := int(r.data[r.pos+2]) | int(r.data[r.pos+3])<<8
	r.pos += 4
	if n != nlen^0xffff {
		return errCorrupt
	}
	if r.pos+n > len(r.data) {
		return errEOF
	}
	*out = append(*out, r.data[r.pos:r.pos+n]...)
	r.pos += n
	return nil
}

func readDynamicHeader(r *bitReader) (*huffmanDecoder, *huffmanDecoder, error) {
	numLit := int(r.readBits(5)) + 257
	numDist := int(r.readBits(5)) + 1
	numCL := int(r.readBits(4)) + 4
	if r.err != nil {
		return nil, nil, r.err
	}
	if numLit > 288 || numDist > 32 {
		return nil, nil, errCorrupt
	}
	clLengths := make([]uint8, 19)
	for i := 0; i < numCL; i++ {
		clLengths[clCodeOrder[i]] = uint8(r.readBits(3))
	}
	if r.err != nil {
		return nil, nil, r.err
	}
	clDec := newHuffmanDecoder(clLengths)

	total := numLit + numDist
	lengths := make([]uint8, 0, total)
	for len(lengths) < total {
		sym, err := clDec.decode(r)
		if err != nil {
			return nil, nil, err
		}
		switch {
		case sym < 16:
			lengths = append(lengths, uint8(sym))
		case sym == 16:
			if len(lengths) == 0 {
				return nil, nil, errCorrupt
			}
			prev := lengths[len(lengths)-1]
			rep := 3 + int(r.readBits(2))
			for ; rep > 0; rep-- {
				lengths = append(lengths, prev)
			}
		case sym == 17:
			rep := 3 + int(r.readBits(3))
			lengths = append(lengths, make([]uint8, rep)...)
		case sym == 18:
			rep := 11 + int(r.readBits(7))
			lengths = append(lengths, make([]uint8, rep)...)
		default:
			return nil, nil, errCorrupt
		}
		if len(lengths) > total {
			return nil, nil, errCorrupt
		}
	}
	if r.err != nil {
		return nil, nil, r.err
	}
	if lengths[endOfBlock] == 0 {
		return nil, nil, errCorrupt
	}
	return newHuffmanDecoder(lengths[:numLit]), newHuffmanDecoder(lengths[numLit:]), nil
}

func readHuffmanBlock(r *bitReader, out *[]byte, litDec, distDec *huffmanDecoder) error {
	for {
		sym, err := litDec.decode(r)
		if err != nil {
			return err
		}
		switch {
		case sym < 256:
			*out = append(*out, byte(sym))
		case sym == endOfBlock:
			return nil
		case sym <= 285:
			lc := int(sym) - 257
			length := lengthBase[lc] + int(r.readBits(uint(lengthExtra[lc])))
			dsym, err := distDec.decode(r)
			if err != nil {
				return err
			}
			if dsym > 29 {
				return errCorrupt
			}
			dist := distBase[dsym] + int(r.readBits(uint(distExtra[dsym])))
			if r.err != nil {
				return r.err
			}
			if dist > len(*out) {
				return errCorrupt
			}
			o := *out
			for i := 0; i < length; i++ {
				o = append(o, o[len(o)-dist])
			}
			*out = o
		default:
			return errCorrupt
		}
	}
}
