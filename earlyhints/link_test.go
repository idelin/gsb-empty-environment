package earlyhints

import (
	"reflect"
	"testing"
)

func TestParseLinkHeader(t *testing.T) {
	values := []string{
		`</app.js>; rel=preload; as=script, <https://cdn.example.com/font.woff2>; rel="preload"; type="font/woff2"`,
		`<https://api.example.com>; rel=preconnect; crossorigin`,
	}
	links := ParseLinkHeader(values)
	if len(links) != 3 {
		t.Fatalf("expected 3 links, got %d: %+v", len(links), links)
	}
	if links[0].Target != "/app.js" || links[0].Params["rel"] != "preload" || links[0].Params["as"] != "script" {
		t.Fatalf("first link parsed incorrectly: %+v", links[0])
	}
	if links[1].Target != "https://cdn.example.com/font.woff2" || links[1].Params["type"] != "font/woff2" {
		t.Fatalf("second link parsed incorrectly: %+v", links[1])
	}
	if links[2].Params["crossorigin"] != "" {
		t.Fatalf("valueless parameter parsed incorrectly: %+v", links[2])
	}
}

func TestFilterLinkValues(t *testing.T) {
	allowed := map[string]bool{"cdn.example.com": true, "api.example.com": true}
	links := ParseLinkHeader([]string{
		`<https://cdn.example.com/app.js>; rel=preload; as=script`,
		`<https://evil.example.org/x.js>; rel=preload`,
		`<http://cdn.example.com/insecure.js>; rel=preload`,
		`<https://cdn.example.com/style.css>; rel=stylesheet`,
		`<https://api.example.com>; rel=preconnect`,
		`</relative.js>; rel=preload`,
		`<https://cdn.example.com/mixed>; rel="preload stylesheet"; as=style`,
		`<HTTPS://CDN.EXAMPLE.COM/upper.js>; rel=PRELOAD`,
	})
	got := FilterLinkValues(links, allowed)
	want := []string{
		`<https://cdn.example.com/app.js>; rel=preload; as=script`,
		`<https://api.example.com>; rel=preconnect`,
		`<https://cdn.example.com/mixed>; rel="preload stylesheet"; as=style`,
		`<HTTPS://CDN.EXAMPLE.COM/upper.js>; rel=preload`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filter mismatch:\n got: %v\nwant: %v", got, want)
	}
}

func TestFilterLinkValuesDropsMalformed(t *testing.T) {
	allowed := map[string]bool{"cdn.example.com": true}
	links := ParseLinkHeader([]string{
		`garbage without brackets`,
		`<https://cdn.example.com/good.js>; rel=preload`,
		`<missing-closing-bracket; rel=preload`,
	})
	got := FilterLinkValues(links, allowed)
	if len(got) != 1 || got[0] != `<https://cdn.example.com/good.js>; rel=preload` {
		t.Fatalf("unexpected filter result: %v", got)
	}
}

func TestParseLinkHeaderRespectsQuotedComma(t *testing.T) {
	links := ParseLinkHeader([]string{
		`<https://cdn.example.com/a.js>; rel=preload; title="a, b", <https://cdn.example.com/b.js>; rel=preconnect`,
	})
	if len(links) != 2 {
		t.Fatalf("expected 2 links, got %d: %+v", len(links), links)
	}
	if links[0].Params["title"] != "a, b" {
		t.Fatalf("quoted comma mishandled: %+v", links[0])
	}
}
