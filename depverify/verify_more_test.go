package depverify

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestProxyRedirectDowngradeRejected(t *testing.T) {
	fetcher := newFakeFetcher()
	src := httpsSource("proxy.golang.org", modA, verA)
	src.Redirects = []Hop{
		{From: src.URL, To: "http://mirror.insecure/" + modA + "/@v/" + verA + ".zip"},
	}
	src.Protocol = "http"
	blob, err := fetcher.add(modA, verA, modFiles(modA), src)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := sumLines(modA, verA, blob)
	if err != nil {
		t.Fatal(err)
	}

	v := New(Options{})
	if _, err := v.Verify(context.Background(), requireOnly(), sum, fetcher); !errors.Is(err, ErrProtocol) {
		t.Fatalf("expected ErrProtocol for http downgrade, got %v", err)
	}

	safe := httpsSource("proxy.golang.org", modA, verA)
	safe.Redirects = []Hop{
		{From: safe.URL, To: "https://cdn.example.com/" + modA + "/" + verA + ".zip"},
	}
	safeBlob, err := buildBlob(modA, verA, modFiles(modA), safe)
	if err != nil {
		t.Fatal(err)
	}
	fetcher.blobs[modA+"@"+verA] = safeBlob
	if _, err := v.Verify(context.Background(), requireOnly(), sum, fetcher); err != nil {
		t.Fatalf("https redirect should be allowed: %v", err)
	}
}

func TestMissingGoSumEntryFails(t *testing.T) {
	fetcher := newFakeFetcher()
	if _, err := fetcher.add(modA, verA, modFiles(modA),
		httpsSource("proxy.golang.org", modA, verA)); err != nil {
		t.Fatal(err)
	}
	v := New(Options{})
	if _, err := v.Verify(context.Background(), requireOnly(), "", fetcher); !errors.Is(err, ErrChecksumMissing) {
		t.Fatalf("expected ErrChecksumMissing with empty go.sum, got %v", err)
	}

	sum := "example.com/other v1.0.0 h1:abcdef\n"
	if _, err := v.Verify(context.Background(), requireOnly(), sum, fetcher); !errors.Is(err, ErrChecksumMissing) {
		t.Fatalf("expected ErrChecksumMissing for unrelated entry, got %v", err)
	}
}

func TestRegisteredLocalReplacementAllowed(t *testing.T) {
	dir := t.TempDir()
	for name, content := range modFiles(modA) {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gomod := "module app\n\nrequire " + modA + " " + verA + "\n\n" +
		"replace " + modA + " " + verA + " => " + dir + "\n"

	v := New(Options{})
	if _, err := v.Verify(context.Background(), gomod, "", newFakeFetcher()); !errors.Is(err, ErrLocalNotRegistered) {
		t.Fatalf("expected ErrLocalNotRegistered, got %v", err)
	}

	v = New(Options{LocalReplacements: []LocalReplacement{
		{Path: modA, Version: verA, Dir: dir},
	}})
	report, err := v.Verify(context.Background(), gomod, "", newFakeFetcher())
	if err != nil {
		t.Fatalf("registered local replacement failed: %v", err)
	}
	vm := report.Modules[modA]
	if vm == nil || !vm.Local || vm.Source.Protocol != "file" || vm.Digest == "" {
		t.Fatalf("local binding wrong: %+v", vm)
	}

	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package a // evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report2, err := v.Verify(context.Background(), gomod, "", newFakeFetcher())
	if err != nil {
		t.Fatal(err)
	}
	if report2.Modules[modA].Digest == vm.Digest {
		t.Fatal("tampered local content kept the same digest")
	}
	gate := NewBuildGate(report)
	if err := gate.Use(modA, verA, report2.Modules[modA].Digest); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("gate should reject tampered local digest, got %v", err)
	}
}

