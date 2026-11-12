package netsec

import (
	"sort"
	"time"
)

// Options bounds the coordinator's freeze and retry behavior.
type Options struct {
	// MaxAttempts is the maximum number of freeze-and-classify rounds.
	MaxAttempts int
	// FreezeWait bounds how long one round waits for every requested
	// thread to reach the frozen state (the "late freeze" window).
	FreezeWait time.Duration
	// RetryDelay separates two attempts; frozen threads are resumed
	// before sleeping so they can leave the patched region naturally.
	RetryDelay time.Duration
	// Sleep is injectable for deterministic tests; nil uses time.Sleep.
	Sleep func(time.Duration)
	// Now is injectable for deterministic tests; nil uses time.Now.
	Now func() time.Time
	// OnRound, when set, is invoked after every freeze-and-classify round
	// with the classified thread states. Tests use it to observe the
	// frozen barrier before any thread is resumed.
	OnRound func(attempt int, threads []frozenThread)
}

func (o Options) withDefaults() Options {
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = 16
	}
	if o.FreezeWait <= 0 {
		o.FreezeWait = 100 * time.Millisecond
	}
	if o.RetryDelay <= 0 {
		o.RetryDelay = time.Millisecond
	}
	if o.Sleep == nil {
		o.Sleep = time.Sleep
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return o
}

// Result summarizes one completed patch transaction.
type Result struct {
	Attempts        int
	FrozenThreads   int
	MigratedThreads int
	OutsideThreads  int
	ExitedThreads   int
}

type threadClass int

const (
	classExited threadClass = iota
	classOutside
	classMappable
	classUnmappable
)

type frozenThread struct {
	ref   ThreadRef
	pc    Addr
	class threadClass
	newPC Addr
}

// ApplyPatch runs one coordinated hot replacement against host.
//
// Protocol per attempt:
//  1. freeze every live thread, waiting for stragglers inside FreezeWait;
//  2. read each frozen thread's program counter and classify it against the
//     plan's boundary map (mappable / outside the region / exited);
//  3. on any unmappable thread, resume everyone and retry so the thread can
//     leave the old sequence naturally;
//  4. otherwise write the new code, flush the instruction cache, then
//     redirect migratable threads;
//  5. on flush failure roll the page and PCs back to the old sequence;
//  6. resume all frozen threads together, regardless of outcome.
func ApplyPatch(host Host, plan Plan, opts Options) (Result, error) {
	opts = opts.withDefaults()
	var result Result

	if err := plan.validate(); err != nil {
		return result, err
	}

	for attempt := 1; attempt <= opts.MaxAttempts; attempt++ {
		result.Attempts = attempt

		frozen, err := freezeAll(host, opts)
		if err != nil {
			return result, err
		}
		result.FrozenThreads = len(frozen)

		threads, blocked := classify(host, plan, frozen)
		if opts.OnRound != nil {
			opts.OnRound(attempt, threads)
		}
		if blocked {
			resumeAll(host, threads)
			if attempt == opts.MaxAttempts {
				return result, ErrRetriesExhausted
			}
			opts.Sleep(opts.RetryDelay)
			continue
		}

		retriable, err := commit(host, plan, threads)
		if err != nil {
			rollback(host, plan, threads)
			resumeAll(host, threads)
			if retriable && attempt < opts.MaxAttempts {
				opts.Sleep(opts.RetryDelay)
				continue
			}
			if retriable {
				return result, ErrRetriesExhausted
			}
			return result, err
		}

		counts(threads, &result)
		resumeAll(host, threads)
		return result, nil
	}

	return result, ErrRetriesExhausted
}

// freezeAll snapshots the live-thread set and waits until every referenced
// thread is frozen. Threads discovered while waiting (including IDs reused
// after an exit) are pulled into the same freeze round; references whose
// generation no longer exists are treated as stale and skipped.
func freezeAll(host Host, opts Options) ([]ThreadRef, error) {
	seen := map[ThreadRef]bool{}
	var requested []ThreadRef

	refresh := func() bool {
		before := len(requested)
		for _, ref := range host.ListThreads() {
			if !seen[ref] {
				seen[ref] = true
				requested = append(requested, ref)
			}
		}
		sortRefs(requested)
		return len(requested) > before
	}

	_ = refresh()
	deadline := opts.Now().Add(opts.FreezeWait)

	for {
		allFrozen := true
		for _, ref := range requested {
			status, err := host.Status(ref)
			if err == ErrThreadExited || err == ErrUnknownThread {
				continue
			}
			if err != nil {
				return nil, err
			}
			switch status {
			case StatusFrozen:
				continue
			case StatusExited:
				continue
			case StatusRunning, StatusUnknown:
				if err := host.Freeze(ref); err != nil && err != ErrThreadExited && err != ErrUnknownThread {
					return nil, err
				}
				allFrozen = false
			}
		}

		if refresh() {
			allFrozen = false
		}
		if allFrozen {
			// Re-check after absorbing threads that appeared during the round.
			if stableFrozen(host, requested) {
				return requested, nil
			}
		}
		if !opts.Now().Before(deadline) {
			return nil, ErrFreezeTimeout
		}
		opts.Sleep(time.Millisecond)
	}
}

