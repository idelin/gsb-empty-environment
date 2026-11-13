package netsec

import (
	"errors"
	"strings"
	"testing"
)

func TestSchedulerSearchFindsNoAbuseInSafeModel(t *testing.T) {
	cfg := DefaultTwoChangeSearch()
	cfg.MaxDepth = 18
	cfg.Delay = int64(60 * 1e9)
	if trace, err := FindShortestUnsafeTrajectory(cfg); !errors.Is(err, ErrNoAbuseTrace) {
		t.Fatalf("safe model: trace=%+v err=%v, want ErrNoAbuseTrace", trace, err)
	}
}

func TestSchedulerSearchFindsShortestPendingRecovery(t *testing.T) {
	cfg := DefaultTwoChangeSearch()
	cfg.MaxDepth = 8
	cfg.Delay = int64(60 * 1e9)
	cfg.Flaws.PendingCanAuthenticate = true

	trace, err := FindShortestUnsafeTrajectory(cfg)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if trace.Steps != 1 {
		t.Fatalf("steps = %d, want 1: %+v", trace.Steps, trace.Trace)
	}
	if !strings.Contains(trace.Violation, "pending method") {
		t.Fatalf("violation = %q", trace.Violation)
	}
}

func TestSchedulerSearchFindsShortestPendingConfirmation(t *testing.T) {
	cfg := DefaultTwoChangeSearch()
	cfg.MaxDepth = 8
	cfg.Delay = int64(60 * 1e9)
	cfg.Flaws.PendingCanConfirm = true

	trace, err := FindShortestUnsafeTrajectory(cfg)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if trace.Steps != 1 {
		t.Fatalf("steps = %d, want 1: %+v", trace.Steps, trace.Trace)
	}
	if !strings.Contains(trace.Violation, "pending method counted") {
		t.Fatalf("violation = %q", trace.Violation)
	}
}

func TestSchedulerSearchFindsMutualPendingConfirmation(t *testing.T) {
	cfg := DefaultTwoChangeSearch()
	cfg.MaxDepth = 8
	cfg.Delay = int64(60 * 1e9)
	cfg.InitialConfirmations = 0
	cfg.RequiredConfirmations = 2
	cfg.Flaws.PendingCanConfirm = true

	trace, err := FindShortestUnsafeTrajectory(cfg)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if trace.Steps != 2 {
		t.Fatalf("steps = %d, want 2: %+v", trace.Steps, trace.Trace)
	}
	if !strings.Contains(trace.Violation, "each other") {
		t.Fatalf("violation = %q", trace.Violation)
	}
}

func TestSchedulerSearchFindsEarlyActivation(t *testing.T) {
	cfg := DefaultTwoChangeSearch()
	cfg.MaxDepth = 8
	cfg.Delay = int64(60 * 1e9)
	cfg.InitialConfirmations = 2
	cfg.Flaws.AllowEarlyActivation = true

	trace, err := FindShortestUnsafeTrajectory(cfg)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if trace.Steps != 1 {
		t.Fatalf("steps = %d, want 1: %+v", trace.Steps, trace.Trace)
	}
	if !strings.Contains(trace.Violation, "before") {
		t.Fatalf("violation = %q", trace.Violation)
	}
}
