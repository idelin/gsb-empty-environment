package netsec

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func mustPublish(t *testing.T, s *Store, key string, content []byte, label Label, opts ...PublishOption) *Handle {
	t.Helper()
	h, err := s.Publish(key, content, label, opts...)
	if err != nil {
		t.Fatalf("Publish(%q) failed: %v", key, err)
	}
	return h
}

func mustGet(t *testing.T, s *Store, key string) *Handle {
	t.Helper()
	h, err := s.Get(key)
	if err != nil {
		t.Fatalf("Get(%q) failed: %v", key, err)
	}
	return h
}

func TestPublishBindsDigestLabelAndContent(t *testing.T) {
	s := NewStore()
	input := []byte("release contents")
	label := Label{Name: "secret", Level: 3}

	h := mustPublish(t, s, "app", input, label)

	wantDigest := Digest(sha256.Sum256(input))
	if got := h.Digest(); got != wantDigest {
		t.Fatalf("Digest() = %s, want %s", got, wantDigest)
	}
	if got := h.Label(); got != label {
		t.Fatalf("Label() = %v, want %v", got, label)
	}
	if got := h.Version(); got != 1 {
		t.Fatalf("Version() = %d, want 1", got)
	}
	if got := string(h.Content()); got != string(input) {
		t.Fatalf("Content() = %q, want %q", got, input)
	}
	if !h.Verify() {
		t.Fatal("Verify() = false, want true")
	}

	input[0] = 'X'
	if got := string(h.Content()); got != "release contents" {
		t.Fatalf("published content changed through caller's slice: %q", got)
	}

	view := h.Content()
	view[0] = 'Y'
	if got := string(h.Content()); got != "release contents" {
		t.Fatalf("published content changed through Content() copy: %q", got)
	}
}

func TestMissingLabelIsDenied(t *testing.T) {
	s := NewStore()

	if _, err := s.Publish("app", []byte("data"), Label{}); !errors.Is(err, ErrMissingLabel) {
		t.Fatalf("Publish with missing label error = %v, want ErrMissingLabel", err)
	}
	if _, err := s.Get("app"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after rejected publish error = %v, want ErrNotFound", err)
	}

	legacy := []byte("legacy data")
	s.mu.Lock()
	s.objects["legacy"] = &version{
		number:  1,
		digest:  Digest(sha256.Sum256(legacy)),
		content: legacy,
	}
	s.mu.Unlock()

	h, err := s.Get("legacy")
	if !errors.Is(err, ErrMissingLabel) {
		t.Fatalf("Get with missing label error = %v, want ErrMissingLabel", err)
	}
	if h != nil {
		t.Fatalf("Get with missing label returned handle %+v, want nil", h)
	}
}

func TestDowngradeRequiresMatchingApproval(t *testing.T) {
	s := NewStore()
	secret := Label{Name: "secret", Level: 3}
	internal := Label{Name: "internal", Level: 2}
	public := Label{Name: "public", Level: 0}

	v1 := mustPublish(t, s, "app", []byte("v1"), secret)

	if _, err := s.Publish("app", []byte("v2"), internal); !errors.Is(err, ErrDowngrade) {
		t.Fatalf("downgrade without approval error = %v, want ErrDowngrade", err)
	}
	if _, err := s.Publish("app", []byte("v2"), internal, ApproveDowngrade(0)); !errors.Is(err, ErrDowngrade) {
		t.Fatalf("downgrade with stale approval error = %v, want ErrDowngrade", err)
	}
	if _, err := s.Publish("app", []byte("v2"), internal, ApproveDowngrade(2)); !errors.Is(err, ErrDowngrade) {
		t.Fatalf("downgrade with future approval error = %v, want ErrDowngrade", err)
	}

	got := mustGet(t, s, "app")
	if got.Version() != v1.Version() || got.Label() != secret {
		t.Fatalf("rejected downgrade changed current release: version=%d label=%v", got.Version(), got.Label())
	}

	v2 := mustPublish(t, s, "app", []byte("v2"), internal, ApproveDowngrade(1))
	if v2.Version() != 2 || v2.Label() != internal {
		t.Fatalf("approved downgrade = version %d label %v, want version 2 label %v", v2.Version(), v2.Label(), internal)
	}

	v3 := mustPublish(t, s, "app", []byte("v3"), public, ApproveDowngrade(2))
	if v3.Version() != 3 || v3.Label() != public {
		t.Fatalf("second approved downgrade = version %d label %v, want version 3 label %v", v3.Version(), v3.Label(), public)
	}

	v4 := mustPublish(t, s, "app", []byte("v4"), secret)
	if v4.Version() != 4 || v4.Label() != secret {
		t.Fatalf("upgrade = version %d label %v, want version 4 label %v", v4.Version(), v4.Label(), secret)
	}

	v5 := mustPublish(t, s, "app", []byte("v5"), Label{Name: "secret-alt", Level: 3})
	if v5.Version() != 5 {
		t.Fatalf("same-level relabel version = %d, want 5", v5.Version())
	}
}

