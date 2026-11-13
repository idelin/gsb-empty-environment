package netsec

import (
	"errors"
	"testing"
)

func TestSandboxPolicyClosesBothBreakaways(t *testing.T) {
	loose := Limits{
		MaxActiveProcesses: 4,
		MaxJobMemory:       1 << 20,
		BreakawayOK:        true,
		SilentBreakawayOK:  true,
		KillOnJobClose:     false,
		UI:                 UILimitHandles,
	}
	hardened := SandboxLimits(loose)

	if hardened.BreakawayOK || hardened.SilentBreakawayOK {
		t.Fatalf("sandbox policy must disable both breakaways, got %+v", hardened)
	}
	if !hardened.KillOnJobClose {
		t.Fatal("sandbox policy must enable KILL_ON_JOB_CLOSE")
	}
	if hardened.MaxActiveProcesses != 4 || hardened.MaxJobMemory != 1<<20 || hardened.UI != UILimitHandles {
		t.Fatalf("caller caps and UI bits must be preserved, got %+v", hardened)
	}
	if err := ValidateSandboxPolicy(hardened); err != nil {
		t.Fatalf("hardened limits should validate: %v", err)
	}

	loose.KillOnJobClose = true
	if err := ValidateSandboxPolicy(loose); !errors.Is(err, ErrBreakawayDenied) {
		t.Fatalf("expected ErrBreakawayDenied, got %v", err)
	}
	loose.BreakawayOK = false
	if err := ValidateSandboxPolicy(loose); !errors.Is(err, ErrSilentBreakawayDenied) {
		t.Fatalf("expected ErrSilentBreakawayDenied, got %v", err)
	}
}

func TestAdmitCreateBreakawayFlags(t *testing.T) {
	t.Run("sandbox_job_rejects_explicit_and_silent_breakaway", func(t *testing.T) {
		job := NewJob(SandboxLimits(Limits{MaxActiveProcesses: 2}))

		if _, err := job.AdmitCreate(CreateBreakawayFromJob); !errors.Is(err, ErrBreakawayDenied) {
			t.Fatalf("explicit breakaway must be denied, got %v", err)
		}
		if _, err := job.AdmitCreate(CreateSilentBreakawayFromJob); !errors.Is(err, ErrSilentBreakawayDenied) {
			t.Fatalf("silent breakaway must be denied, got %v", err)
		}
		// Rejected breakaways reserve nothing.
		if got := job.ActiveProcesses(); got != 0 {
			t.Fatalf("denied breakaway must not consume a slot, active=%d", got)
		}
	})

	t.Run("breakaway_allowed_only_when_limit_permits", func(t *testing.T) {
		job := NewJob(Limits{
			MaxActiveProcesses: 1,
			BreakawayOK:        true,
			KillOnJobClose:     true,
		})

		breakaway, err := job.AdmitCreate(CreateBreakawayFromJob)
		if err != nil {
			t.Fatalf("permitted explicit breakaway failed: %v", err)
		}
		if !breakaway {
			t.Fatal("breakaway result must be true")
		}
		// Escaped processes are not job members and never touch the cap.
		if got := job.ActiveProcesses(); got != 0 {
			t.Fatalf("breakaway child must not count against the cap, active=%d", got)
		}

		// Silent breakaway remains closed on this job.
		if _, err := job.AdmitCreate(CreateSilentBreakawayFromJob); !errors.Is(err, ErrSilentBreakawayDenied) {
			t.Fatalf("silent breakaway must stay denied, got %v", err)
		}

		// The single member slot is still available for a normal create.
		if _, err := job.AdmitCreate(0); err != nil {
			t.Fatalf("normal create should succeed: %v", err)
		}
	})

	t.Run("silent_breakaway_permitted_when_enabled", func(t *testing.T) {
		job := NewJob(Limits{
			MaxActiveProcesses: 1,
			SilentBreakawayOK:  true,
			KillOnJobClose:     true,
		})
		breakaway, err := job.AdmitCreate(CreateSilentBreakawayFromJob)
		if err != nil || !breakaway {
			t.Fatalf("silent breakaway should pass, breakaway=%v err=%v", breakaway, err)
		}
		if job.ActiveProcesses() != 0 {
			t.Fatal("silent breakaway must not reserve a member slot")
		}
	})
}

func TestAdmitCreateRejectsWhenOverLimit(t *testing.T) {
	job := NewJob(SandboxLimits(Limits{MaxActiveProcesses: 2}))

	for i := 0; i < 2; i++ {
		if _, err := job.AdmitCreate(0); err != nil {
			t.Fatalf("member %d should be admitted: %v", i, err)
		}
	}
	if got := job.ActiveProcesses(); got != 2 {
		t.Fatalf("expected 2 active members, got %d", got)
	}

	// The third create is refused rather than killing an existing member.
	_, err := job.AdmitCreate(0)
	if !errors.Is(err, ErrProcessLimitExceeded) {
		t.Fatalf("expected ErrProcessLimitExceeded, got %v", err)
	}
	if got := job.ActiveProcesses(); got != 2 {
		t.Fatalf("existing members must survive rejection, active=%d", got)
	}

	// A rollback (failed spawn) frees the reserved slot; an exit frees a slot.
	job.Release()
	if got := job.ActiveProcesses(); got != 1 {
		t.Fatalf("Release should free one slot, active=%d", got)
	}
	if _, err := job.AdmitCreate(CreateSuspended); err != nil {
		t.Fatalf("slot freed by failed spawn should be reusable: %v", err)
	}
	job.Commit()
	if _, err := job.AdmitCreate(0); err != nil {
		t.Fatalf("slot freed by process exit should be reusable: %v", err)
	}
}

