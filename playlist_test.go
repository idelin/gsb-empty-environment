package llhls

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func newTestPlaylist(t *testing.T, clock Clock) *Playlist {
	t.Helper()
	pl, err := New(Config{
		TargetDuration: 6 * time.Second,
		PartTarget:     time.Second,
		KeepSegments:   3,
		Clock:          clock,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return pl
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	cases := []Config{
		{TargetDuration: 0, PartTarget: time.Second, KeepSegments: 3},
		{TargetDuration: 500 * time.Millisecond, PartTarget: time.Second, KeepSegments: 3},
		{TargetDuration: 6 * time.Second, PartTarget: 0, KeepSegments: 3},
		{TargetDuration: 6 * time.Second, PartTarget: 1500 * time.Millisecond, KeepSegments: 3},
		{TargetDuration: 6 * time.Second, PartTarget: time.Second, KeepSegments: 1},
		{
			TargetDuration: 6 * time.Second,
			PartTarget:     time.Second,
			KeepSegments:   3,
			CanSkipUntil:   5 * time.Second,
		},
	}
	for i, cfg := range cases {
		if _, err := New(cfg); err == nil {
			t.Fatalf("case %d: expected error for %#v", i, cfg)
		}
	}
}

func TestInitialPlaylistContainsLLHLSTagsAndPreloadHint(t *testing.T) {
	pl := newTestPlaylist(t, NewVirtualClock())

	parsed := parsePlaylist(t, string(pl.render(false)))
	if parsed.version != 9 {
		t.Fatalf("version = %d, want 9", parsed.version)
	}
	if parsed.target != 6 {
		t.Fatalf("target = %d, want 6", parsed.target)
	}
	if parsed.partTarget != 1 {
		t.Fatalf("part target = %d, want 1", parsed.partTarget)
	}
	if parsed.mediaSequence != 0 {
		t.Fatalf("media sequence = %d, want 0", parsed.mediaSequence)
	}
	if !strings.Contains(parsed.serverControl, "CAN-BLOCK-RELOAD=YES") ||
		!strings.Contains(parsed.serverControl, "PART-HOLD-BACK=3.000") ||
		!strings.Contains(parsed.serverControl, "CAN-SKIP-UNTIL=36.000") {
		t.Fatalf("SERVER-CONTROL missing fields: %q", parsed.serverControl)
	}
	if len(parsed.segments) != 0 || len(parsed.parts) != 0 {
		t.Fatal("fresh playlist must have no complete segments or parts")
	}
	if parsed.preloadURI != "part/0.0.m4s" {
		t.Fatalf("preload hint = %q, want part/0.0.m4s", parsed.preloadURI)
	}
}

func TestPublishPartsRendersInProgressSegment(t *testing.T) {
	clock := NewVirtualClock()
	pl := newTestPlaylist(t, clock)

	for i := 0; i < 3; i++ {
		part := pl.PublishPart(time.Second, i == 0)
		if part.URI != "part/0."+strconv.Itoa(i)+".m4s" {
			t.Fatalf("part %d uri = %q", i, part.URI)
		}
		clock.Advance(time.Second)
	}

	parsed := parsePlaylist(t, string(pl.render(false)))
	if len(parsed.parts) != 3 {
		t.Fatalf("parts = %d, want 3", len(parsed.parts))
	}
	if !parsed.parts[0].independent || parsed.parts[1].independent {
		t.Fatal("only the first part should be independent")
	}
	if parsed.parts[2].duration != 1.0 {
		t.Fatalf("part duration = %f, want 1.0", parsed.parts[2].duration)
	}
	if parsed.preloadURI != "part/0.3.m4s" {
		t.Fatalf("preload hint should point at the next part, got %q", parsed.preloadURI)
	}
}

func TestSegmentRollsOverAndPreloadHintAdvances(t *testing.T) {
	clock := NewVirtualClock()
	pl := newTestPlaylist(t, clock)

	for i := 0; i < 6; i++ {
		pl.PublishPart(time.Second, true)
		clock.Advance(time.Second)
	}

	parsed := parsePlaylist(t, string(pl.render(false)))
	if len(parsed.segments) != 1 {
		t.Fatalf("complete segments = %d, want 1", len(parsed.segments))
	}
	if parsed.segments[0].uri != "segment/0.m4s" || parsed.segments[0].extinf != 6.0 {
		t.Fatalf("segment 0 rendered wrong: %+v", parsed.segments[0])
	}
	if parsed.preloadURI != "part/1.0.m4s" {
		t.Fatalf("new segment preload = %q, want part/1.0.m4s", parsed.preloadURI)
	}
	if pl.NextMSN() != 2 {
		t.Fatalf("NextMSN = %d, want 2", pl.NextMSN())
	}
}

func TestPruningKeepsSequenceNumbersMonotonic(t *testing.T) {
	clock := NewVirtualClock()
	pl := newTestPlaylist(t, clock)

	for i := 0; i < 24; i++ {
		pl.PublishPart(time.Second, true)
		clock.Advance(time.Second)
	}

	if pl.FirstMSN() != 1 {
		t.Fatalf("FirstMSN = %d, want 1 (MSN 0 pruned)", pl.FirstMSN())
	}
	parsed := parsePlaylist(t, string(pl.render(false)))
	if parsed.mediaSequence != 1 {
		t.Fatalf("MEDIA-SEQUENCE = %d, want 1", parsed.mediaSequence)
	}
	for _, seg := range parsed.segments {
		if seg.uri == "segment/0.m4s" {
			t.Fatal("pruned segment 0 must not be rendered")
		}
	}
	if parsed.preloadURI != "part/4.0.m4s" {
		t.Fatalf("preload = %q, want part/4.0.m4s", parsed.preloadURI)
	}

	for i := 0; i < 6; i++ {
		pl.PublishPart(time.Second, true)
		clock.Advance(time.Second)
	}
	if pl.FirstMSN() != 2 {
		t.Fatalf("FirstMSN = %d, want 2", pl.FirstMSN())
	}

	// All retained MSNs must be strictly increasing and contiguous.
	body := string(pl.render(false))
	for msn := uint64(0); msn < pl.FirstMSN(); msn++ {
		if strings.Contains(body, "segment/"+strconv.FormatUint(msn, 10)+".m4s") {
			t.Fatalf("aged MSN %d still listed", msn)
		}
	}
}

func TestSkipDeltaReplacesOldSegmentsWithSkipTag(t *testing.T) {
	clock := NewVirtualClock()
	pl, err := New(Config{
		TargetDuration: 6 * time.Second,
		PartTarget:     time.Second,
		KeepSegments:   10,
		Clock:          clock,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// 54 parts -> 9 complete segments (0..8) plus open MSN 9.
	for i := 0; i < 54; i++ {
		pl.PublishPart(time.Second, true)
		clock.Advance(time.Second)
	}

	full := parsePlaylist(t, string(pl.render(false)))
	delta := parsePlaylist(t, string(pl.render(true)))

	// CAN-SKIP-UNTIL is 36s. Edge is t=54s, so cutoff is t=18s. Segments
	// ending at or before the cutoff are MSN 0..2 (end 6/12/18s): three
	// skipped; at least one finished segment stays listed.
	if !delta.hasSkip || delta.skip != 3 {
		t.Fatalf("SKIPPED-SEGMENTS = %d (present=%v), want 3", delta.skip, delta.hasSkip)
	}
	if delta.mediaSequence != full.mediaSequence {
		t.Fatal("delta update must keep the same MEDIA-SEQUENCE number")
	}
	if len(delta.segments) != len(full.segments)-3 {
		t.Fatalf("delta lists %d segments, full lists %d", len(delta.segments), len(full.segments))
	}
	if delta.segments[0].uri != "segment/3.m4s" {
		t.Fatalf("first retained segment = %q, want segment/3.m4s", delta.segments[0].uri)
	}
	if delta.preloadURI != full.preloadURI {
		t.Fatal("delta update must keep the live-edge preload hint")
	}

	// Without the skip parameter no SKIP tag is emitted and all segments stay.
	if full.hasSkip {
		t.Fatal("non-delta playlist must not contain EXT-X-SKIP")
	}
}

func TestSkipDeltaWithoutEnoughHistoryEmitsNoTag(t *testing.T) {
	clock := NewVirtualClock()
	pl := newTestPlaylist(t, clock)

	for i := 0; i < 12; i++ {
		pl.PublishPart(time.Second, true)
		clock.Advance(time.Second)
	}

	delta := parsePlaylist(t, string(pl.render(true)))
	if delta.hasSkip {
		t.Fatalf("no segment should be skippable yet, got SKIP=%d", delta.skip)
	}
}

func TestPartCountReportsMissingSegments(t *testing.T) {
	clock := NewVirtualClock()
	pl := newTestPlaylist(t, clock)

	if count, ok := pl.PartCount(99); ok || count != 0 {
		t.Fatalf("future MSN: count=%d ok=%v, want 0/false", count, ok)
	}

	pl.PublishPart(time.Second, true)
	count, ok := pl.PartCount(0)
	if !ok || count != 1 {
		t.Fatalf("MSN 0: count=%d ok=%v, want 1/true", count, ok)
	}

	// Prune MSN 0 away, then it must report missing again.
	for i := 0; i < 23; i++ {
		pl.PublishPart(time.Second, true)
		clock.Advance(time.Second)
	}
	if count, ok := pl.PartCount(0); ok || count != 0 {
		t.Fatalf("pruned MSN: count=%d ok=%v, want 0/false", count, ok)
	}
}
