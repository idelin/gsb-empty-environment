package netsec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"testing"
)

// stubFetcher returns canned bodies or errors per host URL and records the
// exact host order contacted so the discard-and-failover behavior can be
// asserted.
type stubFetcher struct {
	bodies map[string][]byte
	errs   map[string]error
	called []string
}

func (s *stubFetcher) Fetch(_ context.Context, host HostEntry, _ string) (io.ReadCloser, error) {
	s.called = append(s.called, host.URL)
	if err := s.errs[host.URL]; err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(s.bodies[host.URL])), nil
}
func sha256Digest(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// TestDigestMismatchDiscardsAndFailsOver: 摘要不符的内容必须在尝试下一个
// host 前整体丢弃，且最终返回 ErrDigestMismatch 包装错误。
func TestDigestMismatchDiscardsAndFailsOver(t *testing.T) {
	good := []byte("trusted layer contents")
	goodDigest := sha256Digest(good)

	cfg := &Config{
		Server: testServer,
		Hosts: []HostEntry{
			{URL: "https://first-mirror.example.net"},
			{URL: "https://second-mirror.example.net"},
			{URL: "https://third-mirror.example.net"},
		},
	}

	t.Run("first two hosts corrupt, third serves verified content", func(t *testing.T) {
		fetcher := &stubFetcher{
			bodies: map[string][]byte{
				"https://first-mirror.example.net":  []byte("attacker payload"),
				"https://second-mirror.example.net": []byte("another attacker payload"),
				"https://third-mirror.example.net":  good,
			},
			errs: map[string]error{},
		}

		content, err := PullVerified(context.Background(), cfg, nil, goodDigest, fetcher)
		if err != nil {
			t.Fatalf("verified pull should succeed on the third host: %v", err)
		}
		if !bytes.Equal(content, good) {
			t.Fatal("returned content does not match the verified blob")
		}

		wantOrder := []string{
			"https://first-mirror.example.net",
			"https://second-mirror.example.net",
			"https://third-mirror.example.net",
		}
		if len(fetcher.called) != len(wantOrder) {
			t.Fatalf("expected %d fetch attempts, got %v", len(wantOrder), fetcher.called)
		}
		for i := range wantOrder {
			if fetcher.called[i] != wantOrder[i] {
				t.Fatalf("attempt order = %v, want %v", fetcher.called, wantOrder)
			}
		}
	})

	t.Run("every host corrupt yields digest mismatch and no content", func(t *testing.T) {
		fetcher := &stubFetcher{
			bodies: map[string][]byte{
				"https://first-mirror.example.net":  []byte("attacker payload"),
				"https://second-mirror.example.net": []byte("still attacker payload"),
			},
			errs: map[string]error{},
		}
		// Only two corrupt hosts this time.
		cfgTwo := &Config{Server: cfg.Server, Hosts: cfg.Hosts[:2]}

		content, err := PullVerified(context.Background(), cfgTwo, nil, goodDigest, fetcher)
		if !errors.Is(err, ErrDigestMismatch) {
			t.Fatalf("expected ErrDigestMismatch, got %v", err)
		}
		if content != nil {
			t.Fatalf("corrupt content must be discarded, got %q", content)
		}
		if len(fetcher.called) != 2 {
			t.Fatalf("both hosts must be tried after mismatch, calls=%v", fetcher.called)
		}
	})

	t.Run("fetch error on first host still fails over to good host", func(t *testing.T) {
		fetcher := &stubFetcher{
			bodies: map[string][]byte{
				"https://second-mirror.example.net": good,
				"https://third-mirror.example.net":  good,
			},
			errs: map[string]error{
				"https://first-mirror.example.net": errors.New("connection reset"),
			},
		}
		content, err := PullVerified(context.Background(), cfg, nil, goodDigest, fetcher)
		if err != nil {
			t.Fatalf("expected failover after fetch error: %v", err)
		}
		if !bytes.Equal(content, good) {
			t.Fatal("wrong content after failover")
		}
		if fetcher.called[0] != "https://first-mirror.example.net" ||
			fetcher.called[1] != "https://second-mirror.example.net" {
			t.Fatalf("unexpected attempt order %v", fetcher.called)
		}
	})
}

// TestPullSkipsResolveOnlyHost: 运行时拉取不得联系只有 resolve 能力的 host。
func TestPullSkipsResolveOnlyHost(t *testing.T) {
	good := []byte("blob only a pull host may serve")
	goodDigest := sha256Digest(good)

	cfg := &Config{
		Server: testServer,
		Hosts: []HostEntry{
			{URL: "https://resolver.example.net", Capabilities: []string{CapResolve}},
			{URL: "https://puller.example.net"},
		},
	}
	fetcher := &stubFetcher{
		bodies: map[string][]byte{"https://puller.example.net": good},
		errs:   map[string]error{},
	}

	content, err := PullVerified(context.Background(), cfg, nil, goodDigest, fetcher)
	if err != nil {
		t.Fatalf("pull should skip resolve-only host and succeed: %v", err)
	}
	if !bytes.Equal(content, good) {
		t.Fatal("wrong content")
	}
	if len(fetcher.called) != 1 || fetcher.called[0] != "https://puller.example.net" {
		t.Fatalf("resolve-only host must never be fetched, calls=%v", fetcher.called)
	}
}

func TestPullNoPullHost(t *testing.T) {
	cfg := &Config{
		Server: testServer,
		Hosts: []HostEntry{
			{URL: "https://resolver.example.net", Capabilities: []string{CapResolve}},
		},
	}
	fetcher := &stubFetcher{bodies: map[string][]byte{}, errs: map[string]error{}}

	_, err := PullVerified(context.Background(), cfg, nil, sha256Digest([]byte("x")), fetcher)
	if !errors.Is(err, ErrNoPullHost) {
		t.Fatalf("expected ErrNoPullHost, got %v", err)
	}
	if len(fetcher.called) != 0 {
		t.Fatalf("no host should be contacted, calls=%v", fetcher.called)
	}
}

func TestDigestPolicyAndSyntax(t *testing.T) {
	cfg := &Config{
		Server: testServer,
		Hosts:  []HostEntry{{URL: "https://mirror.example.net"}},
	}
	fetcher := &stubFetcher{bodies: map[string][]byte{}, errs: map[string]error{}}

	t.Run("malformed digest", func(t *testing.T) {
		if _, err := PullVerified(context.Background(), cfg, nil, "not-a-digest", fetcher); err == nil {
			t.Fatal("malformed digest must be rejected")
		}
	})

	t.Run("algorithm not allowed by policy", func(t *testing.T) {
		strict := &Policy{AllowedDigestAlgorithms: []string{"sha512"}}
		_, err := PullVerified(context.Background(), cfg, strict, sha256Digest([]byte("x")), fetcher)
		if !errors.Is(err, ErrDigestNotAllowed) {
			t.Fatalf("expected ErrDigestNotAllowed, got %v", err)
		}
		if len(fetcher.called) != 0 {
			t.Fatalf("policy rejection must happen before any fetch, calls=%v", fetcher.called)
		}
	})

	t.Run("truncated sha256 digest", func(t *testing.T) {
		if _, err := PullVerified(context.Background(), cfg, nil, "sha256:abcdef", fetcher); err == nil {
			t.Fatal("truncated digest must be rejected")
		}
	})
}

func TestPullRejectsInvalidConfigFirst(t *testing.T) {
	cfg := &Config{
		Server: testServer,
		Hosts:  []HostEntry{{URL: "http://plaintext-mirror.example.net"}},
	}
	fetcher := &stubFetcher{bodies: map[string][]byte{}, errs: map[string]error{}}

	_, err := PullVerified(context.Background(), cfg, nil, sha256Digest([]byte("x")), fetcher)
	if !errors.Is(err, ErrInsecureHost) {
		t.Fatalf("config validation must run before pulls, got %v", err)
	}
	if len(fetcher.called) != 0 {
		t.Fatalf("invalid config must not trigger any fetch, calls=%v", fetcher.called)
	}
}
