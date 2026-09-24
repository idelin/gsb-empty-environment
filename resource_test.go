package canonicalkey

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMemoryCatalogCloseRejectsLaterUse(t *testing.T) {
	catalog := NewMemoryCatalog(DefaultHash)
	if err := catalog.Close(); err != nil {
		t.Fatal(err)
	}
	_, _, err := catalog.GetOrInsert(context.Background(), []byte("x"), time.Unix(0, 0).UTC())
	if !errors.Is(err, ErrCatalogClosed) {
		t.Fatalf("closed catalog error = %v, want %v", err, ErrCatalogClosed)
	}
}

func TestDepthBudget(t *testing.T) {
	limits := Limits{MaxInputBytes: 1 << 20, MaxCanonicalBytes: 1 << 20, MaxDepth: 2}
	request := Request{"tenant-a", "search", PresentString("cfg-3"), PresentParams(Object{
		"a": Object{"b": Object{"c": Number("1")}},
	})}
	_, err := CanonicalRequest(request, limits)
	if !errors.Is(err, ErrDepthExceeded) {
		t.Fatalf("depth error = %v, want %v", err, ErrDepthExceeded)
	}
}

func TestCanonicalByteBudget(t *testing.T) {
	request := Request{"tenant-a", "search", PresentString("cfg-3"), PresentParams(strings.Repeat("x", 100))}
	_, err := CanonicalRequest(request, Limits{MaxInputBytes: 1 << 20, MaxCanonicalBytes: 30, MaxDepth: 2})
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("canonical budget error = %v, want %v", err, ErrBudgetExceeded)
	}
}
