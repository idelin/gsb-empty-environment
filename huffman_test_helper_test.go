package netsec

import (
	"bufio"
	"bytes"
)

func bufioReader(raw []byte) *bufio.Reader {
	return bufio.NewReader(bytes.NewReader(raw))
}

// huffmanEncodeForTest is a bit-exact RFC 7541 Huffman encoder used only by
// tests, exercising the decoder's Huffman path.
func huffmanEncodeForTest(src []byte) []byte {
	var (
		cur   uint64
		nbits uint
		out   []byte
	)
	for _, b := range src {
		code := uint64(huffmanCodes[b])
		length := uint(huffmanCodeLen[b])
		cur = cur<<length | code
		nbits += length
		for nbits >= 8 {
			nbits -= 8
			out = append(out, byte(cur>>nbits))
		}
	}
	if nbits > 0 {
		// Pad the final partial symbol with all-one EOS prefix bits.
		pad := 8 - nbits
		out = append(out, byte(cur<<pad)|byte(1<<pad-1))
	}
	return out
}
