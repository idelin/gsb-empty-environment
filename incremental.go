package resolver

// Incremental keeps resolution results cached and invalidates only the
// references whose explanation chain consulted a changed declaration
// key. Deletes and edits of declarations therefore do not require a
// full recomputation.
type Incremental struct {
	table map[Key]map[Kind]Decl
	refs  map[int]Ref
	cache map[int]*cacheEntry
	byKey map[Key]map[int]bool // declaration key -> IDs of refs whose result depends on it
}

type cacheEntry struct {
	result Result
	used   []Key // keys consulted while computing result
}

// NewIncremental returns an empty incremental resolver.
func NewIncremental() *Incremental {
	return &Incremental{
		table: make(map[Key]map[Kind]Decl),
		refs:  make(map[int]Ref),
		cache: make(map[int]*cacheEntry),
		byKey: make(map[Key]map[int]bool),
	}
}

// AddRef registers a reference. An existing reference with the same ID
// is replaced and its cached result dropped.
func (in *Incremental) AddRef(r Ref) {
	in.RemoveRef(r.ID)
	in.refs[r.ID] = r
}

// RemoveRef unregisters a reference and its cached result.
func (in *Incremental) RemoveRef(id int) {
	if _, ok := in.refs[id]; !ok {
		return
	}
	delete(in.refs, id)
	in.dropCache(id)
}

// PutDecl inserts or replaces the declaration at (module, name, kind).
// Cached results that consulted the key are invalidated.
func (in *Incremental) PutDecl(d Decl) {
	k := d.Key()
	slot, ok := in.table[k]
	if !ok {
		slot = make(map[Kind]Decl)
		in.table[k] = slot
	}
	if existing, ok := slot[d.Kind]; ok && existing == d {
		return
	}
	slot[d.Kind] = d
	in.invalidate(k)
}

// DeleteDecl removes the declaration at (module, name, kind) if present
// and invalidates cached results that consulted the key.
func (in *Incremental) DeleteDecl(module, name string, kind Kind) {
	k := Key{Module: module, Name: name}
	slot, ok := in.table[k]
	if !ok {
		return
	}
	if _, ok := slot[kind]; !ok {
		return
	}
	delete(slot, kind)
	if len(slot) == 0 {
		delete(in.table, k)
	}
	in.invalidate(k)
}

// Resolve returns the cached result for a reference, recomputing it
// only when a relevant declaration changed since the last call.
func (in *Incremental) Resolve(id int) (Result, bool) {
	r, ok := in.refs[id]
	if !ok {
		return Result{}, false
	}
	if entry, ok := in.cache[id]; ok {
		return entry.result, true
	}
	var used []Key
	endpoint, chain, cyclic, resolved := resolve(r.Module, r.Name, in.lookup, func(k Key) {
		used = append(used, k)
	})
	res := buildResult(r, endpoint, chain, cyclic, resolved)
	in.cache[id] = &cacheEntry{result: res, used: used}
	for _, k := range used {
		ids := in.byKey[k]
		if ids == nil {
			ids = make(map[int]bool)
			in.byKey[k] = ids
		}
		ids[id] = true
	}
	return res, true
}

func (in *Incremental) lookup(k Key) []Decl {
	slot := in.table[k]
	if len(slot) == 0 {
		return nil
	}
	out := make([]Decl, 0, len(slot))
	for _, kind := range []Kind{Definition, Reexport, Import} {
		if d, ok := slot[kind]; ok {
			out = append(out, d)
		}
	}
	return out
}

func (in *Incremental) invalidate(k Key) {
	var todo []int
	for id := range in.byKey[k] {
		todo = append(todo, id)
	}
	for _, id := range todo {
		in.dropCache(id)
	}
}

func (in *Incremental) dropCache(id int) {
	entry, ok := in.cache[id]
	if !ok {
		return
	}
	for _, k := range entry.used {
		if ids, ok := in.byKey[k]; ok {
			delete(ids, id)
			if len(ids) == 0 {
				delete(in.byKey, k)
			}
		}
	}
	delete(in.cache, id)
}
