package netsec

type moduleOverride struct {
	table      []byte
	executable []Range
	hasTable   bool
	hasExec    bool
}

type moduleSource func(index uint64) (Module, bool)

type resolveKey struct {
	module uint64
	name   string
}

type cachedModule struct {
	module Module
	found  bool
}

type loadRun struct {
	source    moduleSource
	overrides map[uint64]moduleOverride
	budget    *budgetState

	modules   map[uint64]cachedModule
	raw       map[uint64][]RawExport
	rawByName map[uint64]map[string]RawExport
	resolved  map[resolveKey]Target
	active    map[resolveKey]bool
}

// Load parses moduleIndex and every module reachable through forwards, binds
// all exports, and only then returns the complete export set. host may be a
// ModuleProvider, a map[uint64]Module, a map[uint64]*Module, or one of the
// equivalent GetModule providers.
func Load(moduleIndex uint64, host any, limit ...Limits) (Exports, error) {
	run, err := newLoadRun(host, nil, firstLimits(limit))
	if err != nil {
		return nil, err
	}
	return run.load(moduleIndex)
}

// LoadExportTrie is an alias for Load.
func LoadExportTrie(moduleIndex uint64, host any, limit ...Limits) (Exports, error) {
	return Load(moduleIndex, host, limit...)
}

// LoadTable is Load with the table for moduleIndex taken directly from table
// rather than from host. Executable ranges for moduleIndex still come from
// host when it provides that module.
func LoadTable(moduleIndex uint64, table []byte, host any, limit ...Limits) (Exports, error) {
	return LoadTableWithRanges(moduleIndex, table, nil, host, limit...)
}

// LoadTableWithRanges is LoadTable with explicit executable ranges for the
// loaded module. A nil executable slice keeps the ranges from host.
func LoadTableWithRanges(
	moduleIndex uint64,
	table []byte,
	executable []Range,
	host any,
	limit ...Limits,
) (Exports, error) {
	override := moduleOverride{
		table:    table,
		hasTable: true,
	}
	if executable != nil {
		override.executable = executable
		override.hasExec = true
	}
	run, err := newLoadRun(
		host, map[uint64]moduleOverride{moduleIndex: override}, firstLimits(limit))
	if err != nil {
		return nil, err
	}
	return run.load(moduleIndex)
}

// Parse loads the table at byte offset zero as module zero. Optional arguments
// may supply a host, Limits, executable ranges, or a module index. The common
// forms are:
//
//	Parse(table)
//	Parse(table, host, limits)
//	Parse(table, executableRanges, host, limits)
//	Parse(table, moduleIndex, host, limits)
//
// Address-bearing exports require executable ranges for their module.
func Parse(table []byte, options ...any) (Exports, error) {
	moduleIndex := uint64(0)
	limits := Limits{}
	var executable []Range
	executableSet := false
	var host any

	for _, option := range options {
		switch value := option.(type) {
		case nil:
			continue
		case Limits:
			limits = value
		case *Limits:
			if value == nil {
				return nil, invalidf("nil Limits")
			}
			limits = *value
		case uint64:
			moduleIndex = value
		case int:
			if value < 0 {
				return nil, invalidf("negative module index %d", value)
			}
			moduleIndex = uint64(value)
		case int64:
			if value < 0 {
				return nil, invalidf("negative module index %d", value)
			}
			moduleIndex = uint64(value)
		case uint:
			moduleIndex = uint64(value)
		case uint32:
			moduleIndex = uint64(value)
		case uint16:
			moduleIndex = uint64(value)
		case uint8:
			moduleIndex = uint64(value)
		case []Range:
			executable = append([]Range(nil), value...)
			executableSet = true
		case Range:
			executable = []Range{value}
			executableSet = true
		case Module:
			if value.Table != nil {
				table = value.Table
			}
			if value.Executable != nil {
				executable = append([]Range(nil), value.Executable...)
				executableSet = true
			}
		case *Module:
			if value == nil {
				return nil, invalidf("nil Module")
			}
			if value.Table != nil {
				table = value.Table
			}
			if value.Executable != nil {
				executable = append([]Range(nil), value.Executable...)
				executableSet = true
			}
		default:
			if host != nil {
				return nil, invalidf("multiple module hosts supplied to Parse")
			}
			host = value
		}
	}

	override := moduleOverride{table: table, hasTable: true}
	if executableSet {
		override.executable = executable
		override.hasExec = true
	}
	run, err := newLoadRun(host, map[uint64]moduleOverride{moduleIndex: override}, limits)
	if err != nil {
		return nil, err
	}
	return run.load(moduleIndex)
}

func newLoadRun(
	host any,
	overrides map[uint64]moduleOverride,
	limits Limits,
) (*loadRun, error) {
	source, err := moduleSourceFrom(host)
	if err != nil {
		return nil, err
	}
	return &loadRun{
		source:    source,
		overrides: overrides,
		budget:    newBudgetState(limits),
		modules:   make(map[uint64]cachedModule),
		raw:       make(map[uint64][]RawExport),
		rawByName: make(map[uint64]map[string]RawExport),
		resolved:  make(map[resolveKey]Target),
		active:    make(map[resolveKey]bool),
	}, nil
}

