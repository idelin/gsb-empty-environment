package netsec

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
)

// maxDepth bounds the number of decoding layers that may be traversed.
const maxDepth = 3

// Range is a half-open byte interval [Start, End).
type Range struct {
	Start, End int
}

// Encoding identifies a supported decoding scheme.
type Encoding string

const (
	EncHex    Encoding = "hex"
	EncBase64 Encoding = "base64"
)

// Step is one replayable decoding step: decode the Src range of the step's
// input content with Encoding. The first step's input is the original text;
// each later step's input is the previous step's decoded output.
type Step struct {
	Encoding Encoding
	Src      Range
}

// Finding reports one secret occurrence reachable through a decoding chain.
type Finding struct {
	// Secret is the index of the matched secret in the caller's slice.
	Secret int
	// Chain is the replayable decoding chain, ordered from the original
	// text inwards. It is empty when the secret appears in plaintext.
	Chain []Step
	// Cover is the minimal range of the original text that covers every
	// source character the matched secret bytes were decoded from.
	Cover Range
}

// UncheckedRange identifies a candidate that was not expanded because the
// expansion budget was exhausted. NodeID 0 denotes the original text; any
// other ID denotes an intermediate decoded content, and Range is expressed
// in that content's coordinates.
type UncheckedRange struct {
	NodeID   int
	Range    Range
	Encoding Encoding
}

// Result is the outcome of a Scan.
type Result struct {
	Findings []Finding
	// Unchecked lists ranges left unexamined due to budget exhaustion.
	Unchecked []UncheckedRange
	// Expansions is the number of successful decode expansions performed.
	Expansions int
}

// Options tunes Scan. The zero value is usable.
type Options struct {
	// MaxDepth limits decoding layers; values outside [1, 3] become 3.
	MaxDepth int
	// MaxExpansions caps the total number of successful decode expansions.
	// Zero or negative means unlimited. Once exhausted, remaining
	// candidates are reported as Unchecked.
	MaxExpansions int
	// MinToken is the minimum character length of auto-detected encoded
	// tokens inside decoded content. Zero defaults to 4.
	MinToken int
}

type match struct {
	secret     int
	start, end int
}

// node is a unique decoded content. Identical contents decoded from
// different sources share one node but keep distinct incoming edges.
type node struct {
	id       int
	content  []byte
	depth    int
	edges    []*edge // incoming provenance edges
	matches  []match
	innerRun bool
}

// edge records that child content was decoded from the parent's src range,
// with bmap mapping each child byte back to its source range in the parent.
type edge struct {
	parent *node
	enc    Encoding
	src    Range
	bmap   []Range
}

type task struct {
	n   *node
	r   Range
	enc Encoding
}

type scanner struct {
	text    []byte
	secrets [][]byte
	opts    Options

	nodes      []*node
	byContent  map[string]*node
	root       *node
	queue      []task
	expansions int
	unchecked  []UncheckedRange
}

// Scan decodes the caller-marked candidate ranges of text (strict hex and
// Base64, up to three layers) and reports every occurrence of the given
// secrets together with its minimal original-text cover and replayable
// decoding chain. Candidate ranges are clipped to text bounds.
func Scan(text []byte, secrets [][]byte, candidates []Range, opts Options) Result {
	_, res := scan(text, secrets, candidates, opts)
	return res
}

func scan(text []byte, secrets [][]byte, candidates []Range, opts Options) (*scanner, Result) {
	if opts.MaxDepth <= 0 || opts.MaxDepth > maxDepth {
		opts.MaxDepth = maxDepth
	}
	if opts.MinToken <= 0 {
		opts.MinToken = 4
	}
	s := &scanner{
		text:      text,
		secrets:   secrets,
		opts:      opts,
		byContent: make(map[string]*node),
	}
	s.root, _ = s.newNode(text, 0)
	s.scanSecrets(s.root)
	for _, c := range candidates {
		if c.Start < 0 {
			c.Start = 0
		}
		if c.End > len(text) {
			c.End = len(text)
		}
		if c.Start >= c.End {
			continue
		}
		s.enqueue(s.root, c)
	}
	s.run()
	return s, s.collect()
}

func (s *scanner) newNode(content []byte, depth int) (*node, bool) {
	key := string(content)
	if n, ok := s.byContent[key]; ok {
		return n, false
	}
	n := &node{id: len(s.nodes), content: content, depth: depth}
	s.nodes = append(s.nodes, n)
	s.byContent[key] = n
	return n, true
}

// enqueue queues decode attempts for range r of n's content, trying every
// encoding the span is eligible for.
func (s *scanner) enqueue(n *node, r Range) {
	span := n.content[r.Start:r.End]
	if isHexSpan(span) {
		s.queue = append(s.queue, task{n, r, EncHex})
	}
	if isB64Span(span) {
		s.queue = append(s.queue, task{n, r, EncBase64})
	}
}

// detectInner finds encoded-looking tokens inside freshly decoded content
// and queues them for the next layer.
func (s *scanner) detectInner(n *node) {
	c := n.content
	for i := 0; i < len(c); {
		if !isB64Char(c[i]) {
			i++
			continue
		}
		j := i
		for j < len(c) && isB64Char(c[j]) {
			j++
		}
		end := j
		for end < len(c) && c[end] == '=' && end-j < 2 {
			end++
		}
		if j-i >= s.opts.MinToken {
			s.enqueue(n, Range{i, end})
		}
		i = end
	}
}

