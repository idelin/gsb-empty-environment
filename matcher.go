package streamsel

// Matcher is a streaming CSS-selector matcher. HTML is fed in arbitrary
// chunks via Write; the texts of matching elements are returned as soon as
// each element closes. Memory usage is proportional to the current element
// nesting depth (times the selector chain length), not to the document size.
type Matcher struct {
	sel         selector
	carry       uint64 // bit i set => step i+1 is linked by a descendant combinator
	p           parser
	stack       []matchFrame
	matchedOpen int // number of open frames currently capturing text
	out         []string
}

type matchFrame struct {
	tag     string
	bits    uint64 // bit i: this element satisfies step i of the selector chain
	matched bool   // this element matched the full selector
	buf     []byte // captured text, only used when matched
}

// NewMatcher compiles a selector and returns a streaming Matcher.
func NewMatcher(sel string) (*Matcher, error) {
	s, err := parseSelector(sel)
	if err != nil {
		return nil, err
	}
	m := &Matcher{sel: s}
	for i := 1; i < len(s.steps); i++ {
		if s.steps[i].comb == combDescendant {
			m.carry |= 1 << (i - 1)
		}
	}
	m.p.ev = m
	return m, nil
}

// Write feeds one chunk of HTML and returns the texts of all matching
// elements that were completed by this chunk. The returned slice is reused
// on the next call; copy it if it must be retained.
func (m *Matcher) Write(p []byte) []string {
	m.out = m.out[:0]
	m.p.feed(p)
	return m.out
}

// Close signals end of input. Any elements still open are closed implicitly
// and their texts returned if they match.
func (m *Matcher) Close() []string {
	m.out = m.out[:0]
	m.p.close()
	return m.out
}

func (m *Matcher) startTag(name string, attrs []attr) {
	var parentBits uint64
	if n := len(m.stack); n > 0 {
		parentBits = m.stack[n-1].bits
	}
	// Descendant-linked progress propagates down the tree.
	bits := parentBits & m.carry
	last := uint(len(m.sel.steps) - 1)
	for i := uint(0); i < last; i++ {
		if parentBits&(1<<i) != 0 && m.sel.steps[i+1].sel.matches(name, attrs) {
			bits |= 1 << (i + 1)
		}
	}
	if m.sel.steps[0].sel.matches(name, attrs) {
		bits |= 1
	}
	m.stack = append(m.stack, matchFrame{
		tag:     name,
		bits:    bits,
		matched: bits&(1<<last) != 0,
	})
	if m.stack[len(m.stack)-1].matched {
		m.matchedOpen++
	}
}

func (m *Matcher) endTag(name string) {
	n := len(m.stack) - 1
	f := m.stack[n]
	m.stack = m.stack[:n]
	if f.matched {
		m.matchedOpen--
		m.out = append(m.out, string(f.buf))
	}
}

func (m *Matcher) text(s string) {
	if m.matchedOpen == 0 {
		return
	}
	for i := range m.stack {
		if m.stack[i].matched {
			m.stack[i].buf = append(m.stack[i].buf, s...)
		}
	}
}