func moduleSourceFrom(host any) (moduleSource, error) {
	switch value := host.(type) {
	case nil:
		return nil, nil
	case ModuleProvider:
		return value.Module, nil
	case interface {
		GetModule(uint64) (Module, bool)
	}:
		return value.GetModule, nil
	case interface {
		Module(uint64) (*Module, bool)
	}:
		return func(index uint64) (Module, bool) {
			module, ok := value.Module(index)
			if !ok || module == nil {
				return Module{}, false
			}
			return *module, true
		}, nil
	case interface {
		GetModule(uint64) (*Module, bool)
	}:
		return func(index uint64) (Module, bool) {
			module, ok := value.GetModule(index)
			if !ok || module == nil {
				return Module{}, false
			}
			return *module, true
		}, nil
	case map[uint64]Module:
		return func(index uint64) (Module, bool) {
			module, ok := value[index]
			return module, ok
		}, nil
	case map[uint64]*Module:
		return func(index uint64) (Module, bool) {
			module, ok := value[index]
			if !ok || module == nil {
				return Module{}, false
			}
			return *module, true
		}, nil
	case func(uint64) (Module, bool):
		return value, nil
	case func(uint64) (*Module, bool):
		return func(index uint64) (Module, bool) {
			module, ok := value(index)
			if !ok || module == nil {
				return Module{}, false
			}
			return *module, true
		}, nil
	default:
		return nil, invalidf("unsupported module host type %T", host)
	}
}

func (r *loadRun) moduleAt(index uint64) (Module, error) {
	if cached, ok := r.modules[index]; ok {
		if !cached.found {
			return Module{}, wrapf(ErrModuleNotFound, "module %d", index)
		}
		return cached.module, nil
	}

	var module Module
	found := false
	if r.source != nil {
		module, found = r.source(index)
	}

	override, hasOverride := r.overrides[index]
	if hasOverride {
		if !found && !override.hasTable {
			r.modules[index] = cachedModule{}
			return Module{}, wrapf(ErrModuleNotFound, "module %d", index)
		}
		found = true
		if override.hasTable {
			module.Table = override.table
		}
		if override.hasExec {
			module.Executable = override.executable
		}
	}

	if !found {
		r.modules[index] = cachedModule{found: false}
		return Module{}, wrapf(ErrModuleNotFound, "module %d", index)
	}
	r.modules[index] = cachedModule{module: module, found: true}
	return module, nil
}

func (r *loadRun) rawExports(index uint64) ([]RawExport, error) {
	if exports, ok := r.raw[index]; ok {
		return exports, nil
	}

	module, err := r.moduleAt(index)
	if err != nil {
		return nil, err
	}
	table, err := parseExportTable(module.Table)
	if err != nil {
		return nil, err
	}
	exports, err := enumerateExportsWithBudget(table.Root, index, r.budget)
	if err != nil {
		return nil, err
	}

	byName := make(map[string]RawExport, len(exports))
	for _, export := range exports {
		byName[export.Name] = export
	}
	r.raw[index] = exports
	r.rawByName[index] = byName
	return exports, nil
}

func (r *loadRun) lookupRaw(key resolveKey) (RawExport, error) {
	if _, err := r.rawExports(key.module); err != nil {
		return RawExport{}, err
	}
	raw, ok := r.rawByName[key.module][key.name]
	if !ok {
		return RawExport{}, wrapf(
			ErrExportNotFound, "module %d has no export %q", key.module, key.name)
	}
	return raw, nil
}

func (r *loadRun) validateAddress(moduleIndex, address uint64) error {
	module, err := r.moduleAt(moduleIndex)
	if err != nil {
		return err
	}
	for _, interval := range module.Executable {
		if interval.Contains(address) {
			return nil
		}
	}
	return wrapf(
		ErrAddressOutOfRange,
		"module %d address 0x%x is not in an executable range",
		moduleIndex, address)
}

// resolve follows a forward chain iteratively. active contains exactly the
// chain currently being resolved, so a repeated key is a forward cycle. A
// completed key is memoized and can safely be reused by another export.
func (r *loadRun) resolve(start resolveKey) (Target, error) {
	var target Target
	haveTarget := false
	var stack []resolveKey
	key := start

	for {
		if !haveTarget {
			if cached, ok := r.resolved[key]; ok {
				target = cached
				haveTarget = true
			} else if r.active[key] {
				return Target{}, wrapf(
					ErrForwardCycle,
					"module %d export %q", key.module, key.name)
			} else {
				raw, err := r.lookupRaw(key)
				if err != nil {
					return Target{}, err
				}

				if raw.Flags == FlagAddress {
					if err := r.validateAddress(key.module, raw.Address); err != nil {
						return Target{}, err
					}
					target = Target{Module: key.module, Address: raw.Address}
					r.resolved[key] = target
					haveTarget = true
				} else {
					r.active[key] = true
					stack = append(stack, key)
					nextName := raw.ForwardName
					if nextName == "" {
						nextName = key.name
					}
					key = resolveKey{module: raw.TargetModule, name: nextName}
					continue
				}
			}
		}

		if len(stack) == 0 {
			return target, nil
		}

		parent := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		r.resolved[parent] = target
		delete(r.active, parent)
	}
}

func (r *loadRun) load(moduleIndex uint64) (Exports, error) {
	raw, err := r.rawExports(moduleIndex)
	if err != nil {
		return nil, err
	}

	// Build the complete set in a local map. Nothing is returned unless every
	// name has also been bound successfully.
	exports := make(Exports, len(raw))
	for _, item := range raw {
		target, err := r.resolve(resolveKey{module: moduleIndex, name: item.Name})
		if err != nil {
			return nil, err
		}
		exports[item.Name] = Export{
			Name:    item.Name,
			Module:  target.Module,
			Address: target.Address,
		}
	}
	return exports, nil
}
