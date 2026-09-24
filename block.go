package secded

import "errors"

// Interleaved block format
// ------------------------
//
// A block groups Depth codewords. Within a block, stream bit t carries
// codeword bit j of word i, where
//
//	t = j*Depth + i      (j in 0..N-1, i in 0..Depth-1)
//
// so the bits of one codeword are spread Depth positions apart. A
// contiguous burst of L stream bits therefore hits each codeword at
// most ceil(L/Depth) times:
//
//	L <= Depth     : <=1 bit per word  -> correctable as single errors
//	L <= 3*Depth   : <=3 bits per word -> recoverable IF the burst is
//	                  marked as erasures (d_min-1 = 3 erasures per word)
//	L >  3*Depth   : beyond capability; affected blocks are reported
//	                  uncorrectable, never silently "repaired".
//
// Blocks are self-contained and start at fixed bit offsets
// (blockIdx * N * Depth), giving O(1) random access to any block.

// DefaultDepth is the default interleave depth (words per block).
// It is a multiple of 8 so every block occupies a whole number of bytes.
const DefaultDepth = 8

// BlockCodec combines a Code with an interleave depth.
type BlockCodec struct {
	Code  *Code
	Depth int
}

// NewBlockCodec builds a BlockCodec; depth must be a positive multiple of 8.
func NewBlockCodec(c *Code, depth int) (*BlockCodec, error) {
	if depth <= 0 || depth%8 != 0 {
		return nil, errors.New("secded: depth must be a positive multiple of 8")
	}
	return &BlockCodec{Code: c, Depth: depth}, nil
}

// BlockDataBits is the number of payload data bits carried per block.
func (b *BlockCodec) BlockDataBits() int { return b.Code.K * b.Depth }

// BlockStreamBits is the number of transmitted stream bits per block.
func (b *BlockCodec) BlockStreamBits() int { return b.Code.N * b.Depth }

// BlockStreamBytes is BlockStreamBits()/8 (always integral: depth%8==0).
func (b *BlockCodec) BlockStreamBytes() int { return b.BlockStreamBits() / 8 }

// StreamBitToWord maps a block-relative stream bit index to its
// (word, 1-indexed codeword position).
func (b *BlockCodec) StreamBitToWord(t int) (word, pos int) {
	return t % b.Depth, t/b.Depth + 1
}

// WordToStreamBit is the inverse of StreamBitToWord.
func (b *BlockCodec) WordToStreamBit(word, pos int) int {
	return (pos-1)*b.Depth + word
}

// EncodeBlock encodes dataBits (BlockDataBits() packed bits, LSB-first)
// into Depth codewords and interleaves them into out at bit offset
// bitOff.
func (b *BlockCodec) EncodeBlock(dataBits []byte, out []byte, bitOff int) {
	k := b.Code.K
	for i := 0; i < b.Depth; i++ {
		var data uint64
		for j := 0; j < k; j++ {
			data |= uint64(getBit(dataBits, i*k+j)) << uint(j)
		}
		cw := b.Code.Encode(data)
		for j := 0; j < b.Code.N; j++ {
			setBit(out, bitOff+(j*b.Depth+i), int(cw>>uint(j)&1))
		}
	}
}

// WordFate records what happened to one codeword during decoding.
type WordFate int

const (
	FateClean WordFate = iota
	FateCorrected
	FateParityOnly
	FateErasureRecovered
	FateUncorrectable
)

func (f WordFate) String() string {
	switch f {
	case FateClean:
		return "clean"
	case FateCorrected:
		return "corrected"
	case FateParityOnly:
		return "parity-bit-only"
	case FateErasureRecovered:
		return "erasure-recovered"
	case FateUncorrectable:
		return "UNCORRECTABLE"
	}
	return "unknown"
}

// WordReport describes the decoding of one codeword.
type WordReport struct {
	Word         int // word index inside the block
	Fate         WordFate
	Erasures     []int // erased codeword positions (1-indexed), if any
	CorrectedPos int   // corrected position, -1 if none
}

// BlockReport describes the decoding of one block.
type BlockReport struct {
	Block int
	Words []WordReport
	OK    bool // false if any word is FateUncorrectable
}

// DecodeBlock decodes one block starting at stream bit offset bitOff.
// erasures lists block-relative stream bit indices known to be bad.
// Data bits are appended (LSB-first) to dataBits, which must have room
// for BlockDataBits() bits; on uncorrectable words the corresponding
// output bits are left zero and reported, never guessed.
func (b *BlockCodec) DecodeBlock(stream []byte, bitOff int, erasures []int, dataBits []byte, dataBitOff int) BlockReport {
	// Deinterleave.
	words := make([]uint64, b.Depth)
	for t := 0; t < b.BlockStreamBits(); t++ {
		w, p := b.StreamBitToWord(t)
		if getBit(stream, bitOff+t) == 1 {
			words[w] |= 1 << uint(p-1)
		}
	}
	// Group erasures per word.
	perWord := make([][]int, b.Depth)
	for _, t := range erasures {
		w, p := b.StreamBitToWord(t)
		perWord[w] = append(perWord[w], p)
	}

	rep := BlockReport{Words: make([]WordReport, b.Depth), OK: true}
	k := b.Code.K
	for i := 0; i < b.Depth; i++ {
		wr := WordReport{Word: i, CorrectedPos: -1, Erasures: perWord[i]}
		var data uint64
		if len(perWord[i]) > 0 {
			d, _, err := b.Code.DecodeWithErasures(words[i], perWord[i])
			if err != nil {
				wr.Fate = FateUncorrectable
			} else {
				wr.Fate = FateErasureRecovered
				data = d
			}
		} else {
			d, st, pos := b.Code.Decode(words[i])
			wr.CorrectedPos = pos
			switch st {
			case StatusClean:
				wr.Fate = FateClean
				data = d
			case StatusCorrected:
				wr.Fate = FateCorrected
				data = d
			case StatusParityOnly:
				wr.Fate = FateParityOnly
				data = d
			case StatusDouble:
				wr.Fate = FateUncorrectable
			}
		}
		if wr.Fate == FateUncorrectable {
			rep.OK = false
		} else {
			for j := 0; j < k; j++ {
				setBit(dataBits, dataBitOff+i*k+j, int(data>>uint(j)&1))
			}
		}
		rep.Words[i] = wr
	}
	return rep
}

// --- packed bit helpers (LSB-first within each byte) ---

func getBit(buf []byte, idx int) int {
	return int(buf[idx/8]>>uint(idx%8)) & 1
}

func setBit(buf []byte, idx, v int) {
	if v == 1 {
		buf[idx/8] |= 1 << uint(idx%8)
	}
}
