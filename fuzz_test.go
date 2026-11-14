package netsec

import "testing"

func FuzzParseAddress(f *testing.F) {
	seeds := []string{
		"::ffff:169.254.169.254",
		"::169.254.169.254",
		"2002:a9fe:a9fe::1",
		"2001:0:4136:e378:8000:63bf:5601:5601",
		"fe80::1%eth0",
		"[fe80::1%25eth0]:8080",
		"0251.0376.0251.0376",
		"2852039166",
		"0xA9FEA9FE",
		"::ffff:0x7f000001",
		"http://[::ffff:169.254.169.254]/",
		"\x00",
		"",
		"::",
		"1.2.3.4.5",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		result, err := ParseAddress(input)
		if err == nil && result.Internal == result.Public {
			t.Fatalf("inconsistent classification for %q: %+v", input, result)
		}
		if err != nil && !IsInternal(input) {
			t.Fatalf("parse failure did not fail closed for %q", input)
		}
	})
}

func FuzzParseEndpoint(f *testing.F) {
	for _, seed := range []string{
		"1.2.3.4:80",
		"[::ffff:169.254.169.254]:80",
		"fe80::1%eth0",
		"http://example.com:443/path",
		"[]",
		"[::1",
		":80",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		host, port, err := ParseEndpoint(input)
		if err == nil && host == "" {
			t.Fatalf("ParseEndpoint(%q) returned an empty host", input)
		}
		if err == nil && port != "" {
			if validatePort(port) != nil {
				t.Fatalf("ParseEndpoint(%q) accepted invalid port %q", input, port)
			}
		}
	})
}
