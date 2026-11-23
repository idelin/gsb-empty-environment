package envguard

import "strings"

// seg is one normalized POSIX path segment, remembered as a rune range over
// the original rope so violations can report their source interval.
type seg struct {
	from, to int64
	dotDot   bool
}

// normalizeSegments performs POSIX path.Clean-style normalization without
// materializing the rope: runs of '/', '.' and '..' are folded while only
// segment ranges are retained.
func (g *Graph) normalizeSegments(id NodeID) ([]seg, bool) {
	cur := g.newCursor(id)
	rooted := false
	var stack []seg
	var start int64
	var have bool
	var first, second rune
	var length int64
	var seenSegment bool
	flush := func(end int64) {
		if !have {
			return
		}
		dot := length == 1 && first == '.'
		dotDot := length == 2 && first == '.' && second == '.'
		switch {
		case dot:
			// Collapses away.
		case dotDot:
			if len(stack) > 0 && !stack[len(stack)-1].dotDot {
				stack = stack[:len(stack)-1]
			} else if !rooted {
				stack = append(stack, seg{from: start, to: end, dotDot: true})
			}
		default:
			stack = append(stack, seg{from: start, to: end})
		}
		have = false
	}
	for {
		r, off, ok := cur.next()
		if !ok {
			flush(cur.pos)
			break
		}
		if r == '/' {
			if !have && len(stack) == 0 && !rooted && !seenSegment {
				rooted = true
			}
			flush(off)
			continue
		}
		if !have {
			have = true
			start = off
			length = 0
			first, second = 0, 0
		}
		switch length {
		case 0:
			first = r
		case 1:
			second = r
		}
		length++
		seenSegment = true
	}
	return stack, rooted
}

// normalizePath renders the normalized path for short inputs and for the
// character-by-character explanation.
func (g *Graph) normalizePath(id NodeID) string {
	segs, rooted := g.normalizeSegments(id)
	if len(segs) == 0 {
		if rooted {
			return "/"
		}
		return "."
	}
	// Rebuild by streaming the whole rope once, selecting the normalized
	// segment ranges; deep nodes are handled by the cursor's explicit stack.
	var b strings.Builder
	if rooted {
		b.WriteByte('/')
	}
	for i, s := range segs {
		if i > 0 {
			b.WriteByte('/')
		}
		cur := g.newCursorAt(id, s.from)
		want := s.to - s.from
		for k := int64(0); k < want; k++ {
			r, _, ok := cur.next()
			if !ok {
				break
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

// checkPath proves the normalized path sits under one of the approved
// prefixes. Prefixes are pre-normalized host constants; matching is segment
// based so "/approved-x" cannot impersonate "/approved".
func (g *Graph) checkPath(id NodeID, prefixes []string) *Violation {
	segs, _ := g.normalizeSegments(id)
	approved := g.prefixSegLists(prefixes)
	matchedPrefix := false
	// Stream segment text alongside the stored ranges so each rune is read once.
	cur := g.newCursor(id)
	var curRunes []rune
	for idx, s := range segs {
		for {
			r, off, ok := cur.next()
			if !ok {
				break
			}
			if off >= s.to {
				break
			}
			if off >= s.from {
				curRunes = append(curRunes, r)
			}
		}
		text := string(curRunes)
		curRunes = curRunes[:0]
		if matchedPrefix {
			continue
		}
		if g.segmentAllowed(idx, text, approved, &matchedPrefix) {
			continue
		}
		return &Violation{Kind: ViolationPath, From: s.from, To: s.to}
	}
	return nil
}

func (g *Graph) prefixSegLists(prefixes []string) [][]string {
	var out [][]string
	for _, p := range prefixes {
		p = strings.TrimPrefix(p, "/")
		if p == "" {
			out = append(out, nil)
			continue
		}
		out = append(out, strings.Split(p, "/"))
	}
	return out
}

func (g *Graph) segmentAllowed(idx int, segText string, approved [][]string, matched *bool) bool {
	for _, pre := range approved {
		if len(pre) == 0 {
			*matched = true
			return true // prefix "/" approves every segment
		}
		if idx >= len(pre) {
			continue
		}
		if pre[idx] != segText {
			continue
		}
		// This segment matches. Once the whole prefix has been consumed,
		// every later segment is free.
		if idx == len(pre)-1 {
			*matched = true
		}
		return true
	}
	return false
}
