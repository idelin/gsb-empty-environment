// Package deflate implements a lightweight DEFLATE (RFC 1951) compression
// kernel using only the Go standard library. Stage one performs LZ77 parsing
// over a 32 KiB sliding-window hash table; stage two builds Huffman codes for
// the literal/length and distance alphabets and emits a compact bit stream
// with proper block headers (stored, static, or dynamic).
package deflate

// BlockType selects which deflate block encoding the compressor emits.
type BlockType int

const (
	// BlockAuto picks the smallest of stored / static / dynamic per block.
	BlockAuto BlockType = iota
	// BlockStored forces uncompressed (stored) blocks.
	BlockStored
	// BlockStatic forces fixed-Huffman blocks.
	BlockStatic
	// BlockDynamic forces dynamic-Huffman blocks.
	BlockDynamic
)

// Options configures the compressor.
type Options struct {
	BlockType BlockType
}

// maxBlockTokens bounds the number of LZ77 tokens per deflate block.
const maxBlockTokens = 1 << 16

// maxStoredChunk is the payload limit of one stored block (LEN is 16 bits).
const maxStoredChunk = 65535

// Compress returns data compressed with the default options (BlockAuto).
func Compress(data []byte) []byte {
	return CompressWithOptions(data, Options{})
}

// CompressWithOptions compresses data according to opt.
func CompressWithOptions(data []byte, opt Options) []byte {
	w := &bitWriter{}
	if len(data) == 0 {
		writeBlock(w, nil, nil, true, opt)
		return w.finish()
	}
	tokens := tokenize(data)
	start, byteStart, byteOff := 0, 0, 0
	for i, t := range tokens {
		if i-start >= maxBlockTokens {
			writeBlock(w, tokens[start:i], data[byteStart:byteOff], false, opt)
			start, byteStart = i, byteOff
		}
		if t.isMatch {
			byteOff += int(t.length)
		} else {
			byteOff++
		}
	}
	writeBlock(w, tokens[start:], data[byteStart:], true, opt)
	return w.finish()
}

// writeBlock emits one deflate block covering tokens (raw holds the same
// input span, used when a stored block wins or is forced).
func writeBlock(w *bitWriter, tokens []token, raw []byte, final bool, opt Options) {
	bt := opt.BlockType
	if bt == BlockAuto {
		bt = chooseBlockType(w, tokens, raw)
	}
	switch bt {
	case BlockStored:
		writeStored(w, raw, final)
	case BlockStatic:
		writeStatic(w, tokens, final)
	default:
		writeDynamic(w, tokens, final)
	}
}

// chooseBlockType estimates the encoded size of each block type and picks
// the smallest. Static and dynamic costs are measured exactly by encoding
// into throwaway writers; the stored cost is computed analytically because
// its byte alignment depends on the live writer state.
func chooseBlockType(w *bitWriter, tokens []token, raw []byte) BlockType {
	stored := storedCost(int(w.nbits), len(raw))
	ts := &bitWriter{}
	writeStatic(ts, tokens, false)
	td := &bitWriter{}
	writeDynamic(td, tokens, false)

	best, bt := stored, BlockStored
	if ts.bitLen() < best {
		best, bt = ts.bitLen(), BlockStatic
	}
	if td.bitLen() < best {
		bt = BlockDynamic
	}
	return bt
}

// storedCost returns the exact bit cost of encoding nbytes as stored blocks,
// given the writer currently has curBits bits pending in its partial byte.
func storedCost(curBits, nbytes int) int {
	total := 0
	first := true
	for first || nbytes > 0 {
		chunk := nbytes
		if chunk > maxStoredChunk {
			chunk = maxStoredChunk
		}
		pad := 5 // after a stored block the stream is byte-aligned: 3 header bits + 5 pad
		if first {
			pad = (8 - (curBits+3)%8) % 8
		}
		total += 3 + pad + 32 + chunk*8
		nbytes -= chunk
		first = false
	}
	return total
}

func writeHeader(w *bitWriter, final bool, btype uint32) {
	var b uint32
	if final {
		b = 1
	}
	w.writeBits(b, 1)
	w.writeBits(btype, 2)
}

// writeStored emits one or more stored (BTYPE=00) blocks.
func writeStored(w *bitWriter, raw []byte, final bool) {
	if len(raw) == 0 {
		writeStoredChunk(w, nil, final)
		return
	}
	for len(raw) > 0 {
		n := len(raw)
		if n > maxStoredChunk {
			n = maxStoredChunk
		}
		writeStoredChunk(w, raw[:n], final && n == len(raw))
		raw = raw[n:]
	}
}

func writeStoredChunk(w *bitWriter, raw []byte, final bool) {
	writeHeader(w, final, 0)
	w.alignByte()
	n := len(raw)
	w.out = append(w.out, byte(n), byte(n>>8), ^byte(n), ^byte(n>>8))
	w.out = append(w.out, raw...)
}

var (
	staticLitEnc  = newHuffEncoder(fixedLitLenLengths())
	staticDistEnc = newHuffEncoder(fixedDistLengths())
)

