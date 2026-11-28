package promqlengine

import (
	"regexp"
	"sort"
)

// Storage is the minimal series/sample source the evaluator needs.
//
// Select returns, for every stored series matching matchers, the ordered
// points lying in the half-open time window (startMs, endMs]. A stale marker
// (IsStaleNaN) is returned like any other point; truncation semantics are the
// selector's responsibility, not the storage's.
//
// Implementations must return each series at most once with points sorted by
// timestamp. MemStorage is the reference in-memory implementation.
type Storage interface {
	Select(startMs, endMs int64, matchers []*Matcher) []Series
}

// MatchOp enumerates the label matcher operators.
type MatchOp int

const (
	MatchEqual MatchOp = iota
	MatchNotEqual
	MatchRegexp
	MatchNotRegexp
)

// String renders the operator in PromQL syntax.
func (op MatchOp) String() string {
	switch op {
	case MatchEqual:
		return "="
	case MatchNotEqual:
		return "!="
	case MatchRegexp:
		return "=~"
	case MatchNotRegexp:
		return "!~"
	default:
		return "?"
	}
}

// Matcher is one label selector predicate. Regex matchers use Go's RE2 syntax
// (the same engine Prometheus uses) and are fully anchored, as in PromQL.
type Matcher struct {
	Name string
	Op   MatchOp
	Val  string
	re   *regexp.Regexp
}

// NewMatcher compiles a matcher, validating regular expressions up front.
func NewMatcher(name string, op MatchOp, val string) (*Matcher, error) {
	m := &Matcher{Name: name, Op: op, Val: val}
	if op == MatchRegexp || op == MatchNotRegexp {
		re, err := regexp.Compile("^(?:" + val + ")$")
		if err != nil {
			return nil, err
		}
		m.re = re
	}
	return m, nil
}

// MustMatcher is the panicking counterpart of NewMatcher for static test
// setup.
func MustMatcher(name string, op MatchOp, val string) *Matcher {
	m, err := NewMatcher(name, op, val)
	if err != nil {
		panic(err)
	}
	return m
}

// Matches reports whether the matcher accepts value.
func (m *Matcher) Matches(value string) bool {
	switch m.Op {
	case MatchEqual:
		return value == m.Val
	case MatchNotEqual:
		return value != m.Val
	case MatchRegexp:
		return m.re.MatchString(value)
	case MatchNotRegexp:
		return !m.re.MatchString(value)
	default:
		return false
	}
}

func (m *Matcher) String() string {
	return m.Name + m.Op.String() + strconvQuote(m.Val)
}

// memSeries is the internal storage record.
type memSeries struct {
	metric Labels
	points []Point
}

// MemStorage is a thread-unsafe, append-only reference Storage backed by
// slices. It is intended for tests and embedders that build deterministic
// scenarios rather than for high-cardinality production workloads.
type MemStorage struct {
	series []*memSeries
	index  map[string]*memSeries
}

// NewMemStorage creates an empty in-memory storage.
func NewMemStorage() *MemStorage {
	return &MemStorage{index: map[string]*memSeries{}}
}

// AddSeries inserts a series with its points. The points are copied and
// sorted by timestamp. Adding the same label set twice merges the points.
func (s *MemStorage) AddSeries(metric Labels, points ...Point) {
	fp := metric.Fingerprint()
	ms, ok := s.index[fp]
	if !ok {
		ms = &memSeries{metric: metric.copy()}
		s.index[fp] = ms
		s.series = append(s.series, ms)
	}
	ms.points = append(ms.points, points...)
	sort.Slice(ms.points, func(i, j int) bool { return ms.points[i].T < ms.points[j].T })
}

// AddPoint appends a single point to an existing series or creates it.
func (s *MemStorage) AddPoint(metric Labels, t int64, v float64) {
	s.AddSeries(metric, Point{T: t, V: v})
}

// Select implements Storage.
func (s *MemStorage) Select(startMs, endMs int64, matchers []*Matcher) []Series {
	var out []Series
	for _, ms := range s.series {
		if !seriesMatches(ms.metric, matchers) {
			continue
		}
		// Window is (startMs, endMs].
		lo := sort.Search(len(ms.points), func(i int) bool { return ms.points[i].T > startMs })
		hi := sort.Search(len(ms.points), func(i int) bool { return ms.points[i].T > endMs })
		if lo == hi {
			continue
		}
		points := make([]Point, hi-lo)
		copy(points, ms.points[lo:hi])
		out = append(out, Series{Metric: ms.metric.copy(), Points: points})
	}
	sortMatrix(out)
	return out
}

// seriesMatches evaluates all predicates. Absent labels have the empty string
// as their implicit value, matching Prometheus semantics.
func seriesMatches(metric Labels, matchers []*Matcher) bool {
	for _, m := range matchers {
		if !m.Matches(metric.Get(m.Name)) {
			return false
		}
	}
	return true
}

func strconvQuote(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for _, r := range s {
		switch r {
		case '\\', '"':
			out = append(out, '\\', byte(r))
		case '\n':
			out = append(out, '\\', 'n')
		default:
			out = append(out, string(r)...)
		}
	}
	out = append(out, '"')
	return string(out)
}
