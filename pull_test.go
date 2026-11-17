package netsec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type fakeHostState struct {
	resolved     Digest
	resolveErr   error
	blob         []byte
	fetchErr     error
	resolveCalls int
	fetchCalls   int
}

type fakeClient struct {
	mu    sync.Mutex
	hosts map[string]*fakeHostState
}

func (f *fakeClient) state(rawHost string) *fakeHostState {
	ep, err := parseEndpoint(rawHost)
	if err != nil {
		panic(err)
	}
	return f.hosts[ep.host]
}

func (f *fakeClient) Resolve(_ context.Context, host Host, _ Reference) (Digest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	state := f.state(host.Host)
	state.resolveCalls++
	if state.resolveErr != nil {
		return Digest{}, state.resolveErr
	}
	return state.resolved, nil
}

func (f *fakeClient) Fetch(_ context.Context, host Host, _ Digest, w io.Writer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	state := f.state(host.Host)
	state.fetchCalls++
	if state.fetchErr != nil {
		return state.fetchErr
	}
	_, err := w.Write(state.blob)
	return err
}

func digestOf(t *testing.T, payload []byte) Digest {
	t.Helper()
	sum := sha256.Sum256(payload)
	d, err := ParseDigest("sha256:" + hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatalf("digest setup: %v", err)
	}
	return d
}

func readDirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestDigestMismatchDiscardsBeforeNextHost(t *testing.T) {
	good := []byte("hello-registry")
	tampered := []byte("attacker-controlled-bytes")
	expected := digestOf(t, good)

	client := &fakeClient{hosts: map[string]*fakeHostState{
		"mirror-a.example.com:443": {blob: tampered},
		"mirror-b.example.com:443": {blob: good},
	}}
	cfg := &Config{
		Server: "https://registry.example.com",
		Hosts: []Host{
			{Host: "https://mirror-a.example.com", Capabilities: []string{CapPull}},
			{Host: "https://mirror-b.example.com", Capabilities: []string{CapPull}},
		},
	}
	dir := t.TempDir()
	puller := &Puller{Client: client, Policy: DefaultPolicy(), Dir: dir}

	path, digest, err := puller.Pull(context.Background(), cfg, "repo/image@"+expected.String())
	if err != nil {
		t.Fatalf("pull should fail over to mirror-b: %v", err)
	}
	if digest != expected {
		t.Errorf("digest = %s, want %s", digest, expected)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read committed content: %v", err)
	}
	if string(got) != string(good) {
		t.Errorf("committed content = %q, want %q", got, good)
	}

	bad := client.state("https://mirror-a.example.com")
	goodState := client.state("https://mirror-b.example.com")
	if bad.fetchCalls != 1 || goodState.fetchCalls != 1 {
		t.Errorf("fetch calls: bad=%d good=%d, want 1/1", bad.fetchCalls, goodState.fetchCalls)
	}

	// The tampered temp content must have been discarded: only the final
	// verified file remains, no .netsec-pull-* leftovers.
	names := readDirNames(t, dir)
	if len(names) != 1 || names[0] != "sha256-"+expected.Encoded {
		t.Fatalf("unexpected dir contents after failover: %v", names)
	}
}

func TestAllHostsMismatchFailsAndDiscards(t *testing.T) {
	good := []byte("hello-registry")
	tampered := []byte("attacker-controlled-bytes")
	expected := digestOf(t, good)

	client := &fakeClient{hosts: map[string]*fakeHostState{
		"mirror-a.example.com:443": {blob: tampered},
		"mirror-b.example.com:443": {blob: []byte("other-bad-bytes")},
	}}
	cfg := &Config{
		Server: "https://registry.example.com",
		Hosts: []Host{
			{Host: "https://mirror-a.example.com", Capabilities: []string{CapPull}},
			{Host: "https://mirror-b.example.com", Capabilities: []string{CapPull}},
		},
	}
	dir := t.TempDir()
	puller := &Puller{Client: client, Policy: DefaultPolicy(), Dir: dir}

	_, _, err := puller.Pull(context.Background(), cfg, "repo/image@"+expected.String())
	if err == nil {
		t.Fatal("expected pull failure when every host serves bad content")
	}
	if !errors.Is(err, ErrNoPullHost) {
		t.Fatalf("want ErrNoPullHost, got %v", err)
	}
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("joined error must still expose ErrDigestMismatch, got %v", err)
	}
	if names := readDirNames(t, dir); len(names) != 0 {
		t.Fatalf("all fetched content must be discarded, dir contains: %v", names)
	}
}