// writeStatic emits a fixed-Huffman (BTYPE=01) block.
func writeStatic(w *bitWriter, tokens []token, final bool) {
	writeHeader(w, final, 1)
	writeTokens(w, tokens, staticLitEnc, staticDistEnc)
}

// writeDynamic emits a dynamic-Huffman (BTYPE=10) block, building fresh
// Huffman codes from the token statistics.
func writeDynamic(w *bitWriter, tokens []token, final bool) {
	var litFreq [286]uint32
	var distFreq [30]uint32
	litFreq[256] = 1 // end-of-block
	for _, t := range tokens {
		if t.isMatch {
			litFreq[257+int(lengthCodeTable[t.length])]++
			distFreq[distCodeIndex(int(t.dist))]++
		} else {
			litFreq[t.lit]++
		}
	}
	litLens := buildCodeLengths(litFreq[:], maxCodeBits)
	distLens := buildCodeLengths(distFreq[:], maxCodeBits)
	writeHeader(w, final, 2)
	writeDynamicHeader(w, litLens, distLens)
	writeTokens(w, tokens, newHuffEncoder(litLens), newHuffEncoder(distLens))
}

// rleSym is one symbol of the run-length encoded code-length sequence.
type rleSym struct {
	sym       int   // 0..18
	extra     uint8 // extra bits payload for symbols 16/17/18
	extraBits uint8
}

// rleEncodeLengths compresses a code-length sequence using the special
// repeat symbols 16 (copy previous 3-6), 17 (zeros 3-10) and 18 (zeros 11-138).
func rleEncodeLengths(lens []uint8) []rleSym {
	var out []rleSym
	i := 0
	for i < len(lens) {
		v := lens[i]
		j := i
		for j < len(lens) && lens[j] == v {
			j++
		}
		run := j - i
		if v == 0 {
			for run >= 11 {
				cnt := run
				if cnt > 138 {
					cnt = 138
				}
				out = append(out, rleSym{sym: 18, extra: uint8(cnt - 11), extraBits: 7})
				run -= cnt
			}
			if run >= 3 {
				out = append(out, rleSym{sym: 17, extra: uint8(run - 3), extraBits: 3})
				run = 0
			}
			for ; run > 0; run-- {
				out = append(out, rleSym{sym: 0})
			}
		} else {
			out = append(out, rleSym{sym: int(v)})
			run--
			for run >= 3 {
				cnt := run
				if cnt > 6 {
					cnt = 6
				}
				out = append(out, rleSym{sym: 16, extra: uint8(cnt - 3), extraBits: 2})
				run -= cnt
			}
			for ; run > 0; run-- {
				out = append(out, rleSym{sym: int(v)})
			}
		}
		i = j
	}
	return out
}

// writeDynamicHeader writes HLIT, HDIST, HCLEN, the code-length code, and the
// run-length encoded code lengths (RFC 1951 3.2.7).
func writeDynamicHeader(w *bitWriter, litLens, distLens []uint8) {
	hlit := len(litLens)
	for hlit > 257 && litLens[hlit-1] == 0 {
		hlit--
	}
	hdist := len(distLens)
	for hdist > 1 && distLens[hdist-1] == 0 {
		hdist--
	}

	all := make([]uint8, 0, hlit+hdist)
	all = append(all, litLens[:hlit]...)
	all = append(all, distLens[:hdist]...)
	syms := rleEncodeLengths(all)

	var clFreq [19]uint32
	for _, s := range syms {
		clFreq[s.sym]++
	}
	clLens := buildCodeLengths(clFreq[:], 7)

	hclen := 19
	for hclen > 4 && clLens[clOrder[hclen-1]] == 0 {
		hclen--
	}

	w.writeBits(uint32(hlit-257), 5)
	w.writeBits(uint32(hdist-1), 5)
	w.writeBits(uint32(hclen-4), 4)
	for i := 0; i < hclen; i++ {
		w.writeBits(uint32(clLens[clOrder[i]]), 3)
	}
	clEnc := newHuffEncoder(clLens)
	for _, s := range syms {
		clEnc.write(w, s.sym)
		if s.extraBits > 0 {
			w.writeBits(uint32(s.extra), uint(s.extraBits))
		}
	}
}

// writeTokens emits the LZ77 token stream followed by the end-of-block code.
func writeTokens(w *bitWriter, tokens []token, litEnc, distEnc huffEncoder) {
	for _, t := range tokens {
		if t.isMatch {
			li := lengthCodeTable[t.length]
			litEnc.write(w, 257+int(li))
			if eb := lengthExtra[li]; eb > 0 {
				w.writeBits(uint32(int(t.length)-int(lengthBase[li])), uint(eb))
			}
			di := distCodeIndex(int(t.dist))
			distEnc.write(w, di)
			if db := distExtra[di]; db > 0 {
				w.writeBits(uint32(int(t.dist)-int(distBase[di])), uint(db))
			}
		} else {
			litEnc.write(w, int(t.lit))
		}
	}
	litEnc.write(w, 256) // end of block
}
