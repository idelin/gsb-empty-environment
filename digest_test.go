package netsec_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"example.org/netsec/q0387"
)

type mockContent struct {
	reader     io.Reader
	discarded  bool
	closed     bool
	discardErr error
	closeErr   error
}

func (m *mockContent) Read(p []byte) (int, error) { return m.reader.Read(p) }

func (m *mockContent) Close() error {
	m.closed = true
	return m.closeErr
}

func (m *mockContent) Discard() error {
	m.discarded = true
	return m.discardErr
}

type fetchResult struct {
	content netsec.Content
	err     error
}

type scriptedPuller struct {
	results map[string]fetchResult
	order   []string
}

func (p *scriptedPuller) Fetch(_ context.Context, host netsec.HostEntry, _ netsec.Digest) (netsec.Content, error) {
	p.order = append(p.order, host.Host)
	result := p.results[host.Host]
	return result.content, result.err
}

type recordingCommitter struct {
	calls      int
	lastDigest netsec.Digest
	lastData   []byte
	err        error
}

func (c *recordingCommitter) Commit(_ context.Context, digest netsec.Digest, payload io.Reader) error {
	c.calls++
	c.lastDigest = digest
	data, err := io.ReadAll(payload)
	if err != nil {
		return err
	}
	c.lastData = data
	return c.err
}

func pullHost(url string) netsec.HostEntry {
	return netsec.HostEntry{
		Host:         url,
		Capabilities: []netsec.Capability{netsec.CapabilityPull},
	}
}

// TestDigestMismatch covers the "摘要不符" scenario: when a host serves
// content whose digest does not match, the partial download must be
// discarded before the next host is tried, and good content from a
// later host must still be committed exactly once.
func TestDigestMismatch(t *testing.T) {
	t.Parallel()

	digest := sha256Digest("payload")
	badContent := &mockContent{reader: strings.NewReader("tampered")}
	goodContent := &mockContent{reader: strings.NewReader("payload")}

	badHost := "https://bad-mirror.example.net"
	goodHost := "https://registry.example.com"

	puller := &scriptedPuller{
		results: map[string]fetchResult{
			badHost:  {content: badContent},
			goodHost: {content: goodContent},
		},
	}
	cfg := authConfig("https://registry.example.com", pullHost(badHost), pullHost(goodHost))

	committer := &recordingCommitter{}
	err := netsec.PullByDigest(context.Background(), cfg, netsec.DefaultPolicy(), digest, puller, committer)
	if err != nil {
		t.Fatalf("PullByDigest() unexpected error: %v", err)
	}

	if !badContent.discarded {
		t.Fatal("mismatched content was not discarded before the next host")
	}
	if !badContent.closed {
		t.Fatal("mismatched content was not closed")
	}
	if committer.calls != 1 || string(committer.lastData) != "payload" || committer.lastDigest.String() != digest {
		t.Fatalf("commit = %+v, want single commit of %q as %s", committer, "payload", digest)
	}
	if !goodContent.closed {
		t.Fatal("committed content was not closed")
	}
	if len(puller.order) != 2 || puller.order[0] != badHost || puller.order[1] != goodHost {
		t.Fatalf("fetch order = %v, want [%s %s]", puller.order, badHost, goodHost)
	}
}

// TestDigestMismatchAllHostsFail verifies that when every host serves
// bad content each download is discarded, nothing is committed and the
// joined error preserves ErrDigestMismatch.
func TestDigestMismatchAllHostsFail(t *testing.T) {
	t.Parallel()

	digest := sha256Digest("payload")
	first := &mockContent{reader: strings.NewReader("tampered-1")}
	second := &mockContent{reader: strings.NewReader("tampered-2")}

	firstHost := "https://bad-mirror-1.example.net"
	secondHost := "https://bad-mirror-2.example.net"

	puller := &scriptedPuller{
		results: map[string]fetchResult{
			firstHost:  {content: first},
			secondHost: {content: second},
		},
	}
	cfg := authConfig("https://registry.example.com", pullHost(firstHost), pullHost(secondHost))

	committer := &recordingCommitter{}
	err := netsec.PullByDigest(context.Background(), cfg, netsec.DefaultPolicy(), digest, puller, committer)
	if !errors.Is(err, netsec.ErrDigestMismatch) {
		t.Fatalf("PullByDigest() error = %v, want errors.Is ErrDigestMismatch", err)
	}
	if committer.calls != 0 {
		t.Fatalf("committer calls = %d, want 0", committer.calls)
	}
	if !first.discarded || !second.discarded {
		t.Fatal("mismatched content was not discarded on every failed host")
	}
	if !first.closed || !second.closed {
		t.Fatal("mismatched content was not closed on every failed host")
	}
}

// TestDigestFetchErrorFailsOver verifies that a transport-level fetch
// failure also moves on to the next host without a commit.
func TestDigestFetchErrorFailsOver(t *testing.T) {
	t.Parallel()

	digest := sha256Digest("payload")
	fetchErr := errors.New("connection reset")
	goodContent := &mockContent{reader: strings.NewReader("payload")}

	failingHost := "https://flaky.example.net"
	goodHost := "https://registry.example.com"

	puller := &scriptedPuller{
		results: map[string]fetchResult{
			failingHost: {err: fetchErr},
			goodHost:    {content: goodContent},
		},
	}
	cfg := authConfig("https://registry.example.com", pullHost(failingHost), pullHost(goodHost))

	committer := &recordingCommitter{}
	if err := netsec.PullByDigest(context.Background(), cfg, netsec.DefaultPolicy(), digest, puller, committer); err != nil {
		t.Fatalf("PullByDigest() unexpected error: %v", err)
	}
	if committer.calls != 1 || string(committer.lastData) != "payload" {
		t.Fatalf("commit = %+v, want single commit of %q", committer, "payload")
	}
	if puller.order[0] != failingHost || puller.order[1] != goodHost {
		t.Fatalf("fetch order = %v, want [%s %s]", puller.order, failingHost, goodHost)
	}
}

// TestPullByDigestRejectsBadConfig verifies the pull path re-runs
// admission control, so a config that could not be written to disk can
// never drive a pull.
func TestPullByDigestRejectsBadConfig(t *testing.T) {
	t.Parallel()

	cfg := authConfig("https://registry.example.com",
		netsec.HostEntry{Host: "http://mirror.example.net", Capabilities: []netsec.Capability{netsec.CapabilityPull}},
	)
	err := netsec.PullByDigest(context.Background(), cfg, netsec.DefaultPolicy(), sha256Digest("x"),
		&scriptedPuller{results: map[string]fetchResult{}}, &recordingCommitter{})
	if !errors.Is(err, netsec.ErrPlaintextNotAllowed) {
		t.Fatalf("PullByDigest() error = %v, want errors.Is ErrPlaintextNotAllowed", err)
	}
}

func TestParseDigest(t *testing.T) {
	t.Parallel()

	valid := sha256Digest("payload")
	if _, err := netsec.ParseDigest(valid); err != nil {
		t.Fatalf("ParseDigest(%q) unexpected error: %v", valid, err)
	}

	invalid := []string{
		"",
		"sha256:zzzz",
		"sha1:" + strings.Repeat("a", 40),
		"sha256:" + strings.Repeat("a", 62),
		"sha256" + strings.Repeat("a", 64),
	}
	for _, value := range invalid {
		if _, err := netsec.ParseDigest(value); err == nil {
			t.Fatalf("ParseDigest(%q) expected error", value)
		}
	}
}
