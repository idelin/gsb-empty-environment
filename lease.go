package fetchquota

import "sync"

// Lease represents a request that has been charged against one host bucket and
// one account bucket but may not have been sent yet.
//
// Every Lease must be finalized exactly once:
//
//   - Call MarkSent once the request has actually been transmitted; the
//     charged tokens stay consumed.
//   - Call Release (or cancel) when the approved request is dropped before
//     sending; both the host and the account tokens are refunded.
type Lease struct {
	s             *Scheduler
	hostBucket    *bucket
	accountBucket *bucket
	key           queueKey

	finalizeOnce sync.Once
}

// MarkSent records that the request was sent, keeping both levels charged.
// Subsequent calls are no-ops.
func (l *Lease) MarkSent() {
	l.finalizeOnce.Do(func() {})
}

// Release refunds one token to each of the host and account buckets and wakes
// queued requests that may now be grantable. It is idempotent: calling it
// after MarkSent or a previous Release does nothing.
func (l *Lease) Release() {
	l.finalizeOnce.Do(func() {
		l.s.mu.Lock()
		l.hostBucket.refund()
		l.accountBucket.refund()
		l.s.notify()
		l.s.mu.Unlock()
	})
}

// Cancel is an alias for Release for call sites that think in terms of
// cancelling an unsent, approved request.
func (l *Lease) Cancel() {
	l.Release()
}

// Account returns the account this lease was granted to.
func (l *Lease) Account() string { return l.key.account }

// Host returns the host this lease was granted for.
func (l *Lease) Host() string { return l.key.host }
