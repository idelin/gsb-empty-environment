package depverify

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const (
	modA     = "example.com/a"
	modFork  = "evil.example.net/fork-a"
	verA     = "v1.2.3"
	verFork  = "v1.2.3"
	verSpoof = "v9.9.9"
)

func modFiles(tag string) map[string]string {
	return map[string]string{
		"go.mod":  "module " + tag + "\n\ngo 1.24.0\n",
		"main.go": "package " + lastSegment(tag) + " // build " + tag + "\n",
	}
}

func lastSegment(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func requireOnly() string {
	return "module app\n\nrequire " + modA + " " + verA + "\n"
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestNormalRequireVerified(t *testing.T) {
	fetcher := newFakeFetcher()
	blob, err := fetcher.add(modA, verA, modFiles(modA), httpsSource("proxy.golang.org", modA, verA))
	if err != nil {
		t.Fatal(err)
	}
	sum, err := sumLines(modA, verA, blob)
	if err != nil {
		t.Fatal(err)
	}

	v := New(Options{})
	report, err := v.Verify(context.Background(), requireOnly(), sum, fetcher)
	if err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
	vm := report.Modules[modA]
	if vm == nil || vm.Replaced || vm.Digest == "" || vm.Source.Protocol != "https" {
		t.Fatalf("unexpected verified module: %+v", vm)
	}
	if !contains(fetcher.fetched, modA+"@"+verA) {
		t.Fatalf("fetcher never fetched original module: %v", fetcher.fetched)
	}

	gate := NewBuildGate(report)
	if err := gate.Use(modA, verA, vm.Digest); err != nil {
		t.Fatalf("gate Use: %v", err)
	}
	if err := gate.Complete(); err != nil {
		t.Fatalf("gate Complete: %v", err)
	}
}

func TestReplaceSourceSwapCannotBypassChecksum(t *testing.T) {
	fetcher := newFakeFetcher()
	original, err := fetcher.add(modA, verA, modFiles(modA), httpsSource("proxy.golang.org", modA, verA))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fetcher.add(modFork, verFork, modFiles(modFork),
		httpsSource("evil.example.net", modFork, verFork)); err != nil {
		t.Fatal(err)
	}
	sum, err := sumLines(modA, verA, original)
	if err != nil {
		t.Fatal(err)
	}
	gomod := "module app\n\nrequire " + modA + " " + verA + "\n\n" +
		"replace " + modA + " " + verA + " => " + modFork + " " + verFork + "\n"

	v := New(Options{})
	report, err := v.Verify(context.Background(), gomod, sum, fetcher)
	if err == nil {
		t.Fatal("expected failure for replace with no checksum")
	}
	if !errors.Is(err, ErrChecksumMissing) {
		t.Fatalf("expected ErrChecksumMissing, got %v", err)
	}
	if _, present := report.Modules[modA]; present {
		t.Fatal("unverified replaced module must not appear as verified")
	}
	if len(fetcher.fetched) != 0 {
		t.Fatalf("missing checksum must block all fetching, fetched %v", fetcher.fetched)
	}

	forkBlob := fetcher.blobs[modFork+"@"+verFork]
	forkSum, err := sumLines(modFork, verFork, forkBlob)
	if err != nil {
		t.Fatal(err)
	}
	report, err = v.Verify(context.Background(), gomod, forkSum, fetcher)
	if err != nil {
		t.Fatalf("verified replacement failed: %v", err)
	}
	vm := report.Modules[modA]
	if vm == nil || !vm.Replaced || vm.EffectivePath != modFork || vm.Digest == "" {
		t.Fatalf("replacement binding wrong: %+v", vm)
	}
}

func TestReplaceWithTamperedContentMismatches(t *testing.T) {
	fetcher := newFakeFetcher()
	good, err := buildBlob(modFork, verFork, modFiles("clean"),
		httpsSource("evil.example.net", modFork, verFork))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fetcher.add(modFork, verFork, modFiles("backdoored"),
		httpsSource("evil.example.net", modFork, verFork)); err != nil {
		t.Fatal(err)
	}
	sum, err := sumLines(modFork, verFork, good)
	if err != nil {
		t.Fatal(err)
	}
	gomod := "module app\n\nrequire " + modA + " " + verA + "\n\n" +
		"replace " + modA + " " + verA + " => " + modFork + " " + verFork + "\n"

	v := New(Options{})
	if _, err := v.Verify(context.Background(), gomod, sum, fetcher); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("expected ErrChecksumMismatch, got %v", err)
	}
}

func TestVersionSpoofingFailsChecksum(t *testing.T) {
	fetcher := newFakeFetcher()
	legit, err := buildBlob(modA, verSpoof, modFiles("legit"),
		httpsSource("proxy.golang.org", modA, verSpoof))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fetcher.add(modA, verSpoof, modFiles("spoofed"),
		httpsSource("proxy.golang.org", modA, verSpoof)); err != nil {
		t.Fatal(err)
	}
	sum, err := sumLines(modA, verSpoof, legit)
	if err != nil {
		t.Fatal(err)
	}
	gomod := "module app\n\nrequire " + modA + " " + verSpoof + "\n"

	v := New(Options{})
	if _, err := v.Verify(context.Background(), gomod, sum, fetcher); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("expected ErrChecksumMismatch, got %v", err)
	}
}
