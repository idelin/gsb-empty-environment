package netsec

// Compression format defined by the task: scan left to right; at each
// position search the previous at most WindowSize bytes for the longest
// non-overlapping match of length MinMatch..MaxMatch; ties on length are
// broken by nearest distance. A match costs MatchCost bytes, otherwise a
// literal costs LiteralCost byte.

const (
	WindowSize  = 32
	MinMatch    = 3
	MaxMatch    = 15
	LiteralCost = 1
	MatchCost   = 2

	// Separator sits between the secret candidate and the attacker query.
	Separator byte = 0xFF
)

// Token is one unit of the compressed stream: either a literal byte
// (Length == 0) or a back-reference match.
type Token struct {
	Literal  byte // valid when Length == 0
	Length   int  // 0 for a literal, otherwise MinMatch..MaxMatch
	Distance int  // valid when Length > 0
}

// IsMatch reports whether the token is a back-reference match.
func (t Token) IsMatch() bool { return t.Length > 0 }

// Cost is the encoded size of the token in bytes.
func (t Token) Cost() int {
	if t.IsMatch() {
		return MatchCost
	}
	return LiteralCost
}

// Parse compresses data and returns the token stream.
func Parse(data []byte) []Token {
	var tokens []Token
	for i := 0; i < len(data); {
		bestLen, bestDist := 0, 0
		start := i - WindowSize
		if start < 0 {
			start = 0
		}
		limit := len(data) - i
		if limit > MaxMatch {
			limit = MaxMatch
		}
		// Scan nearest distance first; the strict ">" below keeps the
		// nearest distance when lengths tie.
		for j := i - 1; j >= start; j-- {
			dist := i - j
			maxLen := limit
			if dist < maxLen {
				maxLen = dist // match must not overlap the current position
			}
			l := 0
			for l < maxLen && data[j+l] == data[i+l] {
				l++
			}
			if l >= MinMatch && l > bestLen {
				bestLen, bestDist = l, dist
			}
		}
		if bestLen >= MinMatch {
			tokens = append(tokens, Token{Length: bestLen, Distance: bestDist})
			i += bestLen
		} else {
			tokens = append(tokens, Token{Literal: data[i]})
			i++
		}
	}
	return tokens
}

// CompressLen returns the compressed length of data in bytes.
func CompressLen(data []byte) int {
	n := 0
	for _, t := range Parse(data) {
		n += t.Cost()
	}
	return n
}

// Payload builds the compressed content: secret || Separator || query.
func Payload(secret, query []byte) []byte {
	p := make([]byte, 0, len(secret)+1+len(query))
	p = append(p, secret...)
	p = append(p, Separator)
	p = append(p, query...)
	return p
}
