package resolver

// resolve follows the declaration chain starting at (module, name).
// lookup returns all declarations stored at a key; record, when non-nil,
// is invoked with every key consulted (including misses) so callers can
// track dependencies for incremental invalidation.
//
// At each key a local declaration (definition or re-export) shadows any
// import of the same name. The walk stops at the first real definition,
// at a key with no applicable declaration, or when a key is revisited
// (a cycle with no real definition).
func resolve(module, name string, lookup func(Key) []Decl, record func(Key)) (endpoint Key, chain []Step, cyclic, ok bool) {
	visited := make(map[Key]bool)
	current := Key{Module: module, Name: name}
	for {
		if record != nil {
			record(current)
		}
		if visited[current] {
			chain = append(chain, Step{
				Key:    current,
				Detail: "cycle detected: re-export chain has no real definition",
			})
			return Key{}, chain, true, false
		}
		visited[current] = true

		local, imp := selectDecl(lookup(current))
		switch {
		case local != nil && local.Kind == Definition:
			detail := "local definition"
			if imp != nil {
				detail += " (shadows import)"
			}
			chain = append(chain, Step{Key: current, Kind: Definition, Detail: detail})
			return current, chain, false, true
		case local != nil: // Reexport
			detail := "re-export -> " + local.TargetModule + "." + local.TargetName
			if imp != nil {
				detail += " (shadows import)"
			}
			chain = append(chain, Step{Key: current, Kind: Reexport, Detail: detail})
			current = local.Target()
		case imp != nil:
			chain = append(chain, Step{
				Key:    current,
				Kind:   Import,
				Detail: "import -> " + imp.TargetModule + "." + imp.TargetName,
			})
			current = imp.Target()
		default:
			chain = append(chain, Step{Key: current, Detail: "no declaration found"})
			return Key{}, chain, false, false
		}
	}
}

// selectDecl picks the applicable declaration at a key: a local
// declaration (definition or re-export) shadows imports of the same name.
func selectDecl(decls []Decl) (local, imp *Decl) {
	for i := range decls {
		d := &decls[i]
		if d.Kind == Import {
			if imp == nil {
				imp = d
			}
		} else if local == nil {
			local = d
		}
	}
	return local, imp
}

func buildResult(r Ref, endpoint Key, chain []Step, cyclic, ok bool) Result {
	res := Result{Ref: r, Chain: chain, Cyclic: cyclic, Resolved: ok}
	switch {
	case ok:
		res.Endpoint = endpoint
		res.Reason = "resolved"
	case cyclic:
		res.Reason = "cyclic unresolved: re-export chain has no real definition"
	default:
		res.Reason = "unresolved: no declaration found"
	}
	return res
}

// Direct resolves every reference from scratch against a fixed
// declaration set. It serves as the ground-truth model used to
// cross-check incremental updates.
type Direct struct {
	table map[Key][]Decl
}

// NewDirect builds a Direct resolver from parsed declarations.
func NewDirect(decls []Decl) *Direct {
	d := &Direct{table: make(map[Key][]Decl)}
	for _, decl := range decls {
		k := decl.Key()
		d.table[k] = append(d.table[k], decl)
	}
	return d
}

// Resolve resolves one reference against the full declaration set.
func (d *Direct) Resolve(r Ref) Result {
	endpoint, chain, cyclic, ok := resolve(r.Module, r.Name, func(k Key) []Decl {
		return d.table[k]
	}, nil)
	return buildResult(r, endpoint, chain, cyclic, ok)
}
