package netsec

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

func fakeResolver(t *testing.T, records map[string][]string) (*Resolver, *int) {
	t.Helper()
	calls := 0
	return &Resolver{
		Lookup: func(_ context.Context, host string) ([]netip.Addr, error) {
			calls++
			values, ok := records[host]
			if !ok {
				return nil, errors.New("no such host")
			}
			addresses := make([]netip.Addr, 0, len(values))
			for _, value := range values {
				addr, err := netip.ParseAddr(value)
				if err != nil {
					t.Fatalf("bad test address %q: %v", value, err)
				}
				addresses = append(addresses, addr)
			}
			return addresses, nil
		},
	}, &calls
}

func TestResolveClassifiesDNSAnswers(t *testing.T) {
	resolver, _ := fakeResolver(t, map[string][]string{
		"public.example":   {"8.8.8.8", "2606:4700:4700::1111"},
		"metadata.example": {"::ffff:169.254.169.254"},
		"6to4.example":     {"2002:a9fe:a9fe::1"},
		"mixed.example":    {"8.8.8.8", "10.0.0.1"},
	})

	tests := []struct {
		host           string
		wantCategories []Category
		wantAddresses  []string
		wantErr        bool
		wantBlocked    bool
	}{
		{"public.example", []Category{CategoryPublic, CategoryPublic}, []string{"8.8.8.8", "2606:4700:4700::1111"}, false, false},
		{"metadata.example", []Category{CategoryLinkLocal}, []string{"169.254.169.254"}, false, true},
		{"6to4.example", []Category{CategoryLinkLocal}, []string{"169.254.169.254"}, false, true},
		{"mixed.example", []Category{CategoryPublic, CategoryPrivate}, []string{"8.8.8.8", "10.0.0.1"}, false, true},
	}

	for _, test := range tests {
		t.Run(test.host, func(t *testing.T) {
			results, err := resolver.Resolve(context.Background(), test.host)
			if err != nil {
				t.Fatalf("Resolve(%q) error = %v", test.host, err)
			}
			if len(results) != len(test.wantCategories) {
				t.Fatalf("Resolve(%q) returned %d results, want %d", test.host, len(results), len(test.wantCategories))
			}
			for i, result := range results {
				if result.Category != test.wantCategories[i] {
					t.Errorf("result[%d] category = %s, want %s", i, result.Category, test.wantCategories[i])
				}
				if result.Address.String() != test.wantAddresses[i] {
					t.Errorf("result[%d] address = %s, want %s", i, result.Address, test.wantAddresses[i])
				}
			}

			checkErr := resolver.CheckHost(context.Background(), test.host)
			if test.wantBlocked {
				if !errors.Is(checkErr, ErrBlocked) {
					t.Errorf("CheckHost(%q) = %v, want ErrBlocked", test.host, checkErr)
				}
			} else if checkErr != nil {
				t.Errorf("CheckHost(%q) = %v, want nil", test.host, checkErr)
			}
		})
	}
}

func TestCheckHostBlocksEveryNonPublicDNSAnswer(t *testing.T) {
	resolver, _ := fakeResolver(t, map[string][]string{
		"mixed.example": {"8.8.8.8", "::ffff:169.254.169.254"},
	})

	results, err := resolver.ResolveAndCheck(context.Background(), "mixed.example")
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("ResolveAndCheck error = %v, want ErrBlocked", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %v, want both answers", results)
	}

	var blocked *BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("error = %T, want *BlockedError", err)
	}
	if len(blocked.Results) != 2 {
		t.Errorf("BlockedError.Results = %v, want both answers", blocked.Results)
	}
}

func TestLiteralIsNotSentToDNS(t *testing.T) {
	resolver, calls := fakeResolver(t, nil)

	if err := resolver.CheckHost(context.Background(), "::ffff:169.254.169.254"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("CheckHost literal = %v, want ErrBlocked", err)
	}
	if *calls != 0 {
		t.Errorf("Lookup was called %d times for an IP literal", *calls)
	}

	if _, err := resolver.Resolve(context.Background(), "1.2.3.999"); !errors.Is(err, ErrInvalidAddress) {
		t.Fatalf("Resolve malformed IPv4 = %v, want ErrInvalidAddress", err)
	}
	if *calls != 0 {
		t.Errorf("Lookup was called %d times for a malformed IPv4 literal", *calls)
	}
}

func TestResolveDecimalAndOctalHosts(t *testing.T) {
	resolver, calls := fakeResolver(t, nil)

	for _, host := range []string{"2852039166", "0251.0376.0251.0376", "0xA9.0xFE.0xA9.0xFE"} {
		results, err := resolver.Resolve(context.Background(), host)
		if err != nil {
			t.Fatalf("Resolve(%q) error = %v", host, err)
		}
		if len(results) != 1 || results[0].Category != CategoryLinkLocal {
			t.Errorf("Resolve(%q) = %+v, want one link-local answer", host, results)
		}
	}
	if *calls != 0 {
		t.Errorf("Lookup was called %d times for literal hosts", *calls)
	}
}

func TestCheckHostPort(t *testing.T) {
	resolver, _ := fakeResolver(t, map[string][]string{
		"metadata.example": {"169.254.169.254"},
	})

	if err := resolver.CheckHostPort(context.Background(), "metadata.example:443"); !errors.Is(err, ErrBlocked) {
		t.Errorf("CheckHostPort internal = %v, want ErrBlocked", err)
	}
	if err := resolver.CheckHostPort(context.Background(), "8.8.8.8:443"); err != nil {
		t.Errorf("CheckHostPort public = %v, want nil", err)
	}
	if err := resolver.CheckHostPort(context.Background(), "8.8.8.8:0"); !errors.Is(err, ErrInvalidAddress) {
		t.Errorf("CheckHostPort invalid port = %v, want ErrInvalidAddress", err)
	}
}

func TestCheckURLForms(t *testing.T) {
	resolver, _ := fakeResolver(t, map[string][]string{
		"public.example":   {"93.184.216.34"},
		"metadata.example": {"169.254.169.254"},
		"mixed.example":    {"8.8.8.8", "10.0.0.1"},
	})

	tests := []struct {
		rawURL  string
		wantErr error
	}{
		{"http://[::ffff:169.254.169.254]/latest", ErrBlocked},
		{"http://[::ffff:169.254.169.254%eth0]/latest", ErrBlocked},
		{"http://[fe80::1%eth0]:8080/latest", ErrBlocked},
		{"http://[2002:a9fe:a9fe::1]/latest", ErrBlocked},
		{"http://0251.0376.0251.0376/latest", ErrBlocked},
		{"http://2852039166/latest", ErrBlocked},
		{"http://0177.0.0.1/latest", ErrBlocked},
		{"http://metadata.example/latest", ErrBlocked},
		{"http://mixed.example/latest", ErrBlocked},
		{"http://8.8.8.8/latest", nil},
		{"http://[2606:4700:4700::1111]/latest", nil},
		{"http://public.example/latest", nil},
		{"ftp://8.8.8.8/latest", ErrUnsupportedScheme},
		{"http://", ErrInvalidAddress},
	}
	for _, test := range tests {
		t.Run(test.rawURL, func(t *testing.T) {
			err := resolver.CheckURL(context.Background(), test.rawURL)
			if test.wantErr == nil {
				if err != nil {
					t.Errorf("CheckURL(%q) = %v, want nil", test.rawURL, err)
				}
				return
			}
			if !errors.Is(err, test.wantErr) {
				t.Errorf("CheckURL(%q) = %v, want %v", test.rawURL, err, test.wantErr)
			}
		})
	}
}