func TestStaleApprovalCannotDowngradeNewerVersion(t *testing.T) {
	s := NewStore()
	high := Label{Name: "top-secret", Level: 4}
	low := Label{Name: "public", Level: 0}

	mustPublish(t, s, "app", []byte("v1"), high)
	mustPublish(t, s, "app", []byte("v2"), high)

	if _, err := s.Publish("app", []byte("v3"), low, ApproveDowngrade(1)); !errors.Is(err, ErrDowngrade) {
		t.Fatalf("stale approval error = %v, want ErrDowngrade", err)
	}

	got := mustGet(t, s, "app")
	if got.Version() != 2 || got.Label() != high || string(got.Content()) != "v2" {
		t.Fatalf("stale approval changed release: version=%d label=%v content=%q", got.Version(), got.Label(), got.Content())
	}
}

func TestCommitFailureLeavesPreviousReleaseIntact(t *testing.T) {
	var fail atomic.Bool
	var sawCommit atomic.Bool
	s := NewStore(WithCommitHook(func(c Commit) error {
		sawCommit.Store(true)
		if c.Digest != Digest(sha256.Sum256(c.Content)) {
			return errors.New("digest does not match commit content")
		}
		if !c.Label.Valid() {
			return errors.New("commit has no label")
		}
		if fail.Load() {
			return errors.New("injected disk failure")
		}
		return nil
	}))

	oldContent := []byte("old complete release")
	oldLabel := Label{Name: "secret", Level: 3}
	v1 := mustPublish(t, s, "app", oldContent, oldLabel)

	fail.Store(true)
	_, err := s.Publish("app", []byte("new partial release"), Label{Name: "public", Level: 0}, ApproveDowngrade(1))
	if err == nil {
		t.Fatal("Publish with failing commit returned nil error")
	}
	if !strings.Contains(err.Error(), "injected disk failure") {
		t.Fatalf("Publish error = %v, want injected commit failure", err)
	}

	got := mustGet(t, s, "app")
	if got.Version() != v1.Version() {
		t.Fatalf("failed commit advanced version to %d, want %d", got.Version(), v1.Version())
	}
	if got.Label() != oldLabel {
		t.Fatalf("failed commit changed label to %v, want %v", got.Label(), oldLabel)
	}
	if string(got.Content()) != string(oldContent) {
		t.Fatalf("failed commit changed content to %q, want %q", got.Content(), oldContent)
	}
	if got.Digest() != v1.Digest() || !got.Verify() {
		t.Fatalf("failed commit changed digest or broke verification: %s", got.Digest())
	}

	if _, err := s.Publish("never-created", []byte("data"), Label{Name: "public", Level: 0}); err == nil {
		t.Fatal("first publish with failing commit returned nil error")
	}
	if _, err := s.Get("never-created"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after failed first publish error = %v, want ErrNotFound", err)
	}

	fail.Store(false)
	v2 := mustPublish(t, s, "app", []byte("new complete release"), Label{Name: "public", Level: 0}, ApproveDowngrade(1))
	if v2.Version() != 2 {
		t.Fatalf("recovery publish version = %d, want 2", v2.Version())
	}
	if v1.Version() != 1 || string(v1.Content()) != string(oldContent) || v1.Label() != oldLabel {
		t.Fatal("old handle changed after a newer release")
	}
	if !sawCommit.Load() {
		t.Fatal("commit hook was never called")
	}
}

func TestReadersSeeExactlyOldOrNewRelease(t *testing.T) {
	s := NewStore()
	oldContent := []byte("old complete release")
	oldLabel := Label{Name: "secret", Level: 3}
	old := mustPublish(t, s, "app", oldContent, oldLabel)

	newContent := []byte("new complete release")
	newLabel := Label{Name: "public", Level: 0}

	const readerCount = 8
	var started atomic.Int64
	var observedNew atomic.Int64
	stop := make(chan struct{})
	errCh := make(chan error, readerCount)
	var wg sync.WaitGroup

	for i := 0; i < readerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			first := true
			for {
				h, err := s.Get("app")
				if err != nil {
					errCh <- err
					return
				}

				switch h.Version() {
				case old.Version():
					if h.Label() != oldLabel || string(h.Content()) != string(oldContent) || h.Digest() != old.Digest() {
						errCh <- fmt.Errorf("old handle was mixed: version=%d label=%v content=%q digest=%s",
							h.Version(), h.Label(), h.Content(), h.Digest())
						return
					}
				case 2:
					if h.Label() != newLabel || string(h.Content()) != string(newContent) || h.Digest() != mustDigest(newContent) {
						errCh <- fmt.Errorf("new handle was mixed: version=%d label=%v content=%q digest=%s",
							h.Version(), h.Label(), h.Content(), h.Digest())
						return
					}
					observedNew.Add(1)
				default:
					errCh <- fmt.Errorf("observed unexpected version %d", h.Version())
					return
				}
				if !h.Verify() {
					errCh <- fmt.Errorf("handle version %d failed verification", h.Version())
					return
				}

				if first {
					first = false
					started.Add(1)
				}
				select {
				case <-stop:
					return
				default:
				}
			}
		}()
	}

	deadline := time.Now().Add(5 * time.Second)
	for started.Load() < readerCount {
		if time.Now().After(deadline) {
			close(stop)
			wg.Wait()
			t.Fatal("readers did not observe the old release in time")
		}
		runtime.Gosched()
	}

	mustPublish(t, s, "app", newContent, newLabel, ApproveDowngrade(old.Version()))

	deadline = time.Now().Add(5 * time.Second)
	for observedNew.Load() < readerCount {
		if time.Now().After(deadline) {
			close(stop)
			wg.Wait()
			t.Fatal("readers did not observe the new release in time")
		}
		runtime.Gosched()
	}

	close(stop)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}