func stableFrozen(host Host, refs []ThreadRef) bool {
	for _, ref := range refs {
		status, err := host.Status(ref)
		if err == ErrThreadExited || err == ErrUnknownThread || status == StatusExited {
			continue
		}
		if err != nil || status != StatusFrozen {
			return false
		}
	}
	return true
}

// classify reads every frozen thread's position and decides whether the
// transaction can proceed. A thread inside the region whose program counter
// is not a declared boundary blocks the attempt.
func classify(host Host, plan Plan, refs []ThreadRef) ([]frozenThread, bool) {
	threads := make([]frozenThread, 0, len(refs))
	blocked := false

	for _, ref := range refs {
		status, err := host.Status(ref)
		if err != nil || status != StatusFrozen {
			threads = append(threads, frozenThread{ref: ref, class: classExited})
			continue
		}

		pc, err := host.ReadPC(ref)
		if err != nil {
			threads = append(threads, frozenThread{ref: ref, class: classExited})
			continue
		}

		entry := frozenThread{ref: ref, pc: pc}
		if !plan.Region.Contains(pc) {
			entry.class = classOutside
		} else if newPC, ok := plan.OldToNewPC[pc]; ok {
			entry.class = classMappable
			entry.newPC = newPC
		} else {
			entry.class = classUnmappable
			blocked = true
		}
		threads = append(threads, entry)
	}
	return threads, blocked
}

// commit rewrites the page, flushes caches, and only then moves the
// migratable threads onto the new sequence. The returned bool reports
// whether a failure is retryable (a thread vanished or thawed between the
// barrier and the redirect); flush failures are not retryable.
func commit(host Host, plan Plan, threads []frozenThread) (bool, error) {
	if err := host.WriteMemory(plan.Region.Start, plan.NewBytes); err != nil {
		return false, err
	}
	if err := host.FlushICache(plan.Region); err != nil {
		return false, err
	}
	for _, thread := range threads {
		if thread.class != classMappable {
			continue
		}
		// Re-validate under the live state: a thread can exit (or have
		// its ID reused by another generation) between classification and
		// commit. Such a reference is no longer safe to redirect, so the
		// whole attempt rolls back and runs another freeze round.
		status, err := host.Status(thread.ref)
		if err == ErrThreadExited || err == ErrUnknownThread || status == StatusExited {
			return true, ErrThreadExited
		}
		if err != nil || status != StatusFrozen {
			return true, ErrThreadNotFrozen
		}
		if err := host.WritePC(thread.ref, thread.newPC); err != nil {
			return false, err
		}
	}
	return false, nil
}

// rollback restores the previous page contents, flushes the stale new bytes
// out of the caches, and puts migrated threads back on their old PCs.
func rollback(host Host, plan Plan, threads []frozenThread) {
	_ = host.WriteMemory(plan.Region.Start, plan.OldBytes)
	_ = host.FlushICache(plan.Region)
	for _, thread := range threads {
		if thread.class == classMappable {
			_ = host.WritePC(thread.ref, thread.pc)
		}
	}
}

// resumeAll releases every frozen thread in one final barrier step. Exited
// threads and stale references are ignored.
func resumeAll(host Host, threads []frozenThread) {
	for _, thread := range threads {
		if err := host.Resume(thread.ref); err != nil && err != ErrThreadExited && err != ErrUnknownThread {
			// Resume is best-effort at the barrier: a failure here cannot
			// undo an already committed patch, so it is surfaced nowhere.
			_ = err
		}
	}
}

func counts(threads []frozenThread, result *Result) {
	for _, thread := range threads {
		switch thread.class {
		case classMappable:
			result.MigratedThreads++
		case classOutside:
			result.OutsideThreads++
		case classExited:
			result.ExitedThreads++
		}
	}
}

func sortRefs(refs []ThreadRef) {
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].ID != refs[j].ID {
			return refs[i].ID < refs[j].ID
		}
		return refs[i].Gen < refs[j].Gen
	})
}
