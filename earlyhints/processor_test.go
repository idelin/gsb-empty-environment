package earlyhints

import (
	"net/http"
	"testing"
)

func TestProcessorFiltersAndStripsHeaders(t *testing.T) {
	allowed := map[string]bool{"cdn.example.com": true}
	p := NewProcessor(allowed, 0)

	upstream := http.Header{}
	upstream.Add("Link", `<https://cdn.example.com/app.js>; rel=preload; as=script`)
	upstream.Add("Link", `<https://evil.example.org/steal.js>; rel=preload`)
	upstream.Set("Set-Cookie", "session=attacker")
	upstream.Set("Content-Length", "42")
	upstream.Set("X-Internal", "secret")

	hint := p.HandleInformational(http.StatusEarlyHints, upstream, 1, 1)
	if hint == nil {
		t.Fatal("expected the hint to be forwarded")
	}
	if len(hint) != 1 {
		t.Fatalf("only Link must survive, got headers: %v", hint)
	}
	if got := hint.Values("Link"); len(got) != 1 || got[0] != `<https://cdn.example.com/app.js>; rel=preload; as=script` {
		t.Fatalf("unexpected Link values: %v", got)
	}
	if hint.Get("Set-Cookie") != "" || hint.Get("Content-Length") != "" || hint.Get("X-Internal") != "" {
		t.Fatalf("forbidden header leaked into hint: %v", hint)
	}
}

func TestProcessorDropsForHTTP10(t *testing.T) {
	p := NewProcessor(map[string]bool{"cdn.example.com": true}, 0)
	upstream := http.Header{}
	upstream.Set("Link", `<https://cdn.example.com/app.js>; rel=preload`)

	if hint := p.HandleInformational(http.StatusEarlyHints, upstream, 1, 0); hint != nil {
		t.Fatalf("HTTP/1.0 client must not receive hints, got: %v", hint)
	}
	if hint := p.HandleInformational(http.StatusEarlyHints, upstream, 0, 9); hint != nil {
		t.Fatalf("HTTP/0.9 client must not receive hints, got: %v", hint)
	}
	if p.Forwarded() != 0 {
		t.Fatalf("dropped hints must not count: %d", p.Forwarded())
	}

	// HTTP/1.1 and HTTP/2 clients are eligible.
	if hint := p.HandleInformational(http.StatusEarlyHints, upstream, 1, 1); hint == nil {
		t.Fatal("HTTP/1.1 client should receive the hint")
	}
	p2 := NewProcessor(map[string]bool{"cdn.example.com": true}, 0)
	if hint := p2.HandleInformational(http.StatusEarlyHints, upstream, 2, 0); hint == nil {
		t.Fatal("HTTP/2 client should receive the hint")
	}
}

func TestProcessorLimitsToThreeHints(t *testing.T) {
	p := NewProcessor(map[string]bool{"cdn.example.com": true}, 0)
	upstream := http.Header{}
	upstream.Set("Link", `<https://cdn.example.com/app.js>; rel=preload`)

	for i := 0; i < 3; i++ {
		if hint := p.HandleInformational(http.StatusEarlyHints, upstream, 1, 1); hint == nil {
			t.Fatalf("hint %d should be forwarded", i+1)
		}
	}
	if p.Forwarded() != 3 {
		t.Fatalf("expected 3 forwarded hints, got %d", p.Forwarded())
	}
	if hint := p.HandleInformational(http.StatusEarlyHints, upstream, 1, 1); hint != nil {
		t.Fatalf("the 4th hint must be dropped, got: %v", hint)
	}
	if p.Forwarded() != 3 {
		t.Fatalf("dropped hint must not increment counter: %d", p.Forwarded())
	}
}

func TestProcessorIgnoresOtherStatusesAndEmptyHints(t *testing.T) {
	p := NewProcessor(map[string]bool{"cdn.example.com": true}, 0)

	if hint := p.HandleInformational(http.StatusContinue, http.Header{}, 1, 1); hint != nil {
		t.Fatalf("100 Continue must not be turned into a hint: %v", hint)
	}

	noLink := http.Header{}
	noLink.Set("Set-Cookie", "x=1")
	if hint := p.HandleInformational(http.StatusEarlyHints, noLink, 1, 1); hint != nil {
		t.Fatalf("hint without safe links must be dropped: %v", hint)
	}

	blocked := http.Header{}
	blocked.Set("Link", `<https://evil.example.org/x.js>; rel=preload`)
	if hint := p.HandleInformational(http.StatusEarlyHints, blocked, 1, 1); hint != nil {
		t.Fatalf("cross-host hint must be dropped: %v", hint)
	}
	if p.Forwarded() != 0 {
		t.Fatalf("nothing should have been forwarded: %d", p.Forwarded())
	}
}
