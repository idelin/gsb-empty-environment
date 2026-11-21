package recovery

import (
	"fmt"
	"reflect"
)

// Expected describes the single visible outcome a crash prefix must produce.
// Set=false means recovery must produce no receipt and no apply callback.
type Expected struct {
	Set         bool
	Deleted     bool
	Generation  uint64
	Txn         TxID
	Blocks      []Block
	MinRollback bool
}

// CrashCase is one independently restored pair of raw stable images. The two
// images may differ in foreign tenant lanes, but the tested tenant lane must be
// equivalent for a no-divergence assertion.
type CrashCase struct {
	Name     string
	Left     RawState
	Right    RawState
	Expected Expected
}

// VerifyConfig defines the independent crash-prefix checker.
type VerifyConfig struct {
	Layout          Layout
	Keys            Keyring
	FrameSize       int
	MaxRetries      int
	Tenant          TenantID
	Lease           Lease
	MinGeneration   uint64
	ForbiddenTenant TenantID
}

// VerificationReport summarizes exhaustive prefix checks.
type VerificationReport struct {
	Prefixes       int
	ReadEvents     int
	ApplyCallbacks int
	MaxPrefixReads int
}

// VerifyCrashPrefixes is independent of any Engine instance. For every raw
// crash prefix it restores two fresh stores, runs a fresh engine on each,
// compares the complete host-visible I/O trace and selected result, invokes the
// apply callback exactly once for acknowledged outcomes, and rejects foreign
// tenant data or reads outside the configured lane.
func VerifyCrashPrefixes(cfg VerifyConfig, cases []CrashCase) (VerificationReport, error) {
	var report VerificationReport
	if cfg.FrameSize == 0 {
		cfg.FrameSize = defaultFrameSize
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = defaultMaxRetries
	}
	if cfg.MaxRetries > 8 {
		cfg.MaxRetries = 8
	}
	lane, err := cfg.Layout.lane(cfg.Tenant)
	if err != nil {
		return report, err
	}
	if err := cfg.Lease.validate(cfg.Tenant); err != nil {
		return report, err
	}
	for _, testCase := range cases {
		report.Prefixes++
		left, err := NewMemStoreFromRaw(testCase.Left)
		if err != nil {
			return report, fmt.Errorf("%s: left: %w", testCase.Name, err)
		}
		right, err := NewMemStoreFromRaw(testCase.Right)
		if err != nil {
			return report, fmt.Errorf("%s: right: %w", testCase.Name, err)
		}
		leftTrace, leftResult, leftApplies, err := verifyOne(cfg, left)
		if err != nil {
			return report, fmt.Errorf("%s: left: %w", testCase.Name, err)
		}
		rightTrace, rightResult, rightApplies, err := verifyOne(cfg, right)
		if err != nil {
			return report, fmt.Errorf("%s: right: %w", testCase.Name, err)
		}
		if !reflect.DeepEqual(leftTrace, rightTrace) {
			return report, fmt.Errorf("%s: recovery trace diverged:\nleft:  %#v\nright: %#v", testCase.Name, leftTrace, rightTrace)
		}
		if leftResult.Receipt != nil {
			if !reflect.DeepEqual(leftResult.Receipt, rightResult.Receipt) {
				return report, fmt.Errorf("%s: recovery receipt diverged", testCase.Name)
			}
		}
		if !reflect.DeepEqual(leftResult.Snapshot, rightResult.Snapshot) {
			return report, fmt.Errorf("%s: recovered snapshot diverged", testCase.Name)
		}
		if err := checkExpected(testCase.Name, testCase.Expected, leftResult, leftApplies); err != nil {
			return report, err
		}
		if err := checkExpected(testCase.Name+"-right", testCase.Expected, rightResult, rightApplies); err != nil {
			return report, err
		}
		if cfg.ForbiddenTenant != (TenantID{}) && leftResult.Snapshot != nil {
			for _, block := range leftResult.Snapshot.Blocks {
				if block.Tenant == cfg.ForbiddenTenant {
					return report, fmt.Errorf("%s: cross-tenant block exposed", testCase.Name)
				}
			}
		}
		for _, event := range leftTrace {
			if event.Region != RegionA && event.Region != RegionB {
				return report, fmt.Errorf("%s: invalid region in trace", testCase.Name)
			}
			if event.Slot < lane.Start || event.Slot >= lane.Start+lane.Slots {
				return report, fmt.Errorf("%s: cross-tenant read at region %s slot %d", testCase.Name, event.Region, event.Slot)
			}
			if event.Op != OpRead || event.Length != cfg.FrameSize {
				return report, fmt.Errorf("%s: unexpected recovery operation %+v", testCase.Name, event)
			}
		}
		if maxAttempts := 2 * lane.Slots * (cfg.MaxRetries + 1); len(leftTrace) > maxAttempts {
			return report, fmt.Errorf("%s: trace has %d attempts, bound is %d", testCase.Name, len(leftTrace), maxAttempts)
		}
		report.ReadEvents += len(leftTrace)
		report.ApplyCallbacks += leftApplies
		if len(leftTrace) > report.MaxPrefixReads {
			report.MaxPrefixReads = len(leftTrace)
		}
	}
	return report, nil
}

