package netsec

import (
	"errors"
	"net/url"
	"testing"
)

func TestOriginNormalization(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want Origin
	}{
		{
			name: "default http port",
			raw:  "HTTP://Example.COM:80/module.zip",
			want: Origin{Scheme: "http", Host: "example.com", Port: 80},
		},
		{
			name: "default https port",
			raw:  "https://example.com:443/",
			want: Origin{Scheme: "https", Host: "example.com", Port: 443},
		},
		{
			name: "trailing root dot",
			raw:  "https://Example.COM./module",
			want: Origin{Scheme: "https", Host: "example.com", Port: 443},
		},
		{
			name: "non-default port",
			raw:  "https://example.com:8443/module",
			want: Origin{Scheme: "https", Host: "example.com", Port: 8443},
		},
		{
			name: "ipv6 default port",
			raw:  "https://[2001:db8::1]:443/",
			want: Origin{Scheme: "https", Host: "2001:db8::1", Port: 443},
		},
		{
			name: "ipv4 mapped ipv6",
			raw:  "http://[::ffff:127.0.0.1]:8080/",
			want: Origin{Scheme: "http", Host: "127.0.0.1", Port: 8080},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseOrigin(test.raw)
			if err != nil {
				t.Fatalf("ParseOrigin(%q): %v", test.raw, err)
			}
			if got != test.want {
				t.Fatalf("ParseOrigin(%q) = %#v, want %#v", test.raw, got, test.want)
			}
		})
	}
}

func TestOriginEqualityIncludesSchemeAndPort(t *testing.T) {
	t.Parallel()

	httpOrigin := MustParseOrigin("http://example.com:80")
	httpsOrigin := MustParseOrigin("https://example.com:443")
	otherPort := MustParseOrigin("http://example.com:8080")

	if !httpOrigin.Equal(MustParseOrigin("HTTP://EXAMPLE.COM.:80/")) {
		t.Fatal("equivalent spellings should compare equal")
	}
	if httpOrigin.Equal(httpsOrigin) {
		t.Fatal("different schemes must not compare equal")
	}
	if httpOrigin.Equal(otherPort) {
		t.Fatal("different ports must not compare equal")
	}
}

func TestParseOriginRejectsUnsafeOrAmbiguousInput(t *testing.T) {
	t.Parallel()

	tests := []string{
		"https://user:secret@example.com/module",
		"ftp://example.com/module",
		"http:///module",
		"http://example.com:0/module",
		"http://example.com:65536/module",
		"http://example.com:/module",
	}
	for _, raw := range tests {
		if _, err := ParseOrigin(raw); err == nil {
			t.Errorf("ParseOrigin(%q) unexpectedly succeeded", raw)
		} else if !errors.Is(err, ErrInvalidOrigin) {
			t.Errorf("ParseOrigin(%q) error = %v, want ErrInvalidOrigin", raw, err)
		}
	}
}

func TestOriginFromURLIgnoresUserinfoForTargetMatching(t *testing.T) {
	t.Parallel()

	u, err := url.Parse("https://attacker:stolen@mirror.example/module.zip")
	if err != nil {
		t.Fatal(err)
	}
	origin, err := OriginFromURL(u)
	if err != nil {
		t.Fatal(err)
	}
	if want := (Origin{Scheme: "https", Host: "mirror.example", Port: 443}); origin != want {
		t.Fatalf("OriginFromURL() = %#v, want %#v", origin, want)
	}
}
