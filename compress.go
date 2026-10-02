package deflate

// blockSize is the LZ77 block granularity; it also keeps stored blocks
// within the 65535-byte LEN limit.
const blockSize = 65535

// Compress encodes data as a raw Deflate (RFC 1951) bitstream using a
// two-stage pipeline: LZ77 over a 32 KiB sliding window, then Huffman
// coding. Each block picks the smallest of stored / static / dynamic
// encodings.
func Compress(data []byte) []byte {
	var w bitWriter
	if len(data) == 0 {
		w.writeBits(1, 1) // BFINAL
		w.writeBits(0, 2) // stored
		w.alignByte()
		w.writeBytes([]byte{0x00, 0x00, 0xff, 0xff})
		return w.bytes()
	}
	for start := 0; start < len(data); start += blockSize {
		end := start + blockSize
		if end > len(data) {
			end = len(data)
		}
		compressBlock(&w, data[start:end], end == len(data))
	}
	return w.bytes()
}

func compressBlock(w *bitWriter, block []byte, final bool) {
	tokens := lz77Encode(block)

	var litFreq [maxLitCodes]uint32
	var distFreq [maxDistCodes]uint32
	distUsed := false
	for _, t := range tokens {
		if t.isMatch {
			litFreq[endOfBlock+1+int(lengthCodeOf[t.length])]++
			distFreq[distCodeOf(int(t.dist))]++
			distUsed = true
		} else {
			litFreq[t.lit]++
		}
	}
	litFreq[endOfBlock]++

	dynLit := buildCodeLengths(litFreq[:], maxCodeBits)
	dynDist := buildCodeLengths(distFreq[:], maxCodeBits)
	if !distUsed {
		// Emit one dummy distance code so the table is never empty.
		dynDist[0] = 1
	}

	numLit := maxLitCodes
	for numLit > 257 && dynLit[numLit-1] == 0 {
		numLit--
	}
	numDist := maxDistCodes
	for numDist > 1 && dynDist[numDist-1] == 0 {
		numDist--
	}

	combined := make([]uint8, 0, numLit+numDist)
	combined = append(combined, dynLit[:numLit]...)
	combined = append(combined, dynDist[:numDist]...)
	ops := rleEncodeLengths(combined)

	var clFreq [19]uint32
	for _, op := range ops {
		clFreq[op.code]++
	}
	clLengths := buildCodeLengths(clFreq[:], 7)
	numCL := 19
	for numCL > 4 && clLengths[clCodeOrder[numCL-1]] == 0 {
		numCL--
	}

	headerBits := 5 + 5 + 4 + 3*numCL
	for _, op := range ops {
		headerBits += int(clLengths[op.code]) + int(op.extraBits)
	}

	dynTotal := 3 + headerBits + tokensBitSize(tokens, dynLit, dynDist)
	staticTotal := 3 + tokensBitSize(tokens, staticLitLengths, staticDistLengths)
	storedTotal := 3 + 7 + 32 + 8*len(block) // upper bound incl. padding

	switch {
	case storedTotal <= staticTotal && storedTotal <= dynTotal:
		w.writeBits(boolBit(final), 1)
		w.writeBits(0, 2)
		w.alignByte()
		n := len(block)
		w.writeBytes([]byte{byte(n), byte(n >> 8), byte(^n), byte(^n >> 8)})
		w.writeBytes(block)
	case staticTotal <= dynTotal:
		w.writeBits(boolBit(final), 1)
		w.writeBits(1, 2)
		writeTokens(w, tokens, staticLitLengths, staticLitCodes, staticDistLengths, staticDistCodes)
	default:
		w.writeBits(boolBit(final), 1)
		w.writeBits(2, 2)
		w.writeBits(uint32(numLit-257), 5)
		w.writeBits(uint32(numDist-1), 5)
		w.writeBits(uint32(numCL-4), 4)
		for i := 0; i < numCL; i++ {
			w.writeBits(uint32(clLengths[clCodeOrder[i]]), 3)
		}
		clCodes := canonicalCodes(clLengths)
		for _, op := range ops {
			w.writeCode(clCodes[op.code], clLengths[op.code])
			if op.extraBits > 0 {
				w.writeBits(uint32(op.extra), uint(op.extraBits))
			}
		}
		writeTokens(w, tokens, dynLit, canonicalCodes(dynLit), dynDist, canonicalCodes(dynDist))
	}
}

func boolBit(b bool) uint32 {
	if b {
		return 1
	}
	return 0
}

// tokensBitSize estimates the payload size of tokens under the given
// code lengths, including extra bits and the end-of-block symbol.
func tokensBitSize(tokens []token, litLen, distLen []uint8) int {
	bits := 0
	for _, t := range tokens {
		if t.isMatch {
			lc := int(lengthCodeOf[t.length])
			bits += int(litLen[endOfBlock+1+lc]) + int(lengthExtra[lc])
			dc := distCodeOf(int(t.dist))
			bits += int(distLen[dc]) + int(distExtra[dc])
		} else {
			bits += int(litLen[t.lit])
		}
	}
	bits += int(litLen[endOfBlock])
	return bits
}

func writeTokens(w *bitWriter, tokens []token, litLen []uint8, litCodes []uint16, distLen []uint8, distCodes []uint16) {
	for _, t := range tokens {
		if t.isMatch {
			lc := int(lengthCodeOf[t.length])
			sym := endOfBlock + 1 + lc
			w.writeCode(litCodes[sym], litLen[sym])
			if lengthExtra[lc] > 0 {
				w.writeBits(uint32(int(t.length)-lengthBase[lc]), uint(lengthExtra[lc]))
			}
			dc := distCodeOf(int(t.dist))
			w.writeCode(distCodes[dc], distLen[dc])
			if distExtra[dc] > 0 {
				w.writeBits(uint32(int(t.dist)-distBase[dc]), uint(distExtra[dc]))
			}
		} else {
			w.writeCode(litCodes[t.lit], litLen[t.lit])
		}
	}
	w.writeCode(litCodes[endOfBlock], litLen[endOfBlock])
}

// rleOp is one run-length operation in a dynamic block header.
type rleOp struct {
	code      uint8 // 0..18
	extra     uint8
	extraBits uint8
}

// rleEncodeLengths compresses a code-length sequence using the
// 16 (repeat prev 3-6), 17 (zeros 3-10) and 18 (zeros 11-138) opcodes.
func rleEncodeLengths(lengths []uint8) []rleOp {
	var ops []rleOp
	i := 0
	for i < len(lengths) {
		v := lengths[i]
		run := 1
		for i+run < len(lengths) && lengths[i+run] == v {
			run++
		}
		i += run
		if v == 0 {
			for run >= 11 {
				n := run
				if n > 138 {
					n = 138
				}
				ops = append(ops, rleOp{18, uint8(n - 11), 7})
				run -= n
			}
			if run >= 3 {
				ops = append(ops, rleOp{17, uint8(run - 3), 3})
				run = 0
			}
			for ; run > 0; run-- {
				ops = append(ops, rleOp{0, 0, 0})
			}
		} else {
			ops = append(ops, rleOp{v, 0, 0})
			run--
			for run >= 3 {
				n := run
				if n > 6 {
					n = 6
				}
				ops = append(ops, rleOp{16, uint8(n - 3), 2})
				run -= n
			}
			for ; run > 0; run-- {
				ops = append(ops, rleOp{v, 0, 0})
			}
		}
	}
	return ops
}
