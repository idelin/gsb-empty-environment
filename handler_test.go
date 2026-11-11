package llhls

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNonBlockingRequestReturnsCurrentPlaylist(t *testing.T) {
	pl := newTestPlaylist(t, NewVirtualClock())

	req := httptest.NewRequest(http.MethodGet, "/playlist.m3u8", nil)
	rec := httptest.NewRecorder()
	pl.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != ContentType {
		t.Fatalf("Content-Type = %q, want %q", ct, ContentType)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", cc)
	}
	parsed := parsePlaylist(t, rec.Body.String())
	if parsed.preloadURI != "part/0.0.m4s" {
		t.Fatalf("preload = %q", parsed.preloadURI)
	}
}

func TestBlockingPartRequestWaitsUntilPartAppears(t *testing.T) {
	pl := newTestPlaylist(t, NewVirtualClock())

	waiting := invoke(pl, "/playlist.m3u8?_HLS_msn=0&_HLS_part=1")
	assertPending(t, pl, waiting, "blocking part request")

	// Part 0 alone must not satisfy a request for part 1.
	pl.PublishPart(time.Second, true)
	assertPending(t, pl, waiting, "request after part 0")

	pl.PublishPart(time.Second, false)
	res := assertResult(t, waiting, "blocking part request")
	if res.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.status)
	}
	parsed := parsePlaylist(t, res.body)
	if len(parsed.parts) < 2 {
		t.Fatalf("response has %d parts, want at least 2", len(parsed.parts))
	}
	if parsed.parts[1].uri != "part/0.1.m4s" {
		t.Fatalf("second part uri = %q", parsed.parts[1].uri)
	}
}

func TestBlockingSegmentRequestWaitsForCompletion(t *testing.T) {
	clock := NewVirtualClock()
	pl := newTestPlaylist(t, clock)

	// MSN 1 is one past the open MSN 0: allowed, but the segment completes
	// only after MSN 0 rolls over and six parts have filled MSN 1.
	waiting := invoke(pl, "/playlist.m3u8?_HLS_msn=1")
	assertPending(t, pl, waiting, "blocking segment request")

	for i := 0; i < 6; i++ {
		pl.PublishPart(time.Second, true)
		clock.Advance(time.Second)
	}
	// MSN 1 is now open with its preload hint but not complete, so the
	// request for the full segment must still be blocked.
	assertPending(t, pl, waiting, "request while MSN 1 is open with no parts")

	// Parts filling MSN 1 do not complete it either.
	for i := 0; i < 3; i++ {
		pl.PublishPart(time.Second, true)
	}
	assertPending(t, pl, waiting, "request while MSN 1 has partial parts")

	for i := 0; i < 5; i++ {
		pl.PublishPart(time.Second, true)
		clock.Advance(time.Second)
	}
	assertPending(t, pl, waiting, "request before MSN 1 completes")
	pl.PublishPart(time.Second, true)
	res := assertResult(t, waiting, "blocking segment request")

	parsed := parsePlaylist(t, res.body)
	found := false
	for _, seg := range parsed.segments {
		if seg.uri == "segment/1.m4s" {
			found = true
			if seg.extinf != 6.0 {
				t.Fatalf("MSN 1 EXTINF = %f, want 6.0", seg.extinf)
			}
		}
	}
	if !found {
		t.Fatalf("completed MSN 1 missing from response:\n%s", res.body)
	}
}

func TestRequestTooFarAheadReturnsBadRequest(t *testing.T) {
	pl := newTestPlaylist(t, NewVirtualClock())

	res := invokeSync(t, pl, "/playlist.m3u8?_HLS_msn=2&_HLS_part=0")
	if res.status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.status)
	}

	// MSN 1 (exactly lastMSN+1) must be accepted and block, not 400.
	waiting := invoke(pl, "/playlist.m3u8?_HLS_msn=1")
	assertPending(t, pl, waiting, "MSN last+1 request")
}

func TestPartWithoutMSNReturnsBadRequest(t *testing.T) {
	pl := newTestPlaylist(t, NewVirtualClock())
	res := invokeSync(t, pl, "/playlist.m3u8?_HLS_part=0")
	if res.status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.status)
	}
}

