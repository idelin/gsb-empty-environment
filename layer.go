package netsec

// Layer holds the loaded named modules of a plugin container together with the
// startup --add-opens patches. It answers the same accessibility questions the
// plugin loader asks before letting a plugin reflectively touch another
// module's internals.
//
// Callers are identified by module name; the empty name denotes the unnamed
// module. All unnamed modules behave identically, so a single key suffices.
type Layer struct {
	modules map[string]ModuleDescriptor

	// extraOpens[source module][package] is the set of caller names a patch
	// force-opens the package to. AllUnnamed is stored verbatim and later
	// matched against unnamed callers.
	extraOpens map[string]map[string]map[string]bool

	// extraReads records readability implied by named --add-opens targets,
	// mirroring the JVM which adds a read edge when opening a package to a
	// named module.
	extraReads map[string]map[string]bool
}

// NewLayer builds a Layer from module descriptors and parsed --add-opens
// patches. Descriptors with an empty name describe unnamed modules and are not
// registered as named modules.
func NewLayer(descriptors []ModuleDescriptor, patches []AddOpensPatch) *Layer {
	layer := &Layer{
		modules:    make(map[string]ModuleDescriptor),
		extraOpens: make(map[string]map[string]map[string]bool),
		extraReads: make(map[string]map[string]bool),
	}

	for _, descriptor := range descriptors {
		if descriptor.Name == "" {
			continue
		}
		layer.modules[descriptor.Name] = descriptor
	}

	for _, patch := range patches {
		packages, ok := layer.extraOpens[patch.Module]
		if !ok {
			packages = make(map[string]map[string]bool)
			layer.extraOpens[patch.Module] = packages
		}
		callers, ok := packages[patch.Package]
		if !ok {
			callers = make(map[string]bool)
			packages[patch.Package] = callers
		}
		callers[patch.Target] = true

		if patch.Target != AllUnnamed {
			readers, ok := layer.extraReads[patch.Target]
			if !ok {
				readers = make(map[string]bool)
				layer.extraReads[patch.Target] = readers
			}
			readers[patch.Module] = true
		}
	}

	return layer
}

// CanRead reports whether module caller reads module target, i.e. whether a
// reflective access from caller into target is allowed at the readability
// stage. Readability follows requires edges and is propagated transitively
// through `requires transitive`.
func (layer *Layer) CanRead(caller, target string) bool {
	if caller == target {
		return true
	}

	// Every unnamed module reads every module.
	if caller == "" {
		return true
	}

	// Automatic modules read every other module, named or unnamed.
	if descriptor, ok := layer.modules[caller]; ok && descriptor.Automatic {
		return true
	}

	// A named --add-opens target also gains an ordinary read edge.
	if readers, ok := layer.extraReads[caller]; ok && readers[target] {
		return true
	}

	// Fixed-point expansion. The caller's own requires edges (plain or
	// transitive) establish direct readability; only transitive edges are
	// followed when leaving a transitively reached module, because plain
	// requires are not re-exported to downstream readers.
	root, ok := layer.modules[caller]
	if !ok {
		return false
	}
	visited := map[string]bool{caller: true}
	pending := make([]string, 0, len(root.Requires))
	for _, requirement := range root.Requires {
		if !visited[requirement.Module] {
			visited[requirement.Module] = true
			pending = append(pending, requirement.Module)
		}
	}

	for len(pending) > 0 {
		name := pending[len(pending)-1]
		pending = pending[:len(pending)-1]

		if name == target {
			return true
		}
		descriptor, ok := layer.modules[name]
		if !ok {
			continue
		}
		for _, requirement := range descriptor.Requires {
			if !requirement.Transitive {
				continue
			}
			if visited[requirement.Module] {
				continue
			}
			visited[requirement.Module] = true
			pending = append(pending, requirement.Module)
		}
	}

	return false
}

// IsOpen reports whether package pkg of module target is opened to caller,
// independent of readability. Unnamed and automatic modules open all of their
// packages, open modules do as well, and ordinary modules rely on qualified or
// unqualified `opens` directives and on --add-opens patches.
func (layer *Layer) IsOpen(target, pkg, caller string) bool {
	// The unnamed module is open to everyone.
	if target == "" {
		return true
	}

	descriptor, ok := layer.modules[target]
	if !ok {
		return false
	}

	// Automatic and explicitly open modules open every package.
	if descriptor.Automatic || descriptor.Open {
		return true
	}

	if packages, ok := layer.extraOpens[target]; ok {
		if callers, ok := packages[pkg]; ok {
			if caller != "" && callers[caller] {
				return true
			}
			if caller == "" && callers[AllUnnamed] {
				return true
			}
		}
	}

	for _, directive := range descriptor.Opens {
		if directive.Package != pkg {
			continue
		}
		if len(directive.Targets) == 0 {
			return true
		}
		for _, friend := range directive.Targets {
			if friend == caller {
				return true
			}
		}
	}

	return false
}

// CanSetAccessible reports whether caller is allowed to call setAccessible on
// a member of package pkg declared in target. Both readability and openness
// are required, and a module always accesses its own packages.
func (layer *Layer) CanSetAccessible(caller, target, pkg string) bool {
	if caller == target && target != "" {
		return true
	}
	if !layer.CanRead(caller, target) {
		return false
	}
	return layer.IsOpen(target, pkg, caller)
}
