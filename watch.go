package netsec

import "context"

// Watch opens an authorized subscription for tenant on prefix.
//
// The prefix must be non-empty and contained in one of the tenant's
// namespace prefixes; otherwise ErrEmptyPrefix or
// ErrPrefixOutsideNamespace is returned and no watch is created.
//
// startVersion controls historical replay: a value of zero or less
// delivers only live events; a positive value replays retained events
// with version >= startVersion that the tenant is allowed to read. A
// start older than the retained history is clamped to the oldest
// available version, while a start newer than the latest version yields
// ErrStartInFuture. Cancellation of ctx terminates the watch.
func (a *Authorizer) Watch(ctx context.Context, tenantName, prefix string, startVersion int64) (*Watch, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil, ErrAuthorizerClosed
	}
	t, ok := a.tenants[tenantName]
	if !ok {
		return nil, ErrUnknownTenant
	}
	if err := validateWatchPrefix(prefix, t.prefixes); err != nil {
		return nil, err
	}
	if len(t.watches) >= a.limits.MaxWatches {
		return nil, ErrWatchLimitExceeded
	}
	latest := a.log.latestVersion()
	if startVersion > latest {
		return nil, ErrStartInFuture
	}

	var replay []Event
	if startVersion > 0 {
		start := startVersion
		if oldest := a.log.minVersion(); oldest > 0 && start < oldest {
			start = oldest
		}
		for _, ev := range a.log.since(start) {
			if ev.Tenant != tenantName {
				continue
			}
			if !hasPrefix(ev.Key, prefix) || !anyAllowedPrefix(ev.Key, t.prefixes) {
				continue
			}
			replay = append(replay, ev)
		}
	}

	if ctx != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}

	a.nextID++
	w := &Watch{
		auth:   a,
		id:     a.nextID,
		tenant: tenantName,
		prefix: prefix,
		ch:     make(chan Event, a.chanCap+len(replay)),
	}
	for _, ev := range replay {
		w.ch <- ev
	}
	t.watches[w.id] = w
	if ctx != nil {
		context.AfterFunc(ctx, func() {
			a.removeWatch(w, ctx.Err())
		})
	}
	return w, nil
}

// Publish records a key mutation and fans it out to the tenant's matching
// watches. It returns the assigned event version.
//
// Events whose key falls outside the tenant's namespace are rejected with
// ErrPrefixOutsideNamespace and never enter the history. Delivery to a
// watch consumes the tenant's rate budget; an exhausted budget or a full
// watch channel drops the event (counted via DroppedEvents), and
// ErrRateLimited is returned alongside the assigned version when at least
// one matching watch was rate limited.
func (a *Authorizer) Publish(tenantName string, op Op, key string, value []byte) (int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return 0, ErrAuthorizerClosed
	}
	t, ok := a.tenants[tenantName]
	if !ok {
		return 0, ErrUnknownTenant
	}
	if key == "" || containsPathTraversal(key) || !anyAllowedPrefix(key, t.prefixes) {
		return 0, ErrPrefixOutsideNamespace
	}

	ev := Event{
		Tenant: tenantName,
		Op:     op,
		Key:    key,
		Value:  append([]byte(nil), value...),
	}
	version := a.log.append(ev)

	limited := false
	for _, w := range t.watches {
		if !hasPrefix(ev.Key, w.prefix) {
			continue
		}
		if !t.bucket.allow() {
			t.dropped++
			limited = true
			continue
		}
		select {
		case w.ch <- ev:
		default:
			t.dropped++
		}
	}
	if limited {
		return version, ErrRateLimited
	}
	return version, nil
}
