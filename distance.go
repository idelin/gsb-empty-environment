// Package fuzzy provides weighted fuzzy search over a dictionary of words.
//
// The distance between two words is the minimum total cost of turning one
// into the other using four edit operations: insertion, deletion,
// replacement, and swapping of two adjacent characters. Each operation has
// a non-negative integer cost supplied by the caller. Formally the distance
// is the shortest path in the edit graph whose nodes are the alignment
// positions (i, j) between the two words and whose edges are the four
// operations. A transposition is a single alignment step covering two
// adjacent positions of both words (OSA-style); it does not model repeated
// or overlapping swaps.
package fuzzy

// Costs holds the non-negative integer cost of each edit operation.
type Costs struct {
	Ins   int // insert one character
	Del   int // delete one character
	Rep   int // replace one character with a different one
	Trans int // swap two adjacent characters
}

func (c Costs) check() {
	if c.Ins < 0 || c.Del < 0 || c.Rep < 0 || c.Trans < 0 {
		panic("fuzzy: edit costs must be non-negative")
	}
}

// Distance returns the minimum-cost edit distance between a and b.
func Distance(a, b string, c Costs) int {
	c.check()
	ar := []rune(a)
	prev2, prev1 := []int(nil), initialRow(len(ar), c.Del)
	var parentCh, ch rune
	for _, ch = range []rune(b) {
		cur := extendRow(ar, parentCh, ch, prev2, prev1, c)
		prev2, prev1 = prev1, cur
		parentCh = ch
	}
	return prev1[len(ar)]
}

// initialRow returns the distances from every prefix of the query to the
// empty word: i deletions for the prefix of length i.
func initialRow(n, delCost int) []int {
	row := make([]int, n+1)
	for i := 1; i <= n; i++ {
		row[i] = row[i-1] + delCost
	}
	return row
}

// extendRow computes the next dynamic-programming row: the distances from
// every prefix of query qr to a word prefix extended by one rune ch.
// prev1 is the row of the current word prefix, prev2 the row of the prefix
// before it (nil at depth 1), and parentCh the last rune of the current
// prefix. prev2 and parentCh are only needed for transpositions.
func extendRow(qr []rune, parentCh, ch rune, prev2, prev1 []int, c Costs) []int {
	n := len(qr)
	cur := make([]int, n+1)
	cur[0] = prev1[0] + c.Ins
	for i := 1; i <= n; i++ {
		best := cur[i-1] + c.Del // delete qr[i-1]
		if v := prev1[i] + c.Ins; v < best {
			best = v // insert ch
		}
		sub := c.Rep
		if qr[i-1] == ch {
			sub = 0
		}
		if v := prev1[i-1] + sub; v < best {
			best = v // match or replace
		}
		if prev2 != nil && i >= 2 && qr[i-2] == ch && qr[i-1] == parentCh {
			if v := prev2[i-2] + c.Trans; v < best {
				best = v // adjacent transposition
			}
		}
		cur[i] = best
	}
	return cur
}

// rowBound is a lower bound on the distance from the query to any word
// carrying the given rows: every path in the edit graph that reaches a
// column beyond the current one passes through the last two rows, and all
// edge costs are non-negative.
func rowBound(cur, prev []int) int {
	m := minRow(cur)
	if prev != nil {
		if v := minRow(prev); v < m {
			m = v
		}
	}
	return m
}

func minRow(row []int) int {
	m := row[0]
	for _, v := range row[1:] {
		if v < m {
			m = v
		}
	}
	return m
}
