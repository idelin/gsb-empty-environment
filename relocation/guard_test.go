package relocation

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type fakeSource struct {
	mu     sync.Mutex
	chains map[Coordinate]*Coordinate
	calls  []Coordinate
}

func (f *fakeSource) Relocation(_ context.Context, coord Coordinate) (*Coordinate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, coord)
	return f.chains[coord], nil
}

func (f *fakeSource) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

type fakeDownloader struct {
	mu         sync.Mutex
	downloaded []Coordinate
}

func (d *fakeDownloader) Download(_ context.Context, coord Coordinate) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.downloaded = append(d.downloaded, coord)
	return nil
}

func (d *fakeDownloader) snapshots() []Coordinate {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Coordinate, len(d.downloaded))
	copy(out, d.downloaded)
	return out
}

func makeCoord(groupID string, index int) Coordinate {
	return Coordinate{
		GroupID:    groupID,
		ArtifactID: "lib",
		Version:    "1.0.0",
	}
}

func chain(source *fakeSource, groupIDs ...string) Coordinate {
	start := makeCoord(groupIDs[0], 0)
	current := start
	for index := 1; index < len(groupIDs); index++ {
		target := makeCoord(groupIDs[index], index)
		source.chains[current] = &target
		current = target
	}
	return start
}

func newTestGuard(source *fakeSource) *Guard {
	return &Guard{
		Registry: newTestRegistry(),
		Source:   source,
	}
}

func TestResolveFollowsRelocations(t *testing.T) {
	source := &fakeSource{chains: map[Coordinate]*Coordinate{}}
	start := chain(source, "com.acme.h0", "com.acme.h1", "org.acme.h2")
	guard := newTestGuard(source)

	resolved, err := guard.Resolve(context.Background(), start)
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if resolved.GroupID != "org.acme.h2" {
		t.Errorf("resolved groupId = %q, want org.acme.h2", resolved.GroupID)
	}
}

func TestResolveMaxHops(t *testing.T) {
	source := &fakeSource{chains: map[Coordinate]*Coordinate{}}
	start := chain(source,
		"com.acme.h0", "com.acme.h1", "com.acme.h2", "com.acme.h3", "com.acme.h4")
	guard := newTestGuard(source)

	if _, err := guard.Resolve(context.Background(), start); !errors.Is(err, ErrMaxHops) {
		t.Fatalf("Resolve error = %v, want ErrMaxHops", err)
	}

	downloader := &fakeDownloader{}
	if _, err := guard.Download(context.Background(), start, downloader); !errors.Is(err, ErrMaxHops) {
		t.Fatalf("Download error = %v, want ErrMaxHops", err)
	}
	if len(downloader.snapshots()) != 0 {
		t.Fatalf("downloader invoked %d times on rejected chain", len(downloader.snapshots()))
	}
}

func TestResolveExactlyThreeHopsAllowed(t *testing.T) {
	source := &fakeSource{chains: map[Coordinate]*Coordinate{}}
	start := chain(source,
		"com.acme.h0", "com.acme.h1", "com.acme.h2", "org.acme.h3")
	guard := newTestGuard(source)

	downloader := &fakeDownloader{}
	resolved, err := guard.Download(context.Background(), start, downloader)
	if err != nil {
		t.Fatalf("three-hop chain should be allowed, got error: %v", err)
	}
	if resolved.GroupID != "org.acme.h3" {
		t.Errorf("resolved groupId = %q, want org.acme.h3", resolved.GroupID)
	}
	if got := downloader.snapshots(); len(got) != 1 || got[0] != resolved {
		t.Fatalf("downloads = %v, want only the final coordinate", got)
	}
}

func TestResolveCycle(t *testing.T) {
	source := &fakeSource{chains: map[Coordinate]*Coordinate{}}
	start := chain(source, "com.acme.h0", "com.acme.h1", "com.acme.h2", "com.acme.h0")
	guard := newTestGuard(source)

	if _, err := guard.Resolve(context.Background(), start); !errors.Is(err, ErrCycle) {
		t.Fatalf("Resolve error = %v, want ErrCycle", err)
	}

	downloader := &fakeDownloader{}
	if _, err := guard.Download(context.Background(), start, downloader); !errors.Is(err, ErrCycle) {
		t.Fatalf("Download error = %v, want ErrCycle", err)
	}
	if len(downloader.snapshots()) != 0 {
		t.Fatalf("downloader invoked %d times on cyclic chain", len(downloader.snapshots()))
	}
}

