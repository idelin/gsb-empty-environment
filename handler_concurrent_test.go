package llhls

import (
	"net/http"
	"testing"
	"time"
)

func TestSkipParameterProducesDeltaResponse(t *testing.T) {
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

	for i := 0; i < 54; i++ {
		pl.PublishPart(time.Second, true)
		clock.Advance(time.Second)
	}

	res := invokeSync(t, pl, "/playlist.m3u8?_HLS_skip=YES")
	parsed := parsePlaylist(t, res.body)
	if !parsed.hasSkip || parsed.skip != 3 {
		t.Fatalf("delta response SKIPPED-SEGMENTS = %d (present=%v)",
			parsed.skip, parsed.hasSkip)
	}

	// Blocking reload combined with skip: wait for part 0 of the open MSN 9.
	waiting := invoke(pl, "/playlist.m3u8?_HLS_msn=9&_HLS_part=0&_HLS_skip=YES")
	assertPending(t, pl, waiting, "blocking skip request")
	pl.PublishPart(time.Second, true)
	satisfied := assertResult(t, waiting, "blocking skip request")
	delta := parsePlaylist(t, satisfied.body)
	if !delta.hasSkip {
		t.Fatal("satisfied blocking skip response must remain a delta playlist")
	}
	if len(delta.parts) != 1 || delta.parts[0].uri != "part/9.0.m4s" {
		t.Fatalf("expected new part 9.0, got %+v", delta.parts)
	}
}

func TestConcurrentBlockingRequestsEachWakeOnTheirPart(t *testing.T) {
	pl := newTestPlaylist(t, NewVirtualClock())

	// State: MSN 0 open with no parts. Issue four requests at once.
	rPart0 := invoke(pl, "/playlist.m3u8?_HLS_msn=0&_HLS_part=0")
	rPart2 := invoke(pl, "/playlist.m3u8?_HLS_msn=0&_HLS_part=2")
	rSeg1 := invoke(pl, "/playlist.m3u8?_HLS_msn=1")
	rBad := invoke(pl, "/playlist.m3u8?_HLS_msn=5&_HLS_part=0")

	bad := assertResult(t, rBad, "over-ahead request")
	if bad.status != http.StatusBadRequest {
		t.Fatalf("over-ahead status = %d, want 400", bad.status)
	}
	assertPending(t, pl, rPart0, "part 0 request")
	assertPending(t, pl, rPart2, "part 2 request")
	assertPending(t, pl, rSeg1, "segment 1 request")

	pl.PublishPart(time.Second, true)
	res0 := assertResult(t, rPart0, "part 0 request")
	if parsePlaylist(t, res0.body).parts[0].uri != "part/0.0.m4s" {
		t.Fatal("part 0 response missing part/0.0.m4s")
	}
	assertPending(t, pl, rPart2, "part 2 still waiting")
	assertPending(t, pl, rSeg1, "segment 1 still waiting")

	pl.PublishPart(time.Second, false)
	assertPending(t, pl, rPart2, "part 2 after one more part")
	pl.PublishPart(time.Second, false)
	res2 := assertResult(t, rPart2, "part 2 request")
	if got := len(parsePlaylist(t, res2.body).parts); got != 3 {
		t.Fatalf("part 2 response lists %d parts, want 3", got)
	}

	// Finish MSN 0 (3 more parts), then complete MSN 1.
	for i := 0; i < 3; i++ {
		pl.PublishPart(time.Second, i == 0)
	}
	assertPending(t, pl, rSeg1, "segment 1 while MSN 1 open")
	for i := 0; i < 6; i++ {
		pl.PublishPart(time.Second, i == 0)
	}
	resSeg1 := assertResult(t, rSeg1, "segment 1 request")
	if resSeg1.status != http.StatusOK {
		t.Fatalf("segment 1 status = %d", resSeg1.status)
	}
	parsed := parsePlaylist(t, resSeg1.body)
	found := false
	for _, seg := range parsed.segments {
		if seg.uri == "segment/1.m4s" {
			found = true
		}
	}
	if !found {
		t.Fatal("segment 1 missing from final response")
	}
}

func TestManyConcurrentBlockersAllWakeOnce(t *testing.T) {
	pl := newTestPlaylist(t, NewVirtualClock())
	const n = 8

	waiters := make([]<-chan httpResult, n)
	for i := range waiters {
		waiters[i] = invoke(pl, "/playlist.m3u8?_HLS_msn=0&_HLS_part=0")
	}
	for i, ch := range waiters {
		assertPending(t, pl, ch, "waiter before publish")
		_ = i
	}

	pl.PublishPart(time.Second, true)

	for i, ch := range waiters {
		res := assertResult(t, ch, "waiter after publish")
		if res.status != http.StatusOK {
			t.Fatalf("waiter %d status = %d", i, res.status)
		}
		parsed := parsePlaylist(t, res.body)
		if len(parsed.parts) != 1 || parsed.parts[0].uri != "part/0.0.m4s" {
			t.Fatalf("waiter %d got parts %+v", i, parsed.parts)
		}
	}
}
