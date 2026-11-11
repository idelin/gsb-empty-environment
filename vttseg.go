// Package vttseg segments live WebVTT subtitle streams so they stay aligned
// with HLS video segments. Cues are split on segment boundaries (a cue
// spanning a boundary appears in both adjacent segments), local cue times are
// mapped onto the 90 kHz MPEG clock via X-TIMESTAMP-MAP with 33-bit
// wraparound handling, and cues arriving out of order can still be inserted
// until the affected segment is closed.
package vttseg

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ClockRate is the MPEG-2 system clock frequency in Hz.
const ClockRate = 90000

// mpegMod is the 33-bit MPEG timestamp modulus.
const mpegMod = uint64(1) << 33

// TimestampMap maps a local WebVTT timestamp to the 90 kHz MPEG clock,
// mirroring the HLS X-TIMESTAMP-MAP header.
type TimestampMap struct {
	// Local is the local (WebVTT) timestamp the mapping is anchored at.
	Local time.Duration
	// MPEG is the 33-bit MPEGTS value (90 kHz ticks) corresponding to Local.
	MPEG uint64
}

// LocalToMPEG converts a local timestamp to a 33-bit MPEGTS value, wrapping
// around the 33-bit modulus.
func (m TimestampMap) LocalToMPEG(local time.Duration) uint64 {
	return AddMPEG(m.MPEG, durationToTicks(local-m.Local))
}

// AddMPEG adds a signed 90 kHz tick delta to an MPEG timestamp, wrapping at
// the 33-bit boundary.
func AddMPEG(base uint64, delta int64) uint64 {
	mod := int64(mpegMod)
	v := (int64(base%mpegMod) + delta%mod) % mod
	if v < 0 {
		v += mod
	}
	return uint64(v)
}

// durationToTicks converts a duration to 90 kHz clock ticks.
func durationToTicks(d time.Duration) int64 {
	return int64(d) * int64(ClockRate) / int64(time.Second)
}

// Cue is a single WebVTT cue. Settings and Payload are preserved verbatim
// from the parsed input.
type Cue struct {
	ID       string
	Start    time.Duration
	End      time.Duration
	Settings string   // cue settings after the "-->" times, without the leading space
	Payload  []string // payload lines, re-joined with "\n" on render
}

// Segmenter accumulates cues for a live stream and renders per-segment
// WebVTT files aligned to fixed-duration segment boundaries.
type Segmenter struct {
	segDur time.Duration
	header []string // extra header blocks (STYLE, NOTE, REGION), verbatim
	cues   []Cue    // sorted by (Start, End, ID)
	tm     TimestampMap
	closed int // number of leading segments already closed
}

// NewSegmenter returns a Segmenter producing segments of segDur.
func NewSegmenter(segDur time.Duration) *Segmenter {
	if segDur <= 0 {
		panic("vttseg: segment duration must be positive")
	}
	return &Segmenter{segDur: segDur}
}

// SetTimestampMap anchors local stream time to the MPEG clock. It is used to
// compute each rendered segment's X-TIMESTAMP-MAP header.
func (s *Segmenter) SetTimestampMap(tm TimestampMap) {
	s.tm = tm
}

// AddHeaderBlock appends a raw header block (e.g. a STYLE or NOTE block) that
// is reproduced verbatim in every rendered segment.
func (s *Segmenter) AddHeaderBlock(block string) {
	s.header = append(s.header, block)
}