func TestResolveCrossOwnerRejected(t *testing.T) {
	source := &fakeSource{chains: map[Coordinate]*Coordinate{}}
	start := chain(source, "com.acme.h0", "net.evil.payload")
	guard := newTestGuard(source)
	malicious := makeCoord("net.evil.payload", 1)

	if _, err := guard.Resolve(context.Background(), start); !errors.Is(err, ErrCrossOwner) {
		t.Fatalf("Resolve error = %v, want ErrCrossOwner", err)
	}

	downloader := &fakeDownloader{}
	if _, err := guard.Download(context.Background(), start, downloader); !errors.Is(err, ErrCrossOwner) {
		t.Fatalf("Download error = %v, want ErrCrossOwner", err)
	}
	for _, downloaded := range downloader.snapshots() {
		if downloaded == malicious {
			t.Fatal("malicious target artifact must never be downloaded")
		}
	}
	if len(downloader.snapshots()) != 0 {
		t.Fatalf("downloader invoked %d times on cross-owner relocation", len(downloader.snapshots()))
	}
}

func TestResolveApprovedCrossOwnerAllowed(t *testing.T) {
	source := &fakeSource{chains: map[Coordinate]*Coordinate{}}
	start := chain(source, "com.acme.h0", "net.evil.payload")
	guard := newTestGuard(source)
	if err := guard.Registry.Approve("net.evil.payload"); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	downloader := &fakeDownloader{}
	resolved, err := guard.Download(context.Background(), start, downloader)
	if err != nil {
		t.Fatalf("Download returned error: %v", err)
	}
	if resolved.GroupID != "net.evil.payload" {
		t.Errorf("resolved groupId = %q, want net.evil.payload", resolved.GroupID)
	}
	got := downloader.snapshots()
	if len(got) != 1 || got[0] != resolved {
		t.Fatalf("downloads = %v, want only the final coordinate %v", got, resolved)
	}
}

func TestResolvePathTraversalTargetRejected(t *testing.T) {
	maliciousGroupIDs := []string{
		"net.evil..",
		"..",
		"net.evil.\\..",
		"evil\\..\\..",
		"..\\..\\etc",
	}
	for _, maliciousGroupID := range maliciousGroupIDs {
		source := &fakeSource{chains: map[Coordinate]*Coordinate{}}
		start := makeCoord("com.acme.h0", 0)
		target := makeCoord(maliciousGroupID, 1)
		source.chains[start] = &target
		guard := newTestGuard(source)

		if _, err := guard.Resolve(context.Background(), start); err == nil {
			t.Errorf("malicious groupId %q was accepted", maliciousGroupID)
		}

		downloader := &fakeDownloader{}
		if _, err := guard.Download(context.Background(), start, downloader); err == nil {
			t.Errorf("Download accepted malicious groupId %q", maliciousGroupID)
		}
		if len(downloader.snapshots()) != 0 {
			t.Errorf("groupId %q: downloader invoked on rejected target", maliciousGroupID)
		}
	}
}

func TestDownloadRejectsInvalidStartCoordinate(t *testing.T) {
	source := &fakeSource{chains: map[Coordinate]*Coordinate{}}
	guard := newTestGuard(source)
	downloader := &fakeDownloader{}
	start := Coordinate{GroupID: "com..acme", ArtifactID: "lib", Version: "1.0.0"}

	if _, err := guard.Download(context.Background(), start, downloader); err == nil {
		t.Fatal("invalid start coordinate must be rejected")
	}
	if len(downloader.snapshots()) != 0 {
		t.Fatal("downloader invoked for invalid start coordinate")
	}
	if source.callCount() != 0 {
		t.Fatal("relocation source must not be consulted for invalid start coordinate")
	}
}

func TestGuardRequiresDependencies(t *testing.T) {
	ctx := context.Background()
	coord := makeCoord("com.acme.h0", 0)
	if _, err := (&Guard{}).Resolve(ctx, coord); err == nil {
		t.Error("guard without registry must fail")
	}
	if _, err := (&Guard{Registry: NewRegistry()}).Resolve(ctx, coord); err == nil {
		t.Error("guard without relocation source must fail")
	}
	if _, err := (&Guard{Registry: NewRegistry(), Source: &fakeSource{}}).Download(ctx, coord, nil); err == nil {
		t.Error("download without a downloader must fail")
	}
}
