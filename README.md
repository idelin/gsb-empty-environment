# fairlock

A fair, cancellable read-write mutex for Go with atomic write-to-read
downgrade.

```go
var mu fairlock.RWMutex

err := mu.Lock(ctx)   // write lock, cancellable
err  = mu.RLock(ctx)  // read lock, cancellable
mu.Downgrade()        // write -> read, atomic, no unlocked gap
mu.Unlock()           // release write hold
mu.RUnlock()          // release read hold
```

## Fairness

Waiters are served in FIFO order. Once a writer is queued, readers
that arrive later queue behind it (`acquire` takes the fast path only
when the queue is empty), so a queued writer can never be starved by a
stream of later readers. A queued writer waits at most for the
currently active readers plus the readers queued ahead of it.

## Tie-breaking rule: grant vs. cancel

The handoff of the lock to a waiter is *committed* atomically under
the internal mutex (`grantLocked` sets `w.granted` and closes `w.ch`).
Cancellation is also processed under the same mutex. Exactly one wins:

- **Grant committed first → grant wins.** `Lock`/`RLock` return `nil`
  even if the context is already cancelled; the caller owns the lock
  and must release it.
- **Cancellation processed first → cancel wins.** The waiter is
  removed from the queue, the grant never happens, and
  `Lock`/`RLock` return `ctx.Err()`.

Because both decisions are serialized by one mutex, there is no
interleaving in which both happen (lock leaked to a cancelled waiter)
or neither happens (lost wakeup). Note `select` between `w.ch` and
`ctx.Done()` may choose randomly when both are ready; the tie rule is
therefore enforced *after* the select, under the mutex, making the
outcome deterministic in effect even when the select is not.

## Downgrade has no unlocked gap

`Downgrade` clears the write hold, records the read hold, and grants
queued readers — all inside one critical section. No writer can slip
in between, and queued readers are woken in the same atomic step.

## Proof by deterministic scheduling

`model_test.go` re-expresses the algorithm as a pure state machine
whose transitions mirror `rwmutex.go` step for step (fast path,
enqueue, grant-notice, cancel, release, downgrade, and the grant
closure). An explorer enumerates **every interleaving** of these
atomic steps for 8 goroutine scenarios (up to 4 goroutines, with both
outcomes of the grant-vs-cancel race branching at every await point)
and asserts after every step:

1. never two writers;
2. never a writer concurrent with readers;
3. no lost wakeup: a grantable front waiter is always already granted;
4. FIFO fairness: no reader is granted over an earlier-queued writer;
5. queue/grant-set consistency (none queued twice, none both queued
   and granted).

`rwmutex_test.go` validates the real implementation: deterministic
ordering tests driven by internal-state observation (no sleeps), both
sides of the tie rule, downgrade atomicity, and a randomized stress
test with an exclusion checker, clean under `-race`.

## Run

```sh
go test -race ./...
```