// AddCue inserts a cue. Cues may arrive out of order, but a cue whose first
// affected segment has already been closed is rejected.
func (s *Segmenter) AddCue(c Cue) error {
	if c.End <= c.Start {
		return fmt.Errorf("vttseg: cue %q has non-positive duration (%s..%s)", c.ID, c.Start, c.End)
	}
	if first := int(c.Start / s.segDur); first < s.closed {
		return fmt.Errorf("vttseg: cue %q at %s arrives after segment %d was closed", c.ID, c.Start, first)
	}
	i := sort.Search(len(s.cues), func(i int) bool {
		ci := s.cues[i]
		if ci.Start != c.Start {
			return ci.Start > c.Start
		}
		if ci.End != c.End {
			return ci.End > c.End
		}
		return ci.ID >= c.ID
	})
	s.cues = append(s.cues, Cue{})
	copy(s.cues[i+1:], s.cues[i:])
	s.cues[i] = c
	return nil
}

// CloseThrough closes every segment whose end boundary is at or before the
// given local time. Cues are no longer accepted for closed segments.
func (s *Segmenter) CloseThrough(local time.Duration) {
	if n := int(local / s.segDur); n > s.closed {
		s.closed = n
	}
}

// Closed reports whether segment i has been closed.
func (s *Segmenter) Closed(i int) bool {
	return i < s.closed
}

// Render produces the WebVTT file for segment i. Cue times are clamped to
// the segment boundaries and rebased to zero; the X-TIMESTAMP-MAP header
// maps local time 0 onto the MPEG clock at the segment start.
func (s *Segmenter) Render(i int) (string, error) {
	if i < 0 {
		return "", fmt.Errorf("vttseg: negative segment index %d", i)
	}
	if s.Closed(i) {
		return "", fmt.Errorf("vttseg: segment %d already closed", i)
	}
	segStart := time.Duration(i) * s.segDur
	segEnd := segStart + s.segDur

	var b strings.Builder
	b.WriteString("WEBVTT\n")
	fmt.Fprintf(&b, "X-TIMESTAMP-MAP=LOCAL:00:00:00.000,MPEGTS:%d\n", s.tm.LocalToMPEG(segStart))
	for _, h := range s.header {
		b.WriteString("\n")
		b.WriteString(strings.TrimRight(h, "\n"))
		b.WriteString("\n")
	}
	for _, c := range s.cues {
		if c.End <= segStart || c.Start >= segEnd {
			continue
		}
		start := max(c.Start, segStart) - segStart
		end := min(c.End, segEnd) - segStart
		b.WriteString("\n")
		if c.ID != "" {
			b.WriteString(c.ID)
			b.WriteString("\n")
		}
		b.WriteString(formatTimestamp(start))
		b.WriteString(" --> ")
		b.WriteString(formatTimestamp(end))
		if c.Settings != "" {
			b.WriteString(" ")
			b.WriteString(c.Settings)
		}
		b.WriteString("\n")
		for _, line := range c.Payload {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	return b.String(), nil
}

// Parse splits a WebVTT document into its header blocks, its
// X-TIMESTAMP-MAP (if present), and its cues. Header blocks and cue
// settings/payloads are preserved verbatim.
func Parse(data string) (header []string, tm *TimestampMap, cues []Cue, err error) {
	data = strings.ReplaceAll(data, "\r\n", "\n")
	data = strings.TrimPrefix(data, "\ufeff")
	lines := strings.Split(data, "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "WEBVTT") {
		return nil, nil, nil, fmt.Errorf("vttseg: missing WEBVTT signature")
	}

	// Split the body into blank-line-separated blocks.
	var blocks [][]string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			blocks = append(blocks, cur)
			cur = nil
		}
	}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		cur = append(cur, line)
	}
	flush()

	for _, block := range blocks {
		first := block[0]
		switch {
		case strings.HasPrefix(first, "X-TIMESTAMP-MAP="):
			m, perr := parseTimestampMap(first)
			if perr != nil {
				return nil, nil, nil, perr
			}
			tm = &m
		case strings.HasPrefix(first, "STYLE"),
			strings.HasPrefix(first, "NOTE"),
			strings.HasPrefix(first, "REGION"):
			header = append(header, strings.Join(block, "\n"))
		default:
			cue, perr := parseCue(block)
			if perr != nil {
				return nil, nil, nil, perr
			}
			cues = append(cues, cue)
		}
	}
	return header, tm, cues, nil
}

