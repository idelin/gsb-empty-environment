package netsec

// RegisterTenant enrolls a tenant together with the namespace prefixes it
// is allowed to read and write.
func (a *Authorizer) RegisterTenant(name string, prefixes []string) error {
	if name == "" {
		return ErrEmptyTenant
	}
	if err := validateNamespace(prefixes); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return ErrAuthorizerClosed
	}
	if _, ok := a.tenants[name]; ok {
		return ErrTenantExists
	}
	a.tenants[name] = &tenant{
		name:     name,
		prefixes: append([]string(nil), prefixes...),
		bucket:   newTokenBucket(a.limits.EventsPerSecond, a.limits.EventBurst, a.clock),
		watches:  make(map[uint64]*Watch),
	}
	return nil
}

// SetLimits updates the per-tenant limits. Rate-limit buckets are reset.
func (a *Authorizer) SetLimits(l Limits) {
	l = normalizeLimits(l)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.limits = l
	for _, t := range a.tenants {
		t.bucket = newTokenBucket(l.EventsPerSecond, l.EventBurst, a.clock)
	}
}

// SetTenantPrefixes replaces a tenant's namespace prefixes. Watches whose
// prefix no longer fits inside the namespace are terminated with
// *ErrPermissionRevoked; surviving watches only receive later events that
// match the new prefixes.
func (a *Authorizer) SetTenantPrefixes(tenantName string, prefixes []string) error {
	if err := validateNamespace(prefixes); err != nil {
		return err
	}
	next := append([]string(nil), prefixes...)

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return ErrAuthorizerClosed
	}
	t, ok := a.tenants[tenantName]
	if !ok {
		return ErrUnknownTenant
	}
	t.prefixes = next
	for id, w := range t.watches {
		if err := validateWatchPrefix(w.prefix, next); err != nil {
			delete(t.watches, id)
			w.terminate(&ErrPermissionRevoked{Tenant: tenantName, Prefix: w.prefix})
		}
	}
	return nil
}

// TenantPrefixes returns a copy of the tenant's current namespace
// prefixes.
func (a *Authorizer) TenantPrefixes(tenantName string) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t, ok := a.tenants[tenantName]
	if !ok {
		return nil, ErrUnknownTenant
	}
	return append([]string(nil), t.prefixes...), nil
}

// WatchCount returns the number of open watches held by a tenant.
func (a *Authorizer) WatchCount(tenantName string) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t, ok := a.tenants[tenantName]
	if !ok {
		return 0, ErrUnknownTenant
	}
	return len(t.watches), nil
}

// DroppedEvents returns how many events for a tenant were not delivered
// because of rate limiting or a full watch channel.
func (a *Authorizer) DroppedEvents(tenantName string) (int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t, ok := a.tenants[tenantName]
	if !ok {
		return 0, ErrUnknownTenant
	}
	return t.dropped, nil
}

// LatestVersion returns the highest event version assigned so far.
func (a *Authorizer) LatestVersion() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.log.latestVersion()
}

// Close shuts the authorizer down and terminates every watch with
// ErrAuthorizerClosed. It is idempotent.
func (a *Authorizer) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil
	}
	a.closed = true
	for _, t := range a.tenants {
		for id, w := range t.watches {
			delete(t.watches, id)
			w.terminate(ErrAuthorizerClosed)
		}
	}
	return nil
}
