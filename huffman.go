package netsec

import (
	"fmt"
	"sync"
)

type huffmanNode struct {
	child [2]*huffmanNode
	sym   byte
	leaf  bool
}

var (
	huffmanRoot     *huffmanNode
	huffmanRootOnce sync.Once
)

func buildHuffmanTree() {
	huffmanRoot = &huffmanNode{}
	for sym := 0; sym < 256; sym++ {
		code := huffmanCodes[sym]
		nbits := huffmanCodeLen[sym]
		n := huffmanRoot
		for bit := int(nbits) - 1; bit >= 0; bit-- {
			v := (code >> uint(bit)) & 1
			if n.child[v] == nil {
				n.child[v] = &huffmanNode{}
			}
			n = n.child[v]
		}
		n.leaf = true
		n.sym = byte(sym)
	}
}

func rootHuffmanNode() *huffmanNode {
	huffmanRootOnce.Do(buildHuffmanTree)
	return huffmanRoot
}

// HuffmanDecodeToString decodes an RFC 7541 Huffman string with no output
// limit.
func HuffmanDecodeToString(v []byte) (string, error) {
	return huffmanDecode(v, 0)
}

// huffmanDecode decodes v and rejects expansion beyond maxLen bytes when
// maxLen is positive. The limit is enforced while writing output so a small
// compressed string cannot force a large allocation.
func huffmanDecode(v []byte, maxLen int) (string, error) {
	root := rootHuffmanNode()
	n := root
	tailLen := 0
	tailBits := 0
	capacity := len(v)
	if capacity < 16 {
		capacity = 16
	}
	if maxLen > 0 && capacity > maxLen {
		capacity = maxLen
	}
	out := make([]byte, 0, capacity)
	for _, b := range v {
		for bit := 7; bit >= 0; bit-- {
			v := int((b >> uint(bit)) & 1)
			tailBits = tailBits<<1 | v
			tailLen++
			n = n.child[v]
			if n == nil {
				return "", ErrInvalidHuffman
			}
			if n.leaf {
				if maxLen > 0 && len(out) >= maxLen {
					return "", fmt.Errorf("%w: Huffman string exceeds %d bytes", ErrLimitExceeded, maxLen)
				}
				out = append(out, n.sym)
				n = root
				tailLen = 0
				tailBits = 0
			}
		}
	}
	if n != root {
		if tailLen > 7 || tailBits != (1<<uint(tailLen))-1 {
			return "", ErrInvalidHuffman
		}
	}
	return string(out), nil
}

// HuffmanEncode appends s encoded as an RFC 7541 Huffman string to dst.
func HuffmanEncode(dst []byte, s string) []byte {
	var bits uint64
	var nbits uint
	for i := 0; i < len(s); i++ {
		code := uint64(huffmanCodes[s[i]])
		length := uint(huffmanCodeLen[s[i]])
		bits = (bits << length) | code
		nbits += length
		for nbits >= 8 {
			nbits -= 8
			dst = append(dst, byte(bits>>nbits))
		}
	}
	if nbits > 0 {
		pad := 8 - nbits
		bits = (bits << pad) | ((1 << pad) - 1)
		dst = append(dst, byte(bits))
	}
	return dst
}

// HuffmanEncodedLength returns the number of bytes needed to Huffman-encode s.
func HuffmanEncodedLength(s string) int {
	var bits uint64
	for i := 0; i < len(s); i++ {
		bits += uint64(huffmanCodeLen[s[i]])
	}
	return int((bits + 7) / 8)
}
