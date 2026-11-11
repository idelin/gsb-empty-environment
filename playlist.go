package llhls

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ContentType is the media playlist content type required by HLS.
const ContentType = "application/vnd.apple.mpegurl"

// Config configures a Playlist. Durations must be whole seconds because the
// HLS playlist format expresses TARGETDURATION and PART-TARGET in seconds.
type Config struct {
	// TargetDuration is #EXT-X-TARGETDURATION: the nominal segment duration.
	TargetDuration time.Duration
	// PartTarget is #EXT-X-PART-INF:PART-TARGET, the nominal part duration.
	PartTarget time.Duration
	// KeepSegments is the number of finished segments retained alongside the
	// in-progress segment. Must be at least 2.
	KeepSegments int
	// CanSkipUntil is the CAN-SKIP-UNTIL window. Zero defaults to
	// 6*TargetDuration per the HLS spec.
	CanSkipUntil time.Duration
	// PartHoldBack is advertised as PART-HOLD-BACK. Zero defaults to
	// 3*PartTarget.
	PartHoldBack time.Duration
	// BlockTimeout bounds how long a blocking request waits. Zero defaults to
	// TargetDuration. On timeout the current playlist is returned with 200.
	BlockTimeout time.Duration
	// SegmentURI builds a segment URI for a media sequence number.
	SegmentURI func(msn uint64) string
	// PartURI builds a partial segment URI for an MSN and 0-based part index.
	PartURI func(msn uint64, part int) string
	// Clock drives blocking-request timers. Defaults to the wall clock.
	Clock Clock
}

// Part is one #EXT-X-PART entry of the in-progress segment.
type Part struct {
	URI         string
	Duration    time.Duration
	Independent bool
}

type segment struct {
	msn      uint64
	start    time.Time
	parts    []Part
	duration time.Duration
	complete bool
}

// Playlist maintains the LL-HLS media playlist state. It is safe for
// concurrent use: encoders mutate it while HTTP handlers render snapshots and
// block on reload requests.
type Playlist struct {
	cfg      Config
	mu       sync.Mutex
	segs     []*segment
	notify   chan struct{}
	blockers int
}

// New validates the configuration and returns a Playlist with segment 0
// already open, so the first published part lands in MSN 0.
func New(cfg Config) (*Playlist, error) {
	if cfg.TargetDuration <= 0 || cfg.TargetDuration%time.Second != 0 {
		return nil, errors.New("llhls: TargetDuration must be a positive whole-second duration")
	}
	if cfg.PartTarget <= 0 || cfg.PartTarget%time.Second != 0 {
		return nil, errors.New("llhls: PartTarget must be a positive whole-second duration")
	}
	if cfg.KeepSegments < 2 {
		return nil, errors.New("llhls: KeepSegments must be at least 2")
	}
	if cfg.CanSkipUntil == 0 {
		cfg.CanSkipUntil = 6 * cfg.TargetDuration
	}
	if cfg.CanSkipUntil < 6*cfg.TargetDuration {
		return nil, errors.New("llhls: CanSkipUntil must be at least 6*TargetDuration")
	}
	if cfg.PartHoldBack == 0 {
		cfg.PartHoldBack = 3 * cfg.PartTarget
	}
	if cfg.BlockTimeout == 0 {
		cfg.BlockTimeout = cfg.TargetDuration
	}
	if cfg.SegmentURI == nil {
		cfg.SegmentURI = func(msn uint64) string { return fmt.Sprintf("segment/%d.m4s", msn) }
	}
	if cfg.PartURI == nil {
		cfg.PartURI = func(msn uint64, part int) string {
			return fmt.Sprintf("part/%d.%d.m4s", msn, part)
		}
	}
	if cfg.Clock == nil {
		cfg.Clock = NewRealClock()
	}
	p := &Playlist{notify: make(chan struct{})}
	p.cfg = cfg
	p.segs = []*segment{{msn: 0, start: cfg.Clock.Now()}}
	return p, nil
}

// PublishPart appends a partial segment to the in-progress segment. When the
// segment reaches TargetDuration it is finalized and a new segment opens
// immediately, which also makes its preload hint available. The returned
// Part carries the generated URI.
func (p *Playlist) PublishPart(dur time.Duration, independent bool) Part {
	p.mu.Lock()
	defer p.mu.Unlock()

	cur := p.segs[len(p.segs)-1]
	part := Part{
		URI:         p.cfg.PartURI(cur.msn, len(cur.parts)),
		Duration:    dur,
		Independent: independent,
	}
	cur.parts = append(cur.parts, part)
	cur.duration += dur

	if cur.duration >= p.cfg.TargetDuration {
		cur.complete = true
		p.segs = append(p.segs, &segment{
			msn:   cur.msn + 1,
			start: p.cfg.Clock.Now(),
		})
		p.pruneLocked()
	}
	p.broadcastLocked()
	return part
}

// pruneLocked drops finished segments beyond KeepSegments. The in-progress
// segment is never removed and media sequence numbers keep increasing.
func (p *Playlist) pruneLocked() {
	drop := len(p.segs) - 1 - p.cfg.KeepSegments
	if drop <= 0 {
		return
	}
	p.segs = p.segs[drop:]
}

