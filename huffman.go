package netsec

// huffmanDecode expands an RFC 7541 section 5.2 Huffman-encoded byte string.
// The code tables in huffman_table.go reproduce Appendix B of RFC 7541.
func huffmanDecode(src []byte) ([]byte, error) {
	// Worst case expansion is 8 bits per output symbol; over-allocation is
	// bounded by the header list size limit enforced by the caller.
	dst := make([]byte, 0, len(src))

	// cur/cbits hold the bits not yet consumed; sbits counts how many bits the
	// current partial symbol spans, to detect truncated symbols.
	var cur uint32
	var cbits, sbits uint8
	for _, b := range src {
		cur = cur<<8 | uint32(b)
		cbits += 8
		sbits += 8
		for cbits >= 8 {
			idx := byte(cur >> (cbits - 8))
			sym, codeLen, ok := huffmanLookup8(idx)
			if !ok {
				return nil, errHPACKHuffman
			}
			if codeLen == 0 {
				// Internal node: consume 8 bits and keep walking.
				cbits -= 8
				continue
			}
			dst = append(dst, sym)
			cbits -= codeLen
			sbits = cbits
		}
	}

	// Decode the final partial byte.
	for cbits > 0 {
		var idx byte
		if cbits == 8 {
			idx = byte(cur)
		} else {
			idx = byte(cur << (8 - cbits))
		}
		sym, codeLen, ok := huffmanLookup8(idx)
		if !ok || codeLen == 0 || codeLen > cbits {
			break
		}
		dst = append(dst, sym)
		cbits -= codeLen
		sbits = cbits
	}

	// More than 7 dangling bits means a truncated symbol.
	if sbits > 7 {
		return nil, errHPACKHuffman
	}
	// Padding must be the all-ones prefix of EOS (RFC 7541 section 5.2).
	if cbits > 0 {
		mask := uint32(1)<<cbits - 1
		if cur&mask != mask {
			return nil, errHPACKHuffman
		}
	}
	return dst, nil
}

// huffmanNode is a level of the precomputed 8-bit-prefix trie. Internal
// nodes have codeLen == 0 and a non-nil children table.
type huffmanNode struct {
	children *[256]*huffmanNode
	sym      byte
	codeLen  uint8
}

var huffmanRoot = buildHuffmanRoot()

func buildHuffmanRoot() *huffmanNode {
	root := &huffmanNode{children: new([256]*huffmanNode)}
	leaves := new([256]huffmanNode)
	for sym, code := range huffmanCodes {
		codeLen := huffmanCodeLen[sym]
		cur := root
		remaining := codeLen
		for remaining > 8 {
			remaining -= 8
			i := uint8(code >> remaining)
			if cur.children[i] == nil {
				cur.children[i] = &huffmanNode{children: new([256]*huffmanNode)}
			}
			cur = cur.children[i]
		}
		shift := 8 - remaining
		start := int(uint8(code << shift))
		end := 1 << shift
		leaves[sym].sym = byte(sym)
		leaves[sym].codeLen = codeLen
		for i := start; i < start+end; i++ {
			cur.children[i] = &leaves[sym]
		}
	}
	return root
}

// huffmanLookup8 walks one 8-bit stride of the trie. A leaf node returns its
// symbol and full code length; an internal node returns codeLen 0.
func huffmanLookup8(idx byte) (byte, uint8, bool) {
	n := huffmanRoot.children[idx]
	if n == nil {
		return 0, 0, false
	}
	if n.children != nil {
		return 0, 0, true
	}
	return n.sym, n.codeLen, true
}
