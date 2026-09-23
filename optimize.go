package runlzv2

type indexDeque struct {
	values []int
	head   int
}

func (d *indexDeque) pushBack(v int) { d.values = append(d.values, v) }

func (d *indexDeque) popBack() { d.values = d.values[:len(d.values)-1] }

func (d *indexDeque) popFront() { d.head++ }

func (d *indexDeque) front() int { return d.values[d.head] }

func (d *indexDeque) back() int { return d.values[len(d.values)-1] }

func (d *indexDeque) empty() bool { return d.head == len(d.values) }

func (d *indexDeque) reset() {
	d.values = d.values[:0]
	d.head = 0
}

func runEnds(src []byte) []int {
	ends := make([]int, 0, 8)
	start := 0
	for i := 1; i <= len(src); i++ {
		if i == len(src) || src[i] != src[i-1] {
			ends = append(ends, start, i)
			start = i
		}
	}
	return ends
}

// planOptimal returns a minimum-total-byte segmentation using the real
// header costs (4 bytes per block). Suffix DP with monotonic deques:
// O(n) time, O(n) memory.
//
//	dp[j] = min over blocks ending at j:
//	  literal [i,j): dp[i] + BlockHeaderLen + (j-i), j-i in [1,MaxBlockLen]
//	  repeat  [i,j): dp[i] + BlockHeaderLen,         j-i in [2,MaxBlockLen],
//	                 and src[i:j] is a single byte value
func planOptimal(src []byte) []segment {
	n := len(src)
	if n == 0 {
		return nil
	}

	runs := runEnds(src)
	dp := make([]int64, n+1)
	choiceKind := make([]byte, n+1)
	prev := make([]int, n+1)

	var literalQ, repeatQ indexDeque
	runIdx := 0
	runStart, runEnd := runs[0], runs[1]

	for j := 1; j <= n; j++ {
		for j > runEnd {
			runIdx += 2
			runStart, runEnd = runs[runIdx], runs[runIdx+1]
			repeatQ.reset()
		}

		// Literal candidate i=j-1, keyed by dp[i]-i.
		c := j - 1
		cKey := dp[c] - int64(c)
		for !literalQ.empty() {
			b := literalQ.back()
			if dp[b]-int64(b) < cKey {
				break
			}
			literalQ.popBack()
		}
		literalQ.pushBack(c)
		for literalQ.front() < j-MaxBlockLen {
			literalQ.popFront()
		}

		// Repeat candidate i=j-2, keyed by dp[i], valid only inside the run.
		if c2 := j - 2; c2 >= runStart {
			for !repeatQ.empty() && dp[repeatQ.back()] >= dp[c2] {
				repeatQ.popBack()
			}
			repeatQ.pushBack(c2)
		}
		minStart := runStart
		if j-MaxBlockLen > minStart {
			minStart = j - MaxBlockLen
		}
		for !repeatQ.empty() && repeatQ.front() < minStart {
			repeatQ.popFront()
		}

		li := literalQ.front()
		best := dp[li] + int64(BlockHeaderLen) + int64(j-li)
		kind := byte(tagLiteral)
		p := li

		if !repeatQ.empty() {
			ri := repeatQ.front()
			rc := dp[ri] + int64(BlockHeaderLen)
			if rc <= best {
				best = rc
				kind = tagRepeat
				p = ri
			}
		}

		dp[j] = best
		choiceKind[j] = kind
		prev[j] = p
	}

	segments := make([]segment, 0, 8)
	for j := n; j > 0; {
		p := prev[j]
		k := kindLiteral
		if choiceKind[j] == tagRepeat {
			k = kindRepeat
		}
		segments = append(segments, segment{start: p, end: j, kind: k})
		j = p
	}
	for l, r := 0, len(segments)-1; l < r; l, r = l+1, r-1 {
		segments[l], segments[r] = segments[r], segments[l]
	}
	return segments
}