func (p *Playlist) broadcastLocked() {
	close(p.notify)
	p.notify = make(chan struct{})
}

// NextMSN returns the MSN that will be assigned to the next segment to open.
// The currently open segment therefore has MSN NextMSN()-1.
func (p *Playlist) NextMSN() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.segs[len(p.segs)-1].msn + 1
}

// FirstMSN returns the MSN of the oldest retained segment. It never decreases.
func (p *Playlist) FirstMSN() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.segs[0].msn
}

// Blockers returns the number of requests currently waiting in a blocking
// reload. It is primarily intended for tests to synchronize on.
func (p *Playlist) Blockers() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.blockers
}

// PartCount returns how many parts have been published for msn and whether
// that segment is still retained.
func (p *Playlist) PartCount(msn uint64) (count int, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if seg := p.findLocked(msn); seg != nil {
		return len(seg.parts), true
	}
	return 0, false
}

func (p *Playlist) findLocked(msn uint64) *segment {
	first := p.segs[0].msn
	if msn < first {
		return nil
	}
	idx := int(msn - first)
	if idx >= len(p.segs) {
		return nil
	}
	return p.segs[idx]
}

// wait blocks until cond reports the requested item is available, the timer
// fires, or stop is closed. The caller holds no locks; cond is evaluated
// under the playlist lock on every state change.
func (p *Playlist) wait(cond func() bool, timer Timer, stop <-chan struct{}) bool {
	p.mu.Lock()
	p.blockers++
	defer func() {
		p.mu.Lock()
		p.blockers--
		p.mu.Unlock()
	}()
	for {
		if cond() {
			p.mu.Unlock()
			return true
		}
		ch := p.notify
		p.mu.Unlock()

		select {
		case <-ch:
			// Re-evaluate under the lock on the next iteration; a closed
			// notify channel simply means "state may have changed".
			p.mu.Lock()
		case <-timer.C():
			return false
		case <-stop:
			return false
		}
	}
}

// isReadyLocked evaluates a blocking request against the current state.
// msn is the requested _HLS_msn; part is -1 when _HLS_part was absent.
func (p *Playlist) isReadyLocked(msn uint64, part int) bool {
	seg := p.findLocked(msn)
	if seg == nil {
		return false
	}
	if part < 0 {
		return seg.complete
	}
	return len(seg.parts) > part
}

// isAgedOutLocked reports whether msn has already left the retention window.
func (p *Playlist) isAgedOutLocked(msn uint64) bool {
	return msn < p.segs[0].msn
}

func formatSeconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', 3, 64)
}

// render builds the playlist text. When delta is true, old segments inside
// the CAN-SKIP-UNTIL window are replaced by a single #EXT-X-SKIP tag.
func (p *Playlist) render(delta bool) []byte {
	p.mu.Lock()
	segs := make([]*segment, len(p.segs))
	copy(segs, p.segs)
	firstMSN := segs[0].msn
	last := segs[len(segs)-1]
	p.mu.Unlock()

	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:9\n")
	b.WriteString("#EXT-X-TARGETDURATION:" + strconv.Itoa(int(p.cfg.TargetDuration/time.Second)) + "\n")
	b.WriteString("#EXT-X-SERVER-CONTROL:CAN-BLOCK-RELOAD=YES,PART-HOLD-BACK=" +
		formatSeconds(p.cfg.PartHoldBack) + ",CAN-SKIP-UNTIL=" +
		formatSeconds(p.cfg.CanSkipUntil) + "\n")
	b.WriteString("#EXT-X-PART-INF:PART-TARGET=" +
		strconv.Itoa(int(p.cfg.PartTarget/time.Second)) + "\n")
	b.WriteString("#EXT-X-MEDIA-SEQUENCE:" + strconv.FormatUint(firstMSN, 10) + "\n")

	skip := 0
	if delta {
		// Live edge: start of the in-progress segment plus its parts.
		edge := last.start
		for _, part := range last.parts {
			edge = edge.Add(part.Duration)
		}
		cutoff := edge.Add(-p.cfg.CanSkipUntil)
		// Keep at least one listed segment besides the in-progress one.
		maxSkip := len(segs) - 2
		for skip < maxSkip {
			seg := segs[skip]
			if !seg.complete || seg.start.Add(seg.duration).After(cutoff) {
				break
			}
			skip++
		}
		if skip > 0 {
			b.WriteString("#EXT-X-SKIP:SKIPPED-SEGMENTS=" + strconv.Itoa(skip) + "\n")
		}
	}

	for _, seg := range segs[skip:] {
		if seg.complete {
			b.WriteString("#EXTINF:" + formatSeconds(seg.duration) + ",\n")
			b.WriteString(p.cfg.SegmentURI(seg.msn) + "\n")
			continue
		}
		for idx, part := range seg.parts {
			line := "#EXT-X-PART:URI=\"" + part.URI + "\",DURATION=" +
				formatSeconds(part.Duration)
			if idx == 0 || part.Independent {
				line += ",INDEPENDENT=YES"
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("#EXT-X-PRELOAD-HINT:TYPE=PART,URI=\"" +
			p.cfg.PartURI(seg.msn, len(seg.parts)) + "\"\n")
	}

	return []byte(b.String())
}
