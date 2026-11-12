package netsec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestWriteFailureLeavesCompletePreviousVersion injects a failure at every
// stage of a second-version commit. After each failed transaction the current
// pointer must still resolve to the complete, verified first version, and no
// partial version directory may be visible.
func TestWriteFailureLeavesCompletePreviousVersion(t *testing.T) {
	stages := []failStage{
		FailStageContent,
		FailStageManifest,
		FailStagePublish,
		FailStageCurrent,
	}

	for _, stage := range stages {
		t.Run(stageName(stage), func(t *testing.T) {
			store := newTestStore(t)
			first := mustCommit(t, store, "doc", []byte("v1-payload"), &Label{Level: LevelConfidential})

			store.setFailAt(stage)
			_, err := store.Commit(context.Background(), CommitInput{
				Object:  "doc",
				Content: []byte("v2-payload"),
				Label:   &Label{Level: LevelSecret},
			})
			if !errors.Is(err, errInjectedFailure) {
				t.Fatalf("Commit: got %v, want injected failure", err)
			}

			current, err := store.CurrentVersion("doc")
			if err != nil {
				t.Fatalf("CurrentVersion after failure: %v", err)
			}
			if current != first.Version() {
				t.Fatalf("current = v%d, want unchanged v%d", current, first.Version())
			}

			handle, err := store.Open("doc")
			if err != nil {
				t.Fatalf("Open after failure: %v", err)
			}
			if string(handle.Content()) != "v1-payload" || handle.Label().Level != LevelConfidential {
				t.Fatalf("old version altered after failed commit: %q / %v",
					handle.Content(), handle.Label())
			}

			// No v2 directory may be left behind as a reachable version.
			if _, err := os.Stat(store.versionDir("doc", 2)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("partial v2 directory survived, stat err = %v", err)
			}
			entries, err := os.ReadDir(filepath.Join(store.objectRoot("doc"), "staging"))
			if err != nil {
				t.Fatalf("read staging: %v", err)
			}
			if len(entries) != 0 {
				t.Fatalf("staging not cleaned up: %v", entries)
			}

			// The store must still accept a subsequent successful commit, and
			// version numbering must not have been consumed by the failure.
			next := mustCommit(t, store, "doc", []byte("v2-payload"), &Label{Level: LevelSecret})
			if next.Version() != 2 {
				t.Fatalf("next version = %d, want 2 (failed tx must not consume a version)", next.Version())
			}
			if v, _ := store.CurrentVersion("doc"); v != 2 {
				t.Fatalf("current = v%d, want v2", v)
			}
		})
	}
}

func stageName(stage failStage) string {
	switch stage {
	case FailStageContent:
		return "content"
	case FailStageManifest:
		return "manifest"
	case FailStagePublish:
		return "publish"
	case FailStageCurrent:
		return "current"
	default:
		return "unknown"
	}
}

