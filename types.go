package netsec

import (
	"net/http"
	"strings"
	"time"
)

// Well-known observation field paths. Only these (and the SelectHeader*
// selectors derived from them) can be suppressed by the caller's allowlist.
const (
	FieldStatus     = "status_code"
	FieldHeaders    = "headers"
	FieldBody       = "body"
	FieldLength     = "length"
	FieldCompressed = "compressed_length"
	FieldTiming     = "timing"
)

// Stage names used by a typical login request. Callers may define their own
// string names; these are convenience constants.
const (
	StageDNS      = "dns"
	StageTCP      = "tcp"
	StageTLS      = "tls"
	StageTTFB     = "ttfb"
	StageTotal    = "total"
	StageBodyRead = "body_read"
)

// Int64Interval is an inclusive integer range [Min, Max].
//
// Observations are expressed as ranges rather than point values: a login
// endpoint leaks no length information as long as the ranges observed for the
// two account classes overlap. Two ranges are therefore equal only when they
// share at least one integer value.
type Int64Interval struct {
	Min int64
	Max int64
}

// Contains reports whether value lies within the interval.
func (r Int64Interval) Contains(value int64) bool {
	return value >= r.Min && value <= r.Max
}

// Overlaps reports whether r and other share at least one integer value.
func (r Int64Interval) Overlaps(other Int64Interval) bool {
	return r.Min <= other.Max && other.Min <= r.Max
}

// Valid reports whether Min <= Max.
func (r Int64Interval) Valid() bool {
	return r.Min <= r.Max
}

// TimingTier is the coarse timing band recorded for one request stage.
//
// The tier deliberately abstracts away wall-clock jitter: test hosts assign a
// band (e.g. "fast", "slow", "timeout") instead of comparing exact durations.
// A timed-out stage is its own band and can never overlap a non-timeout band,
// so a timeout reached for only one account class is a distinguisher even
// when the timeout value itself is randomized.
type TimingTier struct {
	// Bucket is the caller-assigned band label. Labels are informational:
	// the actual equality decision is based on TimedOut and Range.
	Bucket string
	// TimedOut marks the stage as having hit a timeout.
	TimedOut bool
	// Range is the inclusive duration band of the tier.
	Range DurationRange
}

// DurationRange is an inclusive duration range [Min, Max].
type DurationRange struct {
	Min time.Duration
	Max time.Duration
}

// Valid reports whether Min <= Max.
func (r DurationRange) Valid() bool {
	return r.Min <= r.Max
}

// Overlaps reports whether r and other share at least one duration.
func (r DurationRange) Overlaps(other DurationRange) bool {
	return r.Min <= other.Max && other.Min <= r.Max
}

// TimingProfile holds the tier observed for every measured stage. Stage names
// are map keys so that a stage missing on only one side is itself detectable.
type TimingProfile map[string]TimingTier

// HeaderView captures a response header preserving multiplicity and order.
//
// Duplicate header names carry distinct values (e.g. two Set-Cookie values);
// collapsing them into a set would hide a real distinguisher. Values are
// compared element by element, including ordering.
type HeaderView struct {
	Name   string
	Values []string
}

// HeadersFromHTTP converts an http.Header into an ordered HeaderView slice.
// Header names are canonicalized and the result is sorted by name, matching
// the normalization applied by Compare.
func HeadersFromHTTP(h http.Header) []HeaderView {
	views := make([]HeaderView, 0, len(h))
	for name, values := range h {
		copied := append([]string(nil), values...)
		views = append(views, HeaderView{Name: name, Values: copied})
	}
	sortHeaders(views)
	return views
}

// BodyView describes the response body through its template and length bands.
//
// Template is the body with all per-request random content replaced by stable
// placeholders, so templated bodies compare structurally rather than by raw
// text. Compressed is the Content-Encoding-aware length band and may be nil
// when the response is not encoded or the compressed length was not observed.
type BodyView struct {
	Template   string
	Length     Int64Interval
	Compressed *Int64Interval
}

// Observation is everything a test host can externally observe about one
// login response (or one distribution band of repeated responses).
type Observation struct {
	Label   string
	Status  int
	Headers []HeaderView
	Body    BodyView
	Timing  TimingProfile
}

// FieldSelector names a single observation field that the caller declares
// randomized and therefore safe to ignore. Selectors created by the
// SelectHeader* constructors use canonical header names.
type FieldSelector struct {
	path string
}

// SelectField ignores a whole dimension: use the Field* constants.
func SelectField(field string) FieldSelector {
	return FieldSelector{path: field}
}

// SelectHeader ignores every value (including multiplicity and order) of one
// header name.
func SelectHeader(name string) FieldSelector {
	return FieldSelector{path: headerPrefix(http.CanonicalHeaderKey(name))}
}

// SelectHeaderValue ignores one indexed occurrence of a header name.
func SelectHeaderValue(name string, index int) FieldSelector {
	return FieldSelector{path: headerIndexPath(http.CanonicalHeaderKey(name), index)}
}

// SelectStage ignores the timing tier of one stage.
func SelectStage(stage string) FieldSelector {
	return FieldSelector{path: FieldTiming + "." + stage}
}

func headerPrefix(canonicalName string) string {
	return FieldHeaders + "." + canonicalName
}

func headerIndexPath(canonicalName string, index int) string {
	var b strings.Builder
	b.WriteString(headerPrefix(canonicalName))
	b.WriteByte('[')
	writeInt(&b, int64(index))
	b.WriteByte(']')
	return b.String()
}

func httpCanonical(name string) string {
	return http.CanonicalHeaderKey(name)
}