func TestInvalidParametersReturnBadRequest(t *testing.T) {
	pl := newTestPlaylist(t, NewVirtualClock())
	for _, target := range []string{
		"/playlist.m3u8?_HLS_msn=abc",
		"/playlist.m3u8?_HLS_msn=0&_HLS_part=-2",
		"/playlist.m3u8?_HLS_msn=0&_HLS_part=xx",
	} {
		if res := invokeSync(t, pl, target); res.status != http.StatusBadRequest {
			t.Fatalf("%s -> status %d, want 400", target, res.status)
		}
	}
}

func TestAgedOutMSNReturnsImmediately(t *testing.T) {
	clock := NewVirtualClock()
	pl := newTestPlaylist(t, clock)

	for i := 0; i < 24; i++ {
		pl.PublishPart(time.Second, true)
		clock.Advance(time.Second)
	}
	if pl.FirstMSN() != 1 {
		t.Fatalf("precondition: FirstMSN = %d", pl.FirstMSN())
	}

	// MSN 0 has been pruned: answer immediately with the current playlist.
	res := invokeSync(t, pl, "/playlist.m3u8?_HLS_msn=0&_HLS_part=0")
	if res.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.status)
	}
	if strings.Contains(res.body, "segment/0.m4s") {
		t.Fatal("aged-out MSN 0 must not appear in the current playlist")
	}
}

func TestBlockingRequestTimesOutWithCurrentPlaylist(t *testing.T) {
	clock := NewVirtualClock()
	pl, err := New(Config{
		TargetDuration: 6 * time.Second,
		PartTarget:     time.Second,
		KeepSegments:   3,
		BlockTimeout:   2 * time.Second,
		Clock:          clock,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// No encoder runs, so the requested part never appears.
	waiting := invoke(pl, "/playlist.m3u8?_HLS_msn=0&_HLS_part=3")
	assertPending(t, pl, waiting, "blocking request before timeout")

	clock.Advance(2 * time.Second)
	res := assertResult(t, waiting, "timed-out request")
	if res.status != http.StatusOK {
		t.Fatalf("timeout status = %d, want 200 with current playlist", res.status)
	}
	parsed := parsePlaylist(t, res.body)
	if parsed.preloadURI != "part/0.0.m4s" {
		t.Fatalf("timeout response should carry current playlist, got %q",
			parsed.preloadURI)
	}
}

func TestCanceledBlockingRequestUnblocks(t *testing.T) {
	pl := newTestPlaylist(t, NewVirtualClock())
	ctx, cancel := context.WithCancel(context.Background())

	ch := invokeCtx(pl, ctx, "/playlist.m3u8?_HLS_msn=0&_HLS_part=0")
	assertPending(t, pl, ch, "request before cancel")
	cancel()

	select {
	case res := <-ch:
		// Cancellation surfaces either as a transport-less 200 (the handler
		// writes the current playlist when the context channel closes) or as
		// recorder success; the key contract is that the goroutine returns.
		if res.status != http.StatusOK {
			t.Fatalf("canceled request status = %d, want 200", res.status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled request never returned")
	}
}

func TestMethodNotAllowedAndHead(t *testing.T) {
	pl := newTestPlaylist(t, NewVirtualClock())

	post := httptest.NewRequest(http.MethodPost, "/playlist.m3u8", nil)
	rec := httptest.NewRecorder()
	pl.ServeHTTP(rec, post)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow != "GET, HEAD" {
		t.Fatalf("Allow = %q, want GET, HEAD", allow)
	}

	head := httptest.NewRequest(http.MethodHead, "/playlist.m3u8", nil)
	headRec := httptest.NewRecorder()
	pl.ServeHTTP(headRec, head)
	if headRec.Code != http.StatusOK ||
		headRec.Header().Get("Content-Type") != ContentType {
		t.Fatalf("HEAD status=%d content-type=%q",
			headRec.Code, headRec.Header().Get("Content-Type"))
	}
	if headRec.Body.Len() != 0 {
		t.Fatal("HEAD response must have no body")
	}
}