func TestWildcardReplaceRejectedByDefault(t *testing.T) {
	gomod := "module app\n\nrequire " + modA + " " + verA + "\n\n" +
		"replace " + modA + " => " + modFork + " " + verFork + "\n"
	fetcher := newFakeFetcher()
	v := New(Options{})
	if _, err := v.Verify(context.Background(), gomod, "", fetcher); !errors.Is(err, ErrWildcardReplace) {
		t.Fatalf("expected ErrWildcardReplace, got %v", err)
	}

	v = New(Options{AllowWildcardReplace: true})
	if _, err := fetcher.add(modFork, verFork, modFiles(modFork),
		httpsSource("evil.example.net", modFork, verFork)); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(context.Background(), gomod, "", fetcher); !errors.Is(err, ErrChecksumMissing) {
		t.Fatalf("opt-in wildcard still needs checksum, got %v", err)
	}

	// With a checksum for the effective fork at the required version, the
	// wildcard swap is allowed but fully verified and source-bound.
	forkBlob := fetcher.blobs[modFork+"@"+verA]
	sum, err := sumLines(modFork, verA, forkBlob)
	if err != nil {
		t.Fatal(err)
	}
	report, err := v.Verify(context.Background(), gomod, sum, fetcher)
	if err != nil {
		t.Fatalf("verified wildcard replace failed: %v", err)
	}
	vm := report.Modules[modA]
	if vm == nil || !vm.Replaced || vm.EffectivePath != modFork || vm.EffectiveVersion != verA {
		t.Fatalf("wildcard binding wrong: %+v", vm)
	}
}

func TestCompetingReplacesFail(t *testing.T) {
	gomod := "module app\n\nrequire " + modA + " " + verA + "\n\n" +
		"replace " + modA + " " + verA + " => " + modFork + " " + verFork + "\n" +
		"replace " + modA + " " + verA + " => example.net/other " + verA + "\n"
	v := New(Options{})
	if _, err := v.Verify(context.Background(), gomod, "", newFakeFetcher()); !errors.Is(err, ErrCompetingReplace) {
		t.Fatalf("expected ErrCompetingReplace, got %v", err)
	}
}

func TestExcludedVersionFails(t *testing.T) {
	fetcher := newFakeFetcher()
	blob, err := fetcher.add(modA, verA, modFiles(modA),
		httpsSource("proxy.golang.org", modA, verA))
	if err != nil {
		t.Fatal(err)
	}
	sum, err := sumLines(modA, verA, blob)
	if err != nil {
		t.Fatal(err)
	}
	gomod := requireOnly() + "\nexclude " + modA + " " + verA + "\n"
	v := New(Options{})
	if _, err := v.Verify(context.Background(), gomod, sum, fetcher); !errors.Is(err, ErrExcluded) {
		t.Fatalf("expected ErrExcluded, got %v", err)
	}
}

func TestGateRejectsUnverifiedAndUnused(t *testing.T) {
	fetcher := newFakeFetcher()
	blob, err := fetcher.add(modA, verA, modFiles(modA),
		httpsSource("proxy.golang.org", modA, verA))
	if err != nil {
		t.Fatal(err)
	}
	sum, err := sumLines(modA, verA, blob)
	if err != nil {
		t.Fatal(err)
	}
	report, err := New(Options{}).Verify(context.Background(), requireOnly(), sum, fetcher)
	if err != nil {
		t.Fatal(err)
	}

	gate := NewBuildGate(report)
	if err := gate.Use("example.com/unverified", "v1.0.0", "h1:x"); err == nil {
		t.Fatal("gate accepted a module that was never verified")
	}

	goodGate := NewBuildGate(report)
	if err := goodGate.Use(modA, verA, "h1:wrong-digest"); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("expected mismatch from gate, got %v", err)
	}

	emptyGate := NewBuildGate(report)
	if err := emptyGate.Complete(); err == nil {
		t.Fatal("gate should fail when verified modules are not consumed")
	}
}
