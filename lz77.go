package deflate

const (
	windowSize = 32768
	windowMask = windowSize - 1
	minMatch   = 3
	maxMatch   = 258
	hashBits   = 15
	hashSize   = 1 << hashBits
	maxChain   = 256
	niceLength = 258
)

// token is one LZ77 output unit: either a literal byte or a (distance, length)
// back-reference into the sliding window.
type token struct {
	lit     byte
	length  uint16
	dist    uint16
	isMatch bool
}

func hash3(data []byte, i int) uint32 {
	return (uint32(data[i])<<10 ^ uint32(data[i+1])<<5 ^ uint32(data[i+2])) & (hashSize - 1)
}

// tokenize runs the first compression stage: it scans data with a 32 KiB
// sliding-window hash table and emits literals and (distance, length) pairs.
// Lazy matching (one-position lookahead) is used to improve ratio.
func tokenize(data []byte) []token {
	n := len(data)
	head := make([]int, hashSize)
	for i := range head {
		head[i] = -1
	}
	prev := make([]int, windowSize)
	tokens := make([]token, 0, n/2+1)

	insert := func(i int) {
		if i+minMatch <= n {
			h := hash3(data, i)
			prev[i&windowMask] = head[h]
			head[h] = i
		}
	}

	matchAt := func(i int) (length, dist int) {
		if i+minMatch > n {
			return 0, 0
		}
		maxLen := n - i
		if maxLen > maxMatch {
			maxLen = maxMatch
		}
		best, bestDist := 0, 0
		chain := maxChain
		for cand := head[hash3(data, i)]; cand >= 0 && i-cand <= windowSize && chain > 0; cand = prev[cand&windowMask] {
			if best >= maxLen {
				break
			}
			if best == 0 || data[cand+best] == data[i+best] {
				l := 0
				for l < maxLen && data[cand+l] == data[i+l] {
					l++
				}
				if l > best {
					best, bestDist = l, i-cand
					if l >= niceLength {
						break
					}
				}
			}
			chain--
		}
		if best < minMatch {
			return 0, 0
		}
		return best, bestDist
	}

	emitMatch := func(i, length, dist int) {
		tokens = append(tokens, token{isMatch: true, length: uint16(length), dist: uint16(dist)})
		for j := i; j < i+length; j++ {
			insert(j)
		}
	}

	i := 0
	for i < n {
		length, dist := matchAt(i)
		if length >= minMatch {
			// Lazy step: if the next position yields a longer match,
			// emit the current byte as a literal instead.
			if i+1 < n && length < niceLength {
				insert(i)
				if nlen, ndist := matchAt(i + 1); nlen > length {
					tokens = append(tokens, token{lit: data[i]})
					i++
					emitMatch(i, nlen, ndist)
					i += nlen
					continue
				}
				tokens = append(tokens, token{isMatch: true, length: uint16(length), dist: uint16(dist)})
				for j := i + 1; j < i+length; j++ {
					insert(j)
				}
				i += length
				continue
			}
			emitMatch(i, length, dist)
			i += length
			continue
		}
		tokens = append(tokens, token{lit: data[i]})
		insert(i)
		i++
	}
	return tokens
}
