package netsec

import (
	"errors"
	"testing"
	"time"
)

func validManifest() Manifest {
	return testManifest(1, 1, []CheckID{"av"}, time.Second)
}

func TestManifestValidationBranches(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(Manifest) Manifest
		wantErr error
	}{
		{name: "negative generation", mutate: func(m Manifest) Manifest {
			m.Generation = -1
			return m
		}, wantErr: ErrInvalidManifest},
		{name: "no chunks", mutate: func(m Manifest) Manifest {
			m.Chunks = nil
			return m
		}, wantErr: ErrInvalidManifest},
		{name: "non contiguous index", mutate: func(m Manifest) Manifest {
			m.Chunks[0].Index = 7
			return m
		}, wantErr: ErrInvalidManifest},
		{name: "negative length", mutate: func(m Manifest) Manifest {
			m.Chunks[0].Length = -1
			return m
		}, wantErr: ErrInvalidManifest},
		{name: "missing digest algorithm", mutate: func(m Manifest) Manifest {
			m.Chunks[0].Digest.Algorithm = ""
			return m
		}, wantErr: ErrInvalidManifest},
		{name: "missing digest hex", mutate: func(m Manifest) Manifest {
			m.Chunks[0].Digest.Hex = ""
			return m
		}, wantErr: ErrInvalidManifest},
		{name: "no checks", mutate: func(m Manifest) Manifest {
			m.Checks = nil
			return m
		}, wantErr: ErrInvalidManifest},
		{name: "empty check id", mutate: func(m Manifest) Manifest {
			m.Checks[0] = ""
			return m
		}, wantErr: ErrInvalidManifest},
		{name: "duplicate check", mutate: func(m Manifest) Manifest {
			m.Checks = []CheckID{"av", "av"}
			return m
		}, wantErr: ErrInvalidManifest},
		{name: "negative timeout", mutate: func(m Manifest) Manifest {
			m.Timeout = -1
			return m
		}, wantErr: ErrInvalidManifest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.mutate(validManifest()).Validate(); !errors.Is(err, tc.wantErr) {
				t.Fatalf("Validate err = %v, want %v", err, tc.wantErr)
			}
		})
	}

	if err := validManifest().Validate(); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
}

func TestReceiptValidationBranches(t *testing.T) {
	base := passReceipt(1, 0, "av")
	cases := []struct {
		name    string
		receipt Receipt
	}{
		{name: "negative generation", receipt: Receipt{Generation: -1, ChunkIndex: 0, Check: "av", Verdict: VerdictPass}},
		{name: "negative chunk", receipt: Receipt{Generation: 1, ChunkIndex: -1, Check: "av", Verdict: VerdictPass}},
		{name: "empty check", receipt: Receipt{Generation: 1, ChunkIndex: 0, Check: "", Verdict: VerdictPass}},
		{name: "unknown verdict", receipt: Receipt{Generation: 1, ChunkIndex: 0, Check: "av", Verdict: VerdictUnknown}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.receipt.Validate(); !errors.Is(err, ErrInvalidReceipt) {
				t.Fatalf("Validate err = %v, want %v", err, ErrInvalidReceipt)
			}
		})
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid receipt rejected: %v", err)
	}
}

func TestStringMethods(t *testing.T) {
	if got := VerdictUnknown.String(); got != "unknown" {
		t.Fatalf("verdict string = %q", got)
	}
	for verdict, want := range map[CheckVerdict]string{
		VerdictPass:     "pass",
		VerdictPositive: "positive",
		VerdictFailed:   "failed",
	} {
		if got := verdict.String(); got != want {
			t.Fatalf("%d string = %q, want %q", verdict, got, want)
		}
	}
	for status, want := range map[Status]string{
		StatusIncomplete: "incomplete",
		StatusPass:       "pass",
		StatusBlocked:    "blocked",
	} {
		if got := status.String(); got != want {
			t.Fatalf("%d string = %q, want %q", status, got, want)
		}
	}
	for reason, want := range map[Reason]string{
		ReasonAwaitingManifest: "awaiting_manifest",
		ReasonAwaitingReceipts: "awaiting_receipts",
		ReasonTimeout:          "timeout",
		ReasonCheckFailed:      "check_failed",
		ReasonCancelled:        "cancelled",
		ReasonAllClear:         "all_clear",
		ReasonPositive:         "positive",
	} {
		if got := reason.String(); got != want {
			t.Fatalf("%d string = %q, want %q", reason, got, want)
		}
	}
}

func TestFakeSchedulerNowAndZeroDuration(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	scheduler := NewFakeScheduler(start)
	if !scheduler.Now().Equal(start) {
		t.Fatalf("Now = %v, want %v", scheduler.Now(), start)
	}

	fired := make(chan struct{}, 1)
	cancel := scheduler.After(0, func() { fired <- struct{}{} })
	scheduler.Advance(0)
	select {
	case <-fired:
	default:
		t.Fatal("zero-duration timer must fire on Advance")
	}

	// A separately canceled timer must neither fire nor stay pending.
	canceled := make(chan struct{}, 1)
	cancel2 := scheduler.After(time.Second, func() { canceled <- struct{}{} })
	cancel2()
	scheduler.Advance(time.Second)
	select {
	case <-canceled:
		t.Fatal("canceled timer must not fire")
	default:
	}
	if scheduler.Pending() != 0 {
		t.Fatalf("pending = %d, want zero after advance", scheduler.Pending())
	}
	cancel() // canceling an already fired timer is a safe no-op
}

// TestWallSchedulerEndToEnd exercises the production timer path.
func TestWallSchedulerEndToEnd(t *testing.T) {
	aggregator := NewAggregator()
	if err := aggregator.InstallManifest(testManifest(1, 1, []CheckID{"av"}, 20*time.Millisecond)); err != nil {
		t.Fatalf("install: %v", err)
	}
	if aggregator.Generation() != 1 {
		t.Fatalf("generation = %d, want 1", aggregator.Generation())
	}

	decision := <-aggregator.Final()
	if decision.Status != StatusIncomplete || decision.Reason != ReasonTimeout {
		t.Fatalf("decision = %+v, want wall-clock timeout", decision)
	}
}