func TestNestedJobCannotRelaxParent(t *testing.T) {
	parent := SandboxLimits(Limits{
		MaxActiveProcesses: 8,
		MaxJobMemory:       64 << 20,
		UI:                 UILimitHandles | UILimitReadClipboard,
	})

	t.Run("child_cannot_clear_kill_on_close", func(t *testing.T) {
		child := Limits{
			MaxActiveProcesses: 4,
			KillOnJobClose:     false, // tries to drop the parent guarantee
		}
		_, err := NestedLimits(parent, child)
		if !errors.Is(err, ErrKillOnCloseDowngrade) {
			t.Fatalf("expected ErrKillOnCloseDowngrade, got %v", err)
		}
	})

	t.Run("child_cannot_reopen_breakaway_path", func(t *testing.T) {
		child := Limits{
			KillOnJobClose:    true,
			BreakawayOK:       true, // attacker tries to enable escape inside nesting
			SilentBreakawayOK: true,
		}
		effective, err := NestedLimits(parent, child)
		if err != nil {
			t.Fatalf("valid nesting rejected: %v", err)
		}
		if effective.BreakawayOK || effective.SilentBreakawayOK {
			t.Fatalf("breakaway capabilities must AND down to closed, got %+v", effective)
		}
	})

	t.Run("caps_take_minimum_and_restrictions_union", func(t *testing.T) {
		child := Limits{
			MaxActiveProcesses: 3, // tighter than parent's 8
			MaxJobMemory:       0, // unlimited on its own: parent cap must survive
			KillOnJobClose:     true,
			UI:                 UILimitWriteClipboard,
		}
		effective, err := NestedLimits(parent, child)
		if err != nil {
			t.Fatalf("legal nesting rejected: %v", err)
		}
		if effective.MaxActiveProcesses != 3 {
			t.Fatalf("process cap must be the minimum, got %d", effective.MaxActiveProcesses)
		}
		if effective.MaxJobMemory != 64<<20 {
			t.Fatalf("parent memory cap must survive child's unlimited value, got %d", effective.MaxJobMemory)
		}
		if !effective.KillOnJobClose {
			t.Fatal("KILL_ON_JOB_CLOSE must remain effective")
		}
		wantUI := UILimitHandles | UILimitReadClipboard | UILimitWriteClipboard
		if effective.UI != wantUI {
			t.Fatalf("UI restrictions must union, got %#x want %#x", effective.UI, wantUI)
		}
	})

	t.Run("parent_tighter_memory_cap_wins", func(t *testing.T) {
		child := Limits{
			MaxActiveProcesses: 16,
			MaxJobMemory:       32 << 20, // tighter than parent's 64 MiB
			KillOnJobClose:     true,
		}
		effective, err := NestedLimits(parent, child)
		if err != nil {
			t.Fatalf("legal nesting rejected: %v", err)
		}
		if effective.MaxJobMemory != 32<<20 {
			t.Fatalf("tighter child memory cap must win, got %d", effective.MaxJobMemory)
		}
		if effective.MaxActiveProcesses != 8 {
			t.Fatalf("tighter parent process cap must win, got %d", effective.MaxActiveProcesses)
		}
	})
}

func TestNestedEffectiveLimitIsEnforcedAtCreate(t *testing.T) {
	parent := SandboxLimits(Limits{MaxActiveProcesses: 5})
	child, err := NestedLimits(parent, Limits{
		MaxActiveProcesses: 2,
		KillOnJobClose:     true,
	})
	if err != nil {
		t.Fatalf("legal nesting rejected: %v", err)
	}

	job := NewJob(child)
	if _, err := job.AdmitCreate(0); err != nil {
		t.Fatalf("first member denied: %v", err)
	}
	if _, err := job.AdmitCreate(0); err != nil {
		t.Fatalf("second member denied: %v", err)
	}
	if _, err := job.AdmitCreate(0); !errors.Is(err, ErrProcessLimitExceeded) {
		t.Fatalf("nested cap of 2 must reject the third create, got %v", err)
	}

	// Escape stays closed even though a nested child never asked for breakaway
	// explicitly: the merged policy keeps it shut.
	if _, err := job.AdmitCreate(CreateBreakawayFromJob); !errors.Is(err, ErrBreakawayDenied) {
		t.Fatalf("breakaway must remain denied under merged limits, got %v", err)
	}
}