// TestConcurrentReadersSeeOnlyCompleteVersions holds several commit
// transactions at the pre-lock gate while readers run, then releases them so
// the atomic version switch happens under concurrent reads. Every read must
// return a handle whose digest verifies and whose content matches the label
// bound to that exact, fully published version: never a torn intermediate.
func TestConcurrentReadersSeeOnlyCompleteVersions(t *testing.T) {
	store := newTestStore(t)

	const totalVersions = 16

	// Precompute the exact content and label bound to each version. Because a
	// reader may observe a newly committed version the instant it is published,
	// expectations must exist before the transaction runs.
	wantContent := make(map[int64]string, totalVersions)
	wantLevel := make(map[int64]Level, totalVersions)
	for v := int64(1); v <= totalVersions; v++ {
		wantContent[v] = fmt.Sprintf("content-v%d", v)
		// Monotonic, non-decreasing labels so each commit is a valid upgrade
		// (no downgrade approval needed); cap at the highest level once reached.
		lvl := int(v) - 1
		if lvl > int(LevelTopSecret) {
			lvl = int(LevelTopSecret)
		}
		wantLevel[v] = Level(lvl)
	}

	first := mustCommit(t, store, "doc", []byte(wantContent[1]), &Label{Level: wantLevel[1]})

	// launchParked starts a commit that blocks at the pre-lock gate and
	// reports completion on the returned channel.
	launchParked := func(version int64) <-chan struct{} {
		done := make(chan struct{})
		go func() {
			handle, err := store.Commit(context.Background(), CommitInput{
				Object:  "doc",
				Content: []byte(wantContent[version]),
				Label:   &Label{Level: wantLevel[version]},
			})
			if err != nil {
				t.Errorf("commit v%d: %v", version, err)
			} else if handle.Version() != version {
				t.Errorf("committed v%d, want v%d", handle.Version(), version)
			}
			close(done)
		}()
		return done
	}

	verifyHandle := func(handle *Handle) error {
		version := handle.Version()
		if version < first.Version() || version > totalVersions {
			return fmt.Errorf("observed version v%d outside complete-version range", version)
		}
		if string(handle.Content()) != wantContent[version] {
			return fmt.Errorf("v%d content = %q, want %q", version, handle.Content(), wantContent[version])
		}
		if handle.Label() == nil || handle.Label().Level != wantLevel[version] {
			return fmt.Errorf("v%d label inconsistent with committed version", version)
		}
		if handle.Digest() != sha256Hex(handle.Content()) {
			return fmt.Errorf("v%d digest does not bind returned content", version)
		}
		pinned, err := store.OpenVersion("doc", version)
		if err != nil || string(pinned.Content()) != wantContent[version] ||
			pinned.Label().Level != wantLevel[version] {
			return fmt.Errorf("v%d pinned handle inconsistent: %v", version, err)
		}
		return handle.Authorize(LevelTopSecret)
	}

	stop := make(chan struct{})
	gate := newPublishGate()
	store.setPublishGate(gate)

	var readers sync.WaitGroup
	var observedMu sync.Mutex
	observed := make(map[int64]bool)
	var readerErr error
	var readerErrOnce sync.Once

	for worker := 0; worker < 4; worker++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			ticker := time.NewTicker(time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
				}
				handle, err := store.Open("doc")
				if err != nil {
					readerErrOnce.Do(func() { readerErr = fmt.Errorf("Open: %w", err) })
					return
				}
				if err := verifyHandle(handle); err != nil {
					readerErrOnce.Do(func() { readerErr = err })
					return
				}
				observedMu.Lock()
				observed[handle.Version()] = true
				observedMu.Unlock()
			}
		}()
	}

	// Commits are serialized by the store's write lock, so launch them in
	// order and let each one park at the pre-lock gate. Readers run throughout;
	// while a commit is parked the current pointer stays at the complete
	// previous version, and releasing the gate performs the atomic switch.
	for v := int64(2); v <= totalVersions; v++ {
		done := launchParked(v)
		gate.WaitFor(1)
		if cur, err := store.CurrentVersion("doc"); err != nil || cur != v-1 {
			t.Fatalf("while v%d parked, current = v%d (err %v), want v%d", v, cur, err, v-1)
		}
		gate.Allow()
		<-done
		gate.Reset()
	}
	store.setPublishGate(nil)

	// Give readers a final window to observe the new head, then stop them.
	time.Sleep(20 * time.Millisecond)
	close(stop)
	readers.Wait()
	if readerErr != nil {
		t.Fatal(readerErr)
	}

	// Readers must have actually observed multiple complete versions, proving
	// the old/new transition was exercised concurrently.
	if len(observed) < 2 {
		t.Fatalf("readers only observed %d distinct version(s), expected overlap across versions", len(observed))
	}
	if !observed[1] || !observed[totalVersions] {
		t.Fatalf("readers did not span the old/new boundary: observed %v", observed)
	}

	final, err := store.Open("doc")
	if err != nil {
		t.Fatalf("final Open: %v", err)
	}
	if final.Version() != totalVersions {
		t.Fatalf("final version = %d, want %d", final.Version(), totalVersions)
	}
	if string(final.Content()) != wantContent[totalVersions] {
		t.Fatalf("final content mismatch: %q", final.Content())
	}
}

// TestDigestTamperingIsDetected corrupts the stored bytes and confirms the
// handle refuses to bind them, preventing a label from authorizing content it
// was never attached to.
func TestDigestTamperingIsDetected(t *testing.T) {
	store := newTestStore(t)
	mustCommit(t, store, "doc", []byte("original"), &Label{Level: LevelSecret})

	tampered := append([]byte(nil), []byte("original")...)
	tampered[0] = 'X'
	if err := os.WriteFile(
		filepath.Join(store.versionDir("doc", 1), "content.bin"),
		tampered, 0o600,
	); err != nil {
		t.Fatalf("tamper content: %v", err)
	}

	if _, err := store.Open("doc"); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("Open tampered version: got %v, want ErrDigestMismatch", err)
	}
}
