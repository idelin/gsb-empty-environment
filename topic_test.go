package netsec

import (
	"errors"
	"testing"
)

func TestTopicMatching(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		binding    string
		routingKey string
		want       bool
	}{
		{name: "exact", binding: "tenant-a.orders.created", routingKey: "tenant-a.orders.created", want: true},
		{name: "exact segment boundary", binding: "tenant-a.orders", routingKey: "tenant-a.orders.created", want: false},
		{name: "exact prefix is not a segment prefix", binding: "tenant-a.orders.created", routingKey: "tenant-a.orders.createdx", want: false},
		{name: "single matches one", binding: "tenant-a.orders.*", routingKey: "tenant-a.orders.created", want: true},
		{name: "single does not match zero", binding: "tenant-a.orders.*", routingKey: "tenant-a.orders", want: false},
		{name: "single does not match two", binding: "tenant-a.orders.*", routingKey: "tenant-a.orders.created.eu", want: false},
		{name: "single matches whole segment even with prefix", binding: "tenant-a.orders.*", routingKey: "tenant-a.orders.createdx", want: true},
		{name: "multi matches zero", binding: "tenant-a.orders.#", routingKey: "tenant-a.orders", want: true},
		{name: "multi matches one", binding: "tenant-a.orders.#", routingKey: "tenant-a.orders.created", want: true},
		{name: "multi matches many", binding: "tenant-a.orders.#", routingKey: "tenant-a.orders.created.eu.1", want: true},
		{name: "multi respects boundary", binding: "tenant-a.orders.#", routingKey: "tenant-a.ordersx.created", want: false},
		{name: "multi in middle", binding: "tenant-a.#.created", routingKey: "tenant-a.orders.eu.created", want: true},
		{name: "multi in middle zero", binding: "tenant-a.#.created", routingKey: "tenant-a.created", want: true},
		{name: "multi in middle wrong suffix", binding: "tenant-a.#.created", routingKey: "tenant-a.orders.eu.cancelled", want: false},
		{name: "standalone multi matches empty", binding: "#", routingKey: "", want: true},
		{name: "standalone multi matches deep", binding: "#", routingKey: "a.b.c", want: true},
		{name: "standalone single does not match empty", binding: "*", routingKey: "", want: false},
		{name: "standalone single matches one", binding: "*", routingKey: "a", want: true},
		{name: "standalone single does not match two", binding: "*", routingKey: "a.b", want: false},
		{name: "routing wildcard rejected", binding: "#", routingKey: "tenant-a.#", want: false},
		{name: "malformed binding rejected", binding: "tenant-a..orders", routingKey: "tenant-a..orders", want: false},
		{name: "partial wildcard binding rejected", binding: "tenant-a.order*", routingKey: "tenant-a.orders", want: false},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := MatchBinding(test.binding, test.routingKey); got != test.want {
				t.Fatalf("MatchBinding(%q, %q) = %v, want %v", test.binding, test.routingKey, got, test.want)
			}
		})
	}
}

func TestParseRejectsMalformedTopics(t *testing.T) {
	t.Parallel()

	for _, binding := range []string{".a", "a.", "a..b", "a*b", "a#b", "a.*b"} {
		if _, err := ParsePattern(binding); !errors.Is(err, ErrInvalidPattern) {
			t.Errorf("ParsePattern(%q) error = %v, want ErrInvalidPattern", binding, err)
		}
	}
	for _, routingKey := range []string{".a", "a.", "a..b", "a.*", "a.#", "a*b"} {
		if _, err := ParseTopic(routingKey); !errors.Is(err, ErrInvalidRoutingKey) {
			t.Errorf("ParseTopic(%q) error = %v, want ErrInvalidRoutingKey", routingKey, err)
		}
	}

	for _, key := range []string{"", "a", "a.b", "*", "#", "a.*.b", "a.#.b"} {
		if _, err := ParsePattern(key); err != nil {
			t.Errorf("ParsePattern(%q) unexpectedly failed: %v", key, err)
		}
	}
}

func TestPatternCoverage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		allowed   string
		requested string
		want      bool
	}{
		{name: "exact covers itself", allowed: "tenant-a.orders.created", requested: "tenant-a.orders.created", want: true},
		{name: "exact does not cover longer", allowed: "tenant-a.orders.created", requested: "tenant-a.orders.created.eu", want: false},
		{name: "exact does not cover multi", allowed: "tenant-a.orders.created", requested: "tenant-a.orders.#", want: false},
		{name: "segment prefix does not cover", allowed: "tenant-a.orders", requested: "tenant-a.orders.created", want: false},
		{name: "single prefix covers exact", allowed: "tenant-a.orders.*", requested: "tenant-a.orders.created", want: true},
		{name: "single prefix covers itself", allowed: "tenant-a.orders.*", requested: "tenant-a.orders.*", want: true},
		{name: "single prefix does not cover multi", allowed: "tenant-a.orders.*", requested: "tenant-a.orders.#", want: false},
		{name: "multi prefix covers exact", allowed: "tenant-a.orders.#", requested: "tenant-a.orders.created", want: true},
		{name: "multi prefix covers single", allowed: "tenant-a.orders.#", requested: "tenant-a.orders.*", want: true},
		{name: "multi prefix covers itself", allowed: "tenant-a.orders.#", requested: "tenant-a.orders.#", want: true},
		{name: "multi prefix does not cover parent", allowed: "tenant-a.orders.#", requested: "tenant-a.#", want: false},
		{name: "standalone multi covers everything", allowed: "#", requested: "#", want: true},
		{name: "single multi does not cover empty", allowed: "*.#", requested: "#", want: false},
		{name: "multi middle covers exact", allowed: "a.#.b", requested: "a.c.b", want: true},
		{name: "multi middle covers single", allowed: "a.#.b", requested: "a.*.b", want: true},
		{name: "multi middle does not cover wrong suffix", allowed: "a.#.b", requested: "a.#.c", want: false},
		{name: "deeper allowed does not cover broader", allowed: "tenant-a.orders.created.#", requested: "tenant-a.orders.#", want: false},
		{name: "single plus multi covers nonempty multi suffix", allowed: "*.#", requested: "#.a", want: true},
		{name: "single plus multi covers literal multi suffix", allowed: "*.#", requested: "a.#", want: true},
		{name: "ending literal does not cover trailing multi", allowed: "#.a", requested: "#.a.#", want: false},
		{name: "prefix literal does not cover leading multi", allowed: "a.#", requested: "#.a", want: false},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			allowed, err := ParsePattern(test.allowed)
			if err != nil {
				t.Fatalf("ParsePattern(%q): %v", test.allowed, err)
			}
			requested, err := ParsePattern(test.requested)
			if err != nil {
				t.Fatalf("ParsePattern(%q): %v", test.requested, err)
			}
			if got := allowed.Covers(requested); got != test.want {
				t.Fatalf("%q covers %q = %v, want %v", test.allowed, test.requested, got, test.want)
			}
		})
	}
}