// AddVTT parses a WebVTT document and feeds it into the segmenter: header
// blocks are kept verbatim, an X-TIMESTAMP-MAP (if any) becomes the
// segmenter's timestamp map, and cues are inserted in order.
func (s *Segmenter) AddVTT(data string) error {
	header, tm, cues, err := Parse(data)
	if err != nil {
		return err
	}
	for _, h := range header {
		s.AddHeaderBlock(h)
	}
	if tm != nil {
		s.SetTimestampMap(*tm)
	}
	for _, c := range cues {
		if err := s.AddCue(c); err != nil {
			return err
		}
	}
	return nil
}

func parseCue(lines []string) (Cue, error) {
	var c Cue
	timing := 0
	if !strings.Contains(lines[0], "-->") {
		c.ID = lines[0]
		timing = 1
	}
	if timing >= len(lines) || !strings.Contains(lines[timing], "-->") {
		return c, fmt.Errorf("vttseg: cue %q missing timing line", c.ID)
	}
	fields := strings.SplitN(lines[timing], "-->", 2)
	start, err := parseTimestamp(strings.TrimSpace(fields[0]))
	if err != nil {
		return c, err
	}
	rest := strings.TrimSpace(fields[1])
	endStr, settings, _ := strings.Cut(rest, " ")
	end, err := parseTimestamp(strings.TrimSpace(endStr))
	if err != nil {
		return c, err
	}
	c.Start, c.End = start, end
	c.Settings = strings.TrimSpace(settings)
	c.Payload = append(c.Payload, lines[timing+1:]...)
	return c, nil
}

func parseTimestampMap(line string) (TimestampMap, error) {
	var tm TimestampMap
	for _, kv := range strings.Split(strings.TrimPrefix(line, "X-TIMESTAMP-MAP="), ",") {
		k, v, ok := strings.Cut(kv, ":")
		if !ok {
			return tm, fmt.Errorf("vttseg: malformed X-TIMESTAMP-MAP %q", line)
		}
		switch strings.TrimSpace(k) {
		case "LOCAL":
			d, err := parseTimestamp(strings.TrimSpace(v))
			if err != nil {
				return tm, err
			}
			tm.Local = d
		case "MPEGTS":
			n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
			if err != nil {
				return tm, fmt.Errorf("vttseg: bad MPEGTS value: %w", err)
			}
			tm.MPEG = n % mpegMod
		}
	}
	return tm, nil
}

// parseTimestamp parses a WebVTT timestamp (MM:SS.mmm or HH:MM:SS.mmm).
func parseTimestamp(s string) (time.Duration, error) {
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, fmt.Errorf("vttseg: invalid timestamp %q", s)
	}
	secStr, msStr, ok := strings.Cut(parts[len(parts)-1], ".")
	if !ok || len(msStr) != 3 {
		return 0, fmt.Errorf("vttseg: invalid timestamp %q", s)
	}
	nums := make([]int64, 0, 4)
	for _, p := range append(parts[:len(parts)-1], secStr, msStr) {
		n, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("vttseg: invalid timestamp %q", s)
		}
		nums = append(nums, n)
	}
	ms := nums[len(nums)-1]
	sec := nums[len(nums)-2]
	var min, hour int64
	if len(nums) == 4 {
		hour, min = nums[0], nums[1]
	} else {
		min = nums[0]
	}
	return time.Duration(hour)*time.Hour + time.Duration(min)*time.Minute +
		time.Duration(sec)*time.Second + time.Duration(ms)*time.Millisecond, nil
}

func formatTimestamp(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	ms := d.Milliseconds()
	h := ms / 3600000
	ms %= 3600000
	m := ms / 60000
	ms %= 60000
	sec := ms / 1000
	ms %= 1000
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, sec, ms)
}
