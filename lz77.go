package deflate

// token is either a literal byte or a (distance, length) back-reference.
type token struct {
	isMatch bool
	lit     byte
	length  uint16
	dist    uint16
}

const (
	windowSize = 32768
	minMatch   = 3
	maxMatch   = 258
	hashBits   = 15
	maxChain   = 128
)

func hash3(d []byte, i int) uint32 {
	v := uint32(d[i]) | uint32(d[i+1])<<8 | uint32(d[i+2])<<16
	return (v * 2654435761) >> (32 - hashBits)
}

// lz77Encode scans data with a sliding-window hash chain matcher and
// emits a stream of literal / match tokens (greedy matching).
func lz77Encode(data []byte) []token {
	tokens := make([]token, 0, len(data)/2+1)
	head := make([]int32, 1<<hashBits)
	for i := range head {
		head[i] = -1
	}
	prev := make([]int32, windowSize)
	n := len(data)

	insert := func(i int) {
		if i+minMatch > n {
			return
		}
		h := hash3(data, i)
		prev[i&(windowSize-1)] = head[h]
		head[h] = int32(i)
	}

	i := 0
	for i < n {
		bestLen, bestDist := 0, 0
		maxLen := n - i
		if maxLen > maxMatch {
			maxLen = maxMatch
		}
		if maxLen >= minMatch {
			h := hash3(data, i)
			chain := maxChain
			for j := int(head[h]); j >= 0 && i-j <= windowSize && chain > 0; chain-- {
				// Quick check on the byte after the current best match.
				if data[j+bestLen] == data[i+bestLen] {
					l := 0
					for l < maxLen && data[j+l] == data[i+l] {
						l++
					}
					if l > bestLen {
						bestLen = l
						bestDist = i - j
						if l >= maxLen {
							break
						}
					}
				}
				nj := int(prev[j&(windowSize-1)])
				if nj >= j {
					break // stale or self-referencing slot
				}
				j = nj
			}
		}
		if bestLen >= minMatch {
			tokens = append(tokens, token{isMatch: true, length: uint16(bestLen), dist: uint16(bestDist)})
			for k := i; k < i+bestLen; k++ {
				insert(k)
			}
			i += bestLen
		} else {
			tokens = append(tokens, token{lit: data[i]})
			insert(i)
			i++
		}
	}
	return tokens
}
