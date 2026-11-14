package depverify

import (
	"context"
	"errors"
	"testing"
)

func TestRejectedReplaceNeverFetchesAndAggregatesErrors(t *testing.T) {
	gomod := "module app\n\n" +
		"require (\n\t" + modA + " " + verA + "\n\texample.com/missing v1.0.0\n)\n\n" +
		"replace " + modA + " " + verA + " => " + modFork + " " + verFork + "\n" +
		"replace " + modA + " " + verA + " => example.net/other " + verA + "\n"

	fetcher := newFakeFetcher()
	_, err := New(Options{}).Verify(context.Background(), gomod, "", fetcher)
	if err == nil {
		t.Fatal("expected verification failures")
	}
	var multi *MultiError
	if !errors.As(err, &multi) || len(multi.Errors) < 2 {
		t.Fatalf("expected aggregated MultiError, got %v", err)
	}
	if !errors.Is(err, ErrCompetingReplace) || !errors.Is(err, ErrChecksumMissing) {
		t.Fatalf("expected competing replace + missing checksum, got %v", err)
	}
	if len(fetcher.fetched) != 0 {
		t.Fatalf("rejected configuration must not fetch anything, fetched %v", fetcher.fetched)
	}
}

func TestWildcardReplaceNeverFetches(t *testing.T) {
	gomod := "module app\n\nrequire " + modA + " " + verA + "\n\n" +
		"replace " + modA + " => " + modFork + " " + verFork + "\n"
	fetcher := newFakeFetcher()
	_, err := New(Options{}).Verify(context.Background(), gomod, "", fetcher)
	if !errors.Is(err, ErrWildcardReplace) {
		t.Fatalf("expected ErrWildcardReplace, got %v", err)
	}
	if len(fetcher.fetched) != 0 {
		t.Fatalf("wildcard replace must not fetch, fetched %v", fetcher.fetched)
	}
}

func TestDuplicateRequireFails(t *testing.T) {
	gomod := "module app\n\nrequire (\n\t" + modA + " v1.0.0\n\t" + modA + " v2.0.0\n)\n"
	_, err := New(Options{}).Verify(context.Background(), gomod, "", newFakeFetcher())
	if !errors.Is(err, ErrDuplicateRequire) {
		t.Fatalf("expected ErrDuplicateRequire, got %v", err)
	}
}

func TestMalformedSourceURLFails(t *testing.T) {
	fetcher := newFakeFetcher()
	src := Source{URL: "://no-scheme", Protocol: "https"}
	blob, err := fetcher.add(modA, verA, modFiles(modA), src)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := sumLines(modA, verA, blob)
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(Options{}).Verify(context.Background(), requireOnly(), sum, fetcher)
	if !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("expected ErrInvalidSource for bad URL, got %v", err)
	}
}
