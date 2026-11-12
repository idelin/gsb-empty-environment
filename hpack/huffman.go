package hpack

import "errors"

// ErrInvalidHuffman is returned when Huffman-encoded data cannot be decoded
// per RFC 7541 section 5.2.
var ErrInvalidHuffman = errors.New("hpack: invalid Huffman-encoded data")

// huffmanNode is one level of the RFC 7541 appendix B decoding trie.
// Internal nodes carry children indexed by the next 8-bit window; leaf
// nodes carry the decoded symbol and its code length.
type huffmanNode struct {
	children *[256]*huffmanNode
	sym      byte
	codeLen  uint8
}

var huffmanRoot = buildHuffmanTrie()

func buildHuffmanTrie() *huffmanNode {
	root := &huffmanNode{children: new([256]*huffmanNode)}
	leaves := new([256]huffmanNode)
	for sym, code := range huffmanCodes {
		codeLen := huffmanCodeLen[sym]
		cur := root
		remaining := codeLen
		for remaining > 8 {
			remaining -= 8
			idx := byte(uint32(code) >> remaining)
			if cur.children[idx] == nil {
				cur.children[idx] = &huffmanNode{children: new([256]*huffmanNode)}
			}
			cur = cur.children[idx]
		}
		shift := 8 - remaining
		start := int(uint8(uint32(code) << shift))
		end := 1 << shift
		leaves[sym].sym = byte(sym)
		leaves[sym].codeLen = remaining
		for i := start; i < start+end; i++ {
			cur.children[i] = &leaves[sym]
		}
	}
	return root
}

// huffmanDecode decodes RFC 7541 appendix B Huffman data. The trie consumes
// 8 bits at a time; a leaf reports how many of those bits belonged to its
// symbol, so partial windows stay in the accumulator. Trailing padding must
// be a prefix of the EOS code (0x3fffffff, 30 bits) and at most 7 bits long.
func huffmanDecode(src []byte) ([]byte, error) {
	dst := make([]byte, 0, len(src))

	node := huffmanRoot
	var cur uint32
	var cbits uint8
	var sbits uint8

	for _, b := range src {
		cur = cur<<8 | uint32(b)
		cbits += 8
		sbits += 8
		for cbits >= 8 {
			next := node.children[byte(cur>>(cbits-8))]
			if next == nil {
				return nil, ErrInvalidHuffman
			}
			if next.children == nil {
				dst = append(dst, next.sym)
				cbits -= next.codeLen
				node = huffmanRoot
				sbits = cbits
			} else {
				cbits -= 8
				node = next
			}
		}
	}

	for cbits > 0 {
		next := node.children[byte(cur<<(8-cbits))]
		if next == nil {
			return nil, ErrInvalidHuffman
		}
		if next.children != nil || next.codeLen > cbits {
			break
		}
		dst = append(dst, next.sym)
		cbits -= next.codeLen
		node = huffmanRoot
		sbits = cbits
	}

	// More than 7 leftover bits means a symbol was truncated. Fewer bits are
	// padding and must all be ones (an EOS-code prefix).
	if sbits > 7 {
		return nil, ErrInvalidHuffman
	}
	if mask := uint32(1<<cbits - 1); cur&mask != mask {
		return nil, ErrInvalidHuffman
	}
	return dst, nil
}

// huffmanEncode appends the Huffman encoding of s to dst, including the EOS
// prefix padding required by RFC 7541 section 5.2.
func huffmanEncode(dst []byte, s []byte) []byte {
	var code uint64
	var bits uint
	for _, b := range s {
		n := uint(huffmanCodeLen[b])
		code = code<<n | uint64(huffmanCodes[b])
		bits += n
		for bits >= 8 {
			bits -= 8
			dst = append(dst, byte(code>>bits))
		}
	}
	if bits > 0 {
		const eosPrefix = 0x3fffffff >> 22 // top 8 bits of the 30-bit EOS code
		pad := 8 - bits
		code = code<<pad | uint64(eosPrefix)>>(8-pad)
		dst = append(dst, byte(code))
	}
	return dst
}

// ExportHuffmanEncode exposes Huffman encoding for tests in other packages.
// It is not intended for production callers.
func ExportHuffmanEncode(dst, src []byte) []byte {
	return huffmanEncode(dst, src)
}
