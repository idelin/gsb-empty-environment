package logtrunc

// Deleter deletes one segment. It must be idempotent: deleting an
// already-deleted segment must succeed, because a request is retried
// as a whole after a partial failure.
type Deleter func(segmentID string) error

// Executor runs truncation requests against a Deleter. Requests are
// persisted in the Store before any deletion is attempted, so a crash
// between planning and deletion cannot lose work: after recovery,
// ProcessPending retries every unfinished request.
type Executor struct {
	store  *Store
	delete Deleter
}

func NewExecutor(store *Store, d Deleter) *Executor {
	return &Executor{store: store, delete: d}
}

// Submit persists the request first (so recovery cannot forget it) and
// then makes an immediate best-effort attempt. Failures are not
// reported here; the request stays pending for ProcessPending.
func (e *Executor) Submit(r Request) error {
	if err := e.store.Submit(r); err != nil {
		return err
	}
	e.ProcessPending()
	return nil
}

// ProcessPending retries every unfinished request once. A request whose
// deletions all succeed is marked done; any failure leaves it pending
// so a later call (or a post-recovery call) retries it.
func (e *Executor) ProcessPending() {
	for _, req := range e.store.Pending() {
		if e.run(req) {
			e.store.Complete(req.ID)
		}
	}
}

func (e *Executor) run(req Request) bool {
	for _, id := range req.SegmentIDs {
		if err := e.delete(id); err != nil {
			return false
		}
	}
	return true
}
