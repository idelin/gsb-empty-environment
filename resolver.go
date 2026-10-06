package resolve

// Resolver resolves references incrementally: results are cached and
// only entries whose dependency chain touched a mutated key are
// recomputed. It must agree with Direct on every reference.
type Resolver struct {
	store
	cache map[Key]Result
	deps  map[Key]map[Key]struct{} // start key -> keys its result depends on
	rdeps map[Key]map[Key]struct{} // key -> start keys depending on it
}

// NewResolver returns an empty incremental resolver.
func NewResolver() *Resolver {
	return &Resolver{
		store: newStore(),
		cache: make(map[Key]Result),
		deps:  make(map[Key]map[Key]struct{}),
		rdeps: make(map[Key]map[Key]struct{}),
	}
}

// Resolve resolves the reference (module, name), using and populating
// the cache.
func (r *Resolver) Resolve(start Key) Result {
	if res, ok := r.cache[start]; ok {
		return res
	}
	visited := make(map[Key]struct{})
	res := walk(r.locals, r.imports, start, func(k Key) { visited[k] = struct{}{} })
	r.cache[start] = res
	r.deps[start] = visited
	for k := range visited {
		set := r.rdeps[k]
		if set == nil {
			set = make(map[Key]struct{})
			r.rdeps[k] = set
		}
		set[start] = struct{}{}
	}
	return res
}

// SetLocal adds or replaces a local declaration and invalidates
// everything whose resolution depends on that key.
func (r *Resolver) SetLocal(key Key, d Decl) {
	r.store.SetLocal(key, d)
	r.invalidate(key)
}

// DeleteLocal removes a local declaration and invalidates dependents.
func (r *Resolver) DeleteLocal(key Key) {
	r.store.DeleteLocal(key)
	r.invalidate(key)
}

// SetImport adds or replaces an import and invalidates dependents.
func (r *Resolver) SetImport(from, to Key) {
	r.store.SetImport(from, to)
	r.invalidate(from)
}

// DeleteImport removes an import and invalidates dependents.
func (r *Resolver) DeleteImport(from Key) {
	r.store.DeleteImport(from)
	r.invalidate(from)
}

// invalidate drops every cached result whose chain visited key. A
// cached result's dependency set contains exactly the keys its walk
// visited, so entries not depending on key are provably unaffected.
func (r *Resolver) invalidate(key Key) {
	for s := range r.rdeps[key] {
		r.evict(s)
	}
	delete(r.rdeps, key)
}

func (r *Resolver) evict(start Key) {
	if _, ok := r.cache[start]; !ok {
		return
	}
	delete(r.cache, start)
	for d := range r.deps[start] {
		delete(r.rdeps[d], start)
		if len(r.rdeps[d]) == 0 {
			delete(r.rdeps, d)
		}
	}
	delete(r.deps, start)
}
