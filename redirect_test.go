package netsec

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func request(t *testing.T, rawURL string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatalf("NewRequest(%q): %v", rawURL, err)
	}
	return req
}

func TestCheckRedirectBlocksInternalTarget(t *testing.T) {
	resolver, _ := fakeResolver(t, map[string][]string{
		"public.example":   {"93.184.216.34"},
		"metadata.example": {"::ffff:169.254.169.254"},
	})

	via := []*http.Request{request(t, "http://public.example/")}
	targets := []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://[::ffff:169.254.169.254]/latest/meta-data/",
		"http://[::ffff:169.254.169.254%25en0]/latest/meta-data/",
		"http://metadata.example/latest/meta-data/",
		"http://[::169.254.169.254]/latest/meta-data/",
		"http://[2002:a9fe:a9fe::1]/latest/meta-data/",
		"http://[fe80::1%25en0]:8080/latest/meta-data/",
		"http://0251.0376.0251.0376/latest/meta-data/",
		"http://2852039166/latest/meta-data/",
		"http://0177.0.0.1/latest/meta-data/",
	}

	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			err := resolver.CheckRedirect(request(t, target), via)
			if !errors.Is(err, ErrBlocked) {
				t.Errorf("CheckRedirect(%q) = %v, want ErrBlocked", target, err)
			}
		})
	}
}

func TestCheckRedirectRevalidatesInitialHop(t *testing.T) {
	resolver, _ := fakeResolver(t, map[string][]string{
		"public.example":   {"93.184.216.34"},
		"metadata.example": {"169.254.169.254"},
	})

	// The initial URL is internal and must be caught even though the redirect
	// target itself is public.
	err := resolver.CheckRedirect(
		request(t, "http://public.example/"),
		[]*http.Request{request(t, "http://metadata.example/")},
	)
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("CheckRedirect = %v, want ErrBlocked for the initial hop", err)
	}
}

func TestCheckRedirectAllowsPublicChain(t *testing.T) {
	resolver, _ := fakeResolver(t, map[string][]string{
		"one.example":   {"93.184.216.34"},
		"two.example":   {"2606:4700:4700::1111"},
		"three.example": {"8.8.8.8"},
	})

	err := resolver.CheckRedirect(
		request(t, "http://three.example/c"),
		[]*http.Request{
			request(t, "http://one.example/a"),
			request(t, "http://two.example/b"),
		},
	)
	if err != nil {
		t.Fatalf("CheckRedirect(public chain) = %v", err)
	}
}

func TestCheckRedirectResolvesRelativeTarget(t *testing.T) {
	resolver, _ := fakeResolver(t, map[string][]string{
		"public.example": {"93.184.216.34"},
	})
	target := request(t, "/next")
	err := resolver.CheckRedirect(target, []*http.Request{request(t, "http://public.example/start")})
	if err != nil {
		t.Fatalf("CheckRedirect(relative public target) = %v", err)
	}
}

func TestCheckRedirectRejectsUnsupportedScheme(t *testing.T) {
	resolver, _ := fakeResolver(t, nil)
	err := resolver.CheckRedirect(request(t, "ftp://8.8.8.8/file"), nil)
	if !errors.Is(err, ErrUnsupportedScheme) {
		t.Errorf("CheckRedirect(ftp) = %v, want ErrUnsupportedScheme", err)
	}
}

func TestCheckRedirectLimitsChain(t *testing.T) {
	resolver, _ := fakeResolver(t, map[string][]string{
		"public.example": {"93.184.216.34"},
	})
	via := make([]*http.Request, DefaultMaxRedirects)
	for i := range via {
		via[i] = request(t, "http://public.example/")
	}
	err := resolver.CheckRedirect(request(t, "http://public.example/"), via)
	if err == nil {
		t.Fatalf("CheckRedirect accepted %d hops", len(via))
	}
	if errors.Is(err, ErrBlocked) {
		t.Errorf("redirect limit error should not be ErrBlocked: %v", err)
	}
}

type redirectTransport struct {
	location string
	calls    int
}

func (t *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.calls++
	if t.calls == 1 {
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{t.location}},
			Body:       io.NopCloser(strings.NewReader("redirect")),
			Request:    req,
		}, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("ok")),
		Request:    req,
	}, nil
}

type countingTransport struct {
	calls int
}

func (t *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.calls++
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("ok")),
		Request:    req,
	}, nil
}

func TestHTTPClientRevalidatesRedirectTarget(t *testing.T) {
	resolver, _ := fakeResolver(t, map[string][]string{
		"public.example":   {"93.184.216.34"},
		"metadata.example": {"169.254.169.254"},
	})

	tests := []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://[::ffff:169.254.169.254]/latest/meta-data/",
		"http://metadata.example/latest/meta-data/",
	}
	for _, location := range tests {
		t.Run(location, func(t *testing.T) {
			transport := &redirectTransport{location: location}
			client := &http.Client{
				Transport:     transport,
				CheckRedirect: resolver.CheckRedirect,
			}
			_, err := client.Do(request(t, "http://public.example/"))
			if !errors.Is(err, ErrBlocked) {
				t.Fatalf("client.Do = %v, want ErrBlocked", err)
			}
			if transport.calls != 1 {
				t.Errorf("transport called %d times, want 1 (redirect must not be followed)", transport.calls)
			}
		})
	}
}

func TestHTTPClientFollowsPublicRedirect(t *testing.T) {
	resolver, _ := fakeResolver(t, map[string][]string{
		"public.example": {"93.184.216.34"},
		"other.example":  {"1.1.1.1"},
	})
	transport := &redirectTransport{location: "http://other.example/next"}
	client := &http.Client{
		Transport:     transport,
		CheckRedirect: resolver.CheckRedirect,
	}
	response, err := client.Do(request(t, "http://public.example/"))
	if err != nil {
		t.Fatalf("client.Do = %v", err)
	}
	defer response.Body.Close()
	if transport.calls != 2 {
		t.Errorf("transport called %d times, want 2", transport.calls)
	}
}

func TestHTTPClientValidatesInitialTarget(t *testing.T) {
	resolver, _ := fakeResolver(t, map[string][]string{
		"public.example":   {"93.184.216.34"},
		"metadata.example": {"169.254.169.254"},
	})
	transport := &countingTransport{}
	client := &http.Client{
		Transport:     resolver.WrapRoundTripper(transport),
		CheckRedirect: resolver.CheckRedirect,
	}

	_, err := client.Do(request(t, "http://metadata.example/latest"))
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("client.Do = %v, want ErrBlocked", err)
	}
	if transport.calls != 0 {
		t.Errorf("transport called %d times, want 0", transport.calls)
	}

	response, err := client.Do(request(t, "http://public.example/"))
	if err != nil {
		t.Fatalf("client.Do(public) = %v", err)
	}
	defer response.Body.Close()
	if transport.calls != 1 {
		t.Errorf("transport called %d times after public request, want 1", transport.calls)
	}
}

func TestDialContextRejectsInternalTargetBeforeDialing(t *testing.T) {
	resolver, _ := fakeResolver(t, map[string][]string{
		"metadata.example": {"169.254.169.254"},
	})
	_, err := resolver.DialContext(context.Background(), "tcp", "metadata.example:80")
	if !errors.Is(err, ErrBlocked) {
		t.Errorf("DialContext = %v, want ErrBlocked", err)
	}
}