func TestResolveOnlyHostSkippedForBlobPull(t *testing.T) {
	good := []byte("hello-registry")
	expected := digestOf(t, good)

	client := &fakeClient{hosts: map[string]*fakeHostState{
		"disco.example.com:443":  {blob: good},
		"mirror.example.com:443": {blob: good},
	}}
	policy := DefaultPolicy()
	policy.ResolveOnlyHosts = []string{"disco.example.com"}
	cfg := &Config{
		Server: "https://registry.example.com",
		Hosts: []Host{
			{Host: "https://disco.example.com", Capabilities: []string{CapResolve}},
			{Host: "https://mirror.example.com", Capabilities: []string{CapResolve, CapPull}},
		},
	}
	puller := &Puller{Client: client, Policy: policy, Dir: t.TempDir()}

	path, _, err := puller.Pull(context.Background(), cfg, "repo/image@"+expected.String())
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if filepath.Dir(path) != puller.Dir {
		t.Errorf("content landed outside configured dir: %s", path)
	}
	if disco := client.state("https://disco.example.com"); disco.fetchCalls != 0 {
		t.Errorf("resolve-only host must never be asked for a blob, got %d fetch calls", disco.fetchCalls)
	}
	if mirror := client.state("https://mirror.example.com"); mirror.fetchCalls != 1 {
		t.Errorf("mirror should serve the blob once, got %d", mirror.fetchCalls)
	}

	// If the host table illegally grants pull to a resolve-only host, the
	// puller refuses up front via the admission check.
	bad := &Config{
		Server: "https://registry.example.com",
		Hosts: []Host{
			{Host: "https://disco.example.com", Capabilities: []string{CapResolve, CapPull}},
		},
	}
	_, _, err = puller.Pull(context.Background(), bad, "repo/image@"+expected.String())
	if !errors.Is(err, ErrResolveOnlyPull) {
		t.Fatalf("want ErrResolveOnlyPull, got %v", err)
	}
}

func TestTagReferenceResolvesThenVerifies(t *testing.T) {
	good := []byte("v1-payload")
	expected := digestOf(t, good)

	client := &fakeClient{hosts: map[string]*fakeHostState{
		"mirror.example.com:443": {resolved: expected, blob: good},
	}}
	cfg := &Config{
		Server: "https://registry.example.com",
		Hosts: []Host{
			{Host: "https://mirror.example.com", Capabilities: []string{CapPull}},
		},
	}
	puller := &Puller{Client: client, Policy: DefaultPolicy(), Dir: t.TempDir()}

	path, digest, err := puller.Pull(context.Background(), cfg, "repo/image:v1")
	if err != nil {
		t.Fatalf("pull by tag: %v", err)
	}
	if digest != expected {
		t.Errorf("digest = %s, want %s", digest, expected)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read content: %v", err)
	}
	if string(got) != string(good) {
		t.Errorf("content = %q, want %q", got, good)
	}
	state := client.state("https://mirror.example.com")
	if state.resolveCalls != 1 || state.fetchCalls != 1 {
		t.Errorf("calls: resolve=%d fetch=%d, want 1/1", state.resolveCalls, state.fetchCalls)
	}
}

func TestFetchErrorFailsOver(t *testing.T) {
	good := []byte("hello-registry")
	expected := digestOf(t, good)

	client := &fakeClient{hosts: map[string]*fakeHostState{
		"mirror-a.example.com:443": {blob: good, fetchErr: errors.New("connection reset")},
		"mirror-b.example.com:443": {blob: good},
	}}
	cfg := &Config{
		Server: "https://registry.example.com",
		Hosts: []Host{
			{Host: "https://mirror-a.example.com", Capabilities: []string{CapPull}},
			{Host: "https://mirror-b.example.com", Capabilities: []string{CapPull}},
		},
	}
	puller := &Puller{Client: client, Policy: DefaultPolicy(), Dir: t.TempDir()}

	if _, _, err := puller.Pull(context.Background(), cfg, "repo/image@"+expected.String()); err != nil {
		t.Fatalf("fetch error should trigger failover: %v", err)
	}
	if names := readDirNames(t, puller.Dir); len(names) != 1 {
		t.Fatalf("want exactly one committed file, got %v", names)
	}
}

func TestRejectInsecureConfigAtPullTime(t *testing.T) {
	cfg := &Config{
		Server: "https://registry.example.com",
		Hosts: []Host{
			{Host: "http://mirror.example.com", Capabilities: []string{CapPull}},
		},
	}
	puller := &Puller{Client: &fakeClient{hosts: map[string]*fakeHostState{}}, Policy: DefaultPolicy(), Dir: t.TempDir()}
	_, _, err := puller.Pull(context.Background(), cfg, "repo/image@sha256:"+hex.EncodeToString(make([]byte, sha256.Size)))
	if !errors.Is(err, ErrPlaintext) {
		t.Fatalf("puller must re-run admission checks, got %v", err)
	}
}

func TestParseReferenceAndDigest(t *testing.T) {
	d := "sha256:" + hex.EncodeToString(make([]byte, sha256.Size))
	ref, err := ParseReference("repo/image:v1@" + d)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ref.Repository != "repo/image" || ref.Tag != "v1" || ref.Digest == nil || ref.Digest.String() != d {
		t.Fatalf("unexpected reference: %+v", ref)
	}

	for _, bad := range []string{
		"",
		"repo@sha256:zzz",
		"repo@md5:" + hex.EncodeToString(make([]byte, 16)),
		"repo@sha256:" + hex.EncodeToString(make([]byte, 16)),
	} {
		if _, err := ParseReference(bad); err == nil {
			t.Errorf("expected error parsing %q", bad)
		}
	}
}