func verifyOne(cfg VerifyConfig, store *MemStore) ([]Event, RecoveryResult, int, error) {
	var observerTrace []Event
	engine, err := New(store, cfg.Keys, Config{
		Layout:     cfg.Layout,
		FrameSize:  cfg.FrameSize,
		MaxRetries: cfg.MaxRetries,
	}, WithObserver(ObserverFunc(func(event Event) {
		observerTrace = append(observerTrace, event)
	})))
	if err != nil {
		return nil, RecoveryResult{}, 0, err
	}
	applies := 0
	result, err := engine.RecoverAndApply(RecoveryRequest{
		Tenant:        cfg.Tenant,
		Lease:         cfg.Lease,
		MinGeneration: cfg.MinGeneration,
	}, func(got RecoveryResult) error {
		applies++
		if applies != 1 {
			return fmt.Errorf("apply callback invoked %d times", applies)
		}
		if got.Receipt == nil {
			return fmt.Errorf("apply callback invoked without receipt")
		}
		return nil
	})
	if err != nil {
		return observerTrace, result, applies, err
	}
	if !reflect.DeepEqual(observerTrace, result.Trace) {
		return observerTrace, result, applies, fmt.Errorf("observer and result traces differ")
	}
	if result.Snapshot != nil {
		if result.Snapshot.Tenant != cfg.Tenant {
			return observerTrace, result, applies, fmt.Errorf("snapshot tenant mismatch")
		}
		for _, block := range result.Snapshot.Blocks {
			if block.Tenant != cfg.Tenant {
				return observerTrace, result, applies, fmt.Errorf("foreign block tenant %s", block.Tenant)
			}
			if block.ID == (BlockID{}) {
				return observerTrace, result, applies, fmt.Errorf("zero block id")
			}
		}
	}
	if result.Receipt != nil {
		if result.Receipt.Tenant != cfg.Tenant || result.Receipt.Lease != cfg.Lease {
			return observerTrace, result, applies, fmt.Errorf("receipt lease/tenant mismatch")
		}
		if result.Snapshot != nil {
			if result.Snapshot.Lease != cfg.Lease || result.Snapshot.Generation != result.Receipt.Generation ||
				result.Snapshot.Txn != result.Receipt.Txn {
				return observerTrace, result, applies, fmt.Errorf("snapshot receipt mismatch")
			}
		}
	}
	return observerTrace, result, applies, nil
}

func checkExpected(name string, want Expected, got RecoveryResult, applies int) error {
	if want.MinRollback && !got.RollbackDetected {
		return fmt.Errorf("%s: expected rollback detection", name)
	}
	if !want.Set {
		if got.Receipt != nil || got.Snapshot != nil || got.Deleted || applies != 0 {
			return fmt.Errorf("%s: expected no visible outcome, got receipt=%v deleted=%v applies=%d", name, got.Receipt != nil, got.Deleted, applies)
		}
		return nil
	}
	if got.Receipt == nil {
		return fmt.Errorf("%s: expected receipt, got none", name)
	}
	if applies != 1 {
		return fmt.Errorf("%s: apply count %d, want 1", name, applies)
	}
	if got.Receipt.Generation != want.Generation || got.Receipt.Txn != want.Txn || got.Receipt.Deleted != want.Deleted {
		return fmt.Errorf("%s: receipt %+v, want generation=%d txn=%s deleted=%v", name, got.Receipt, want.Generation, want.Txn, want.Deleted)
	}
	if !want.Deleted {
		if got.Snapshot == nil {
			return fmt.Errorf("%s: expected snapshot", name)
		}
		if !reflect.DeepEqual(got.Snapshot.Blocks, want.Blocks) {
			return fmt.Errorf("%s: blocks differ", name)
		}
	} else if got.Snapshot != nil || !got.Deleted {
		return fmt.Errorf("%s: expected tombstone", name)
	}
	return nil
}