func (s *scanner) run() {
	seen := make(map[UncheckedRange]bool)
	for len(s.queue) > 0 {
		t := s.queue[0]
		s.queue = s.queue[1:]
		if s.opts.MaxExpansions > 0 && s.expansions >= s.opts.MaxExpansions {
			u := UncheckedRange{NodeID: t.n.id, Range: t.r, Encoding: t.enc}
			if !seen[u] {
				seen[u] = true
				s.unchecked = append(s.unchecked, u)
			}
			continue
		}
		s.expand(t)
	}
}

func (s *scanner) expand(t task) {
	span := t.n.content[t.r.Start:t.r.End]
	var dec []byte
	var bm []Range
	var err error
	switch t.enc {
	case EncHex:
		dec, err = decodeHexStrict(span)
		if err == nil {
			bm = hexByteMap(t.r.Start, len(dec))
		}
	case EncBase64:
		dec, err = decodeBase64Strict(span)
		if err == nil {
			bm = base64ByteMap(len(span), t.r.Start, len(dec))
		}
	}
	if err != nil || len(dec) == 0 {
		return
	}
	child, fresh := s.newNode(dec, t.n.depth+1)
	for _, e := range child.edges {
		if e.parent == t.n && e.enc == t.enc && e.src == t.r {
			return // duplicate candidate, edge already recorded
		}
	}
	s.expansions++
	child.edges = append(child.edges, &edge{parent: t.n, enc: t.enc, src: t.r, bmap: bm})
	if d := t.n.depth + 1; child.depth > d {
		child.depth = d
	}
	if fresh {
		s.scanSecrets(child)
	}
	if !child.innerRun && child.depth < s.opts.MaxDepth {
		child.innerRun = true
		s.detectInner(child)
	}
}

func (s *scanner) scanSecrets(n *node) {
	for si, sec := range s.secrets {
		if len(sec) == 0 || len(sec) > len(n.content) {
			continue
		}
		off := 0
		for {
			idx := bytes.Index(n.content[off:], sec)
			if idx < 0 {
				break
			}
			n.matches = append(n.matches, match{secret: si, start: off + idx, end: off + idx + len(sec)})
			off += idx + 1
		}
	}
}

// paths enumerates every provenance chain from the root to n, as edge lists
// ordered root-inwards. Cycles and chains deeper than MaxDepth are cut.
func (s *scanner) paths(n *node) [][]*edge {
	var out [][]*edge
	var walk func(cur *node, rev []*edge, seen map[*node]bool)
	walk = func(cur *node, rev []*edge, seen map[*node]bool) {
		if cur == s.root {
			p := make([]*edge, len(rev))
			for i, e := range rev {
				p[len(rev)-1-i] = e
			}
			out = append(out, p)
			return
		}
		if len(rev) >= s.opts.MaxDepth {
			return
		}
		seen[cur] = true
		for _, e := range cur.edges {
			if !seen[e.parent] {
				walk(e.parent, append(rev, e), seen)
			}
		}
		delete(seen, cur)
	}
	walk(n, nil, make(map[*node]bool))
	return out
}

// mapRange folds r (a range in the child content) through bmap into the
// minimal covering range in the parent content.
func mapRange(bmap []Range, r Range) (Range, bool) {
	if r.Start < 0 || r.End > len(bmap) || r.Start >= r.End {
		return Range{}, false
	}
	lo, hi := bmap[r.Start].Start, bmap[r.Start].End
	for i := r.Start + 1; i < r.End; i++ {
		if bmap[i].Start < lo {
			lo = bmap[i].Start
		}
		if bmap[i].End > hi {
			hi = bmap[i].End
		}
	}
	return Range{lo, hi}, true
}

func (s *scanner) collect() Result {
	res := Result{Expansions: s.expansions, Unchecked: s.unchecked}
	seen := make(map[string]bool)
	for _, n := range s.nodes {
		if len(n.matches) == 0 {
			continue
		}
		for _, p := range s.paths(n) {
			for _, m := range n.matches {
				cover := Range{m.start, m.end}
				ok := true
				for i := len(p) - 1; i >= 0; i-- {
					cover, ok = mapRange(p[i].bmap, cover)
					if !ok {
						break
					}
				}
				if !ok {
					continue
				}
				chain := make([]Step, len(p))
				for i, e := range p {
					chain[i] = Step{Encoding: e.enc, Src: e.src}
				}
				key := chainKey(m.secret, cover, chain)
				if seen[key] {
					continue
				}
				seen[key] = true
				res.Findings = append(res.Findings, Finding{Secret: m.secret, Chain: chain, Cover: cover})
			}
		}
	}
	sort.Slice(res.Findings, func(i, j int) bool {
		a, b := res.Findings[i], res.Findings[j]
		if a.Cover.Start != b.Cover.Start {
			return a.Cover.Start < b.Cover.Start
		}
		if a.Cover.End != b.Cover.End {
			return a.Cover.End < b.Cover.End
		}
		if a.Secret != b.Secret {
			return a.Secret < b.Secret
		}
		return chainKey(a.Secret, a.Cover, a.Chain) < chainKey(b.Secret, b.Cover, b.Chain)
	})
	return res
}

func chainKey(secret int, cover Range, chain []Step) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d@%d:%d", secret, cover.Start, cover.End)
	for _, st := range chain {
		fmt.Fprintf(&b, "|%s:%d:%d", st.Encoding, st.Src.Start, st.Src.End)
	}
	return b.String()
}
