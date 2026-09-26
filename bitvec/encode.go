package bitvec

import (
	"encoding/binary"
	"hash/crc32"
	"math/bits"
)

// Encode compresses vec into the serialized bitmap format. vec[i] is
// the value of bit i; the bitmap length is len(bits), and any tail
// capacity in the final block is padded with zeros that never
// participate in queries.
func Encode(vec []bool) []byte {
	bitLen := uint64(len(vec))
	numBlocks := uint32((bitLen + BlockBits - 1) / BlockBits)
	numSupers := (numBlocks + BlocksPerSuper - 1) / BlocksPerSuper

	dir := make([]byte, uint64(numBlocks)*dirEntrySize)
	pops := make([]uint64, numBlocks)
	var data []byte
	var total uint64

	var words [BlockBits / 64]uint64
	for b := uint32(0); b < numBlocks; b++ {
		base := uint64(b) * BlockBits
		for w := range words {
			words[w] = 0
		}
		// Load valid bits only; tail padding stays zero.
		end := base + BlockBits
		if end > bitLen {
			end = bitLen
		}
		for i := base; i < end; i++ {
			if vec[i] {
				words[(i-base)/64] |= 1 << ((i - base) % 64)
			}
		}
		pop := 0
		for _, w := range words {
			pop += bits2ones(w)
		}
		pops[b] = uint64(pop)
		total += uint64(pop)

		entry := dir[uint64(b)*dirEntrySize:]
		binary.LittleEndian.PutUint16(entry[1:3], uint16(pop))
		off := uint32(len(data))
		switch {
		case pop == 0:
			entry[0] = blkZero
		case pop == BlockBits:
			entry[0] = blkOne
		case pop <= sparseMax:
			entry[0] = blkSparse
			data = append(data, byte(pop))
			for w := 0; w < BlockBits/64; w++ {
				for x := words[w]; x != 0; x &= x - 1 {
					pos := uint16(w*64 + bits.TrailingZeros64(x))
					data = binary.LittleEndian.AppendUint16(data, pos)
				}
			}
		default:
			entry[0] = blkRaw
			for _, w := range words {
				data = binary.LittleEndian.AppendUint64(data, w)
			}
		}
		binary.LittleEndian.PutUint32(entry[4:8], off)
	}

	// Rank superblock index: cumulative ones before each superblock.
	numSamples := uint32(0)
	if total > 0 {
		numSamples = uint32((total + SelectRate - 1) / SelectRate)
	}
	super := make([]byte, uint64(numSupers)*superEntrySize)
	sel := make([]byte, uint64(numSamples)*sampleEntrySize)
	var cum uint64
	nextTarget := uint64(0)
	si := 0
	for b := uint32(0); b < numBlocks; b++ {
		if b%BlocksPerSuper == 0 {
			binary.LittleEndian.PutUint64(super[uint64(b/BlocksPerSuper)*superEntrySize:], cum)
		}
		for nextTarget < cum+pops[b] && si < int(numSamples) {
			e := sel[uint64(si)*sampleEntrySize:]
			binary.LittleEndian.PutUint32(e[0:4], b)
			binary.LittleEndian.PutUint64(e[8:16], cum)
			si++
			nextTarget += SelectRate
		}
		cum += pops[b]
	}

	// Assemble sections at deterministic offsets.
	dirOff := uint64(headerSize)
	superOff := dirOff + uint64(len(dir))
	selOff := superOff + uint64(len(super))
	dataOff := selOff + uint64(len(sel))
	totalLen := dataOff + uint64(len(data))

	out := make([]byte, 0, totalLen)
	out = append(out, make([]byte, headerSize)...)
	out = append(out, dir...)
	out = append(out, super...)
	out = append(out, sel...)
	out = append(out, data...)

	h := out[:headerSize]
	copy(h[0:4], magic[:])
	binary.LittleEndian.PutUint32(h[4:8], Version)
	binary.LittleEndian.PutUint64(h[8:16], bitLen)
	binary.LittleEndian.PutUint32(h[16:20], numBlocks)
	binary.LittleEndian.PutUint32(h[20:24], numSupers)
	binary.LittleEndian.PutUint32(h[24:28], BlocksPerSuper)
	binary.LittleEndian.PutUint32(h[28:32], SelectRate)
	binary.LittleEndian.PutUint64(h[32:40], total)
	binary.LittleEndian.PutUint32(h[40:44], numSamples)
	// h[44:48] flags, reserved zero
	binary.LittleEndian.PutUint64(h[48:56], dirOff)
	binary.LittleEndian.PutUint64(h[56:64], superOff)
	binary.LittleEndian.PutUint64(h[64:72], selOff)
	binary.LittleEndian.PutUint64(h[72:80], dataOff)
	binary.LittleEndian.PutUint64(h[80:88], uint64(len(data)))
	binary.LittleEndian.PutUint64(h[88:96], totalLen)
	binary.LittleEndian.PutUint32(h[96:100], crc32.ChecksumIEEE(out[dirOff:superOff]))
	binary.LittleEndian.PutUint32(h[100:104], crc32.ChecksumIEEE(out[superOff:selOff]))
	binary.LittleEndian.PutUint32(h[104:108], crc32.ChecksumIEEE(out[selOff:dataOff]))
	binary.LittleEndian.PutUint32(h[108:112], crc32.ChecksumIEEE(out[dataOff:]))
	binary.LittleEndian.PutUint32(h[112:116], crc32.ChecksumIEEE(h[:112]))
	return out
}

func bits2ones(w uint64) int { return bits.OnesCount64(w) }