func mustDigest(content []byte) Digest {
	return Digest(sha256.Sum256(content))
}

func TestConcurrentReadsObserveCompleteReleases(t *testing.T) {
	var attempts atomic.Uint64
	s := NewStore(WithCommitHook(func(c Commit) error {
		if n := attempts.Add(1); n%7 == 0 {
			return errors.New("injected commit failure")
		}
		if c.Digest != Digest(sha256.Sum256(c.Content)) {
			return errors.New("commit digest mismatch")
		}
		if !c.Label.Valid() {
			return errors.New("commit label missing")
		}
		return nil
	}))

	initialContent := []byte(encodeRelease("public", 0, 0))
	mustPublish(t, s, "app", initialContent, Label{Name: "public", Level: 0})

	const (
		writeAttempts = 2000
		readerCount   = 8
	)

	stop := make(chan struct{})
	errCh := make(chan error, readerCount+1)
	var wg sync.WaitGroup

	for i := 0; i < readerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var lastVersion uint64
			for {
				select {
				case <-stop:
					return
				default:
				}

				h, err := s.Get("app")
				if err != nil {
					errCh <- err
					return
				}
				if !h.Verify() {
					errCh <- fmt.Errorf("handle version %d failed digest verification", h.Version())
					return
				}

				name, level, generation, ok := decodeRelease(h.Content())
				if !ok {
					errCh <- fmt.Errorf("handle version %d has malformed content %q", h.Version(), h.Content())
					return
				}
				if h.Label().Name != name || h.Label().Level != level {
					errCh <- fmt.Errorf("torn release: content label %s/%d, handle label %s/%d",
						name, level, h.Label().Name, h.Label().Level)
					return
				}
				if generation < 0 {
					errCh <- fmt.Errorf("negative generation %d", generation)
					return
				}
				if h.Version() < lastVersion {
					errCh <- fmt.Errorf("version went backwards: %d then %d", lastVersion, h.Version())
					return
				}
				lastVersion = h.Version()
			}
		}()
	}

	var (
		currentVersion uint64 = 1
		currentLevel          = 0
		successes      uint64 = 1
		failures       uint64
	)
	for attempt := 1; attempt <= writeAttempts; attempt++ {
		name, level := "public", 0
		if attempt%2 == 1 {
			name, level = "secret", 3
		}
		content := []byte(encodeRelease(name, level, attempt))

		var opts []PublishOption
		if level < currentLevel {
			opts = append(opts, ApproveDowngrade(currentVersion))
		}

		_, err := s.Publish("app", content, Label{Name: name, Level: level}, opts...)
		if err != nil {
			if !strings.Contains(err.Error(), "injected commit failure") {
				t.Fatalf("unexpected publish error: %v", err)
			}
			failures++
		} else {
			successes++
			currentVersion++
			currentLevel = level
		}
	}
	close(stop)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Error(err)
	}

	final := mustGet(t, s, "app")
	if final.Version() != successes {
		t.Fatalf("final version = %d, want %d successful publishes", final.Version(), successes)
	}
	if failures == 0 {
		t.Fatal("test did not inject any commit failures")
	}
}

func encodeRelease(name string, level, generation int) string {
	return fmt.Sprintf("%s|%d|%d", name, level, generation)
}

func decodeRelease(content []byte) (name string, level, generation int, ok bool) {
	parts := strings.Split(string(content), "|")
	if len(parts) != 3 || parts[0] == "" {
		return "", 0, 0, false
	}
	level, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", 0, 0, false
	}
	generation, err = strconv.Atoi(parts[2])
	if err != nil {
		return "", 0, 0, false
	}
	return parts[0], level, generation, true
}
