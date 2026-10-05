package atomicpub

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func versionedFiles(tag string) []File {
	return []File{
		{Name: "a.txt", Data: []byte("a-" + tag)},
		{Name: "b.txt", Data: []byte("b-" + tag)},
	}
}

func TestPublishAndRead(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Acquire(); err != ErrEmpty {
		t.Fatalf("want ErrEmpty, got %v", err)
	}

	v1, err := s.Publish(versionedFiles("one"))
	if err != nil {
		t.Fatal(err)
	}
	if v1 != "v-000001" || s.Current() != v1 {
		t.Fatalf("v1=%q current=%q", v1, s.Current())
	}

	r, err := s.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Read("a.txt")
	if err != nil || string(got) != "a-one" {
		t.Fatalf("got %q err %v", got, err)
	}

	if _, err := s.Publish(versionedFiles("two")); err != nil {
		t.Fatal(err)
	}

	// Old handle keeps reading the complete old version.
	if r.Version() != v1 {
		t.Fatalf("reader version %q", r.Version())
	}
	got, err = r.Read("b.txt")
	if err != nil || string(got) != "b-one" {
		t.Fatalf("old reader got %q err %v", got, err)
	}

	// New readers see the complete new version.
	r2, err := s.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	got, err = r2.Read("a.txt")
	if err != nil || string(got) != "a-two" {
		t.Fatalf("new reader got %q err %v", got, err)
	}
	r.Close()
	r2.Close()
}

func TestOldReaderSurvivesGC(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	v1, _ := s.Publish(versionedFiles("one"))
	r, _ := s.Acquire()

	if _, err := s.Publish(versionedFiles("two")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(versionedFiles("three")); err != nil {
		t.Fatal(err)
	}

	// v1 is pinned by r, so it must still be fully readable.
	for _, f := range []string{"a.txt", "b.txt"} {
		got, err := r.Read(f)
		if err != nil || !strings.HasSuffix(string(got), "-one") {
			t.Fatalf("pinned read %s: %q err %v", f, got, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, v1)); err != nil {
		t.Fatalf("pinned version dir missing: %v", err)
	}

	// Unpinned old versions are collected.
	if _, err := os.Stat(filepath.Join(dir, "v-000002")); !os.IsNotExist(err) {
		t.Fatalf("unpinned v-000002 should be collected, stat err=%v", err)
	}

	r.Close()
	if _, err := s.Publish(versionedFiles("four")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, v1)); !os.IsNotExist(err) {
		t.Fatalf("released v1 should be collected, stat err=%v", err)
	}
}

// TestConcurrentReadersNeverMixed hammers the store with a publisher and
// readers; every read pair must come from a single consistent version.
func TestConcurrentReadersNeverMixed(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(versionedFiles("v000000")); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	errs := make(chan error, 64)

	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				r, err := s.Acquire()
				if err != nil {
					errs <- err
					return
				}
				a, errA := r.Read("a.txt")
				b, errB := r.Read("b.txt")
				r.Close()
				if errA != nil || errB != nil {
					errs <- fmt.Errorf("read: %v / %v", errA, errB)
					return
				}
				ta := strings.TrimPrefix(string(a), "a-")
				tb := strings.TrimPrefix(string(b), "b-")
				if ta != tb {
					errs <- fmt.Errorf("mixed version: a=%q b=%q", a, b)
					return
				}
			}
		}()
	}

	for i := 0; i < 50; i++ {
		if _, err := s.Publish(versionedFiles(fmt.Sprintf("v%06d", i))); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestRejectsBadFileNames(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, nil)
	for _, name := range []string{"", manifestName, "a/b", "..", `a\b`} {
		if _, err := s.Publish([]File{{Name: name, Data: []byte("x")}}); err == nil {
			t.Fatalf("name %q should be rejected", name)
		}
	}
	if s.Current() != "" {
		t.Fatal("failed publishes must not change current")
	}
}
