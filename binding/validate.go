package binding

import (
	"errors"
	"fmt"
)

type exportKey struct {
	name    string
	version string
}

type catalog struct {
	modules map[string]*Module
	exports map[string]map[exportKey]Export
	order   []string
}

func newCatalog(modules []Module, order []string) (*catalog, error) {
	if err := validateModuleSet(modules, true); err != nil {
		return nil, err
	}

	byID := make(map[string]*Module, len(modules))
	exports := make(map[string]map[exportKey]Export, len(modules))
	for index := range modules {
		module := modules[index]
		byID[module.ID] = &module
		moduleExports := make(map[exportKey]Export, len(module.Exports))
		for _, export := range module.Exports {
			moduleExports[exportKey{export.Name, export.Version}] = export
		}
		exports[module.ID] = moduleExports
	}

	seen := make(map[string]bool, len(order))
	for _, id := range order {
		if _, ok := byID[id]; !ok {
			return nil, fmt.Errorf("global order references missing module %q", id)
		}
		if seen[id] {
			return nil, fmt.Errorf("global order contains duplicate module %q", id)
		}
		seen[id] = true
	}
	for _, module := range modules {
		if !seen[module.ID] {
			return nil, fmt.Errorf("module %q is missing from global order", module.ID)
		}
	}

	return &catalog{modules: byID, exports: exports, order: order}, nil
}

func validateModules(modules []Module) error {
	return validateModuleSet(modules, true)
}

func validateModuleSet(modules []Module, enforceModuleLimit bool) error {
	if len(modules) == 0 {
		return errors.New("module list is empty")
	}
	if enforceModuleLimit && len(modules) > 16 {
		return fmt.Errorf("module count %d exceeds limit 16", len(modules))
	}

	ids := make(map[string]bool, len(modules))
	for _, module := range modules {
		if module.ID == "" {
			return errors.New("module ID is empty")
		}
		if ids[module.ID] {
			return fmt.Errorf("duplicate module ID %q", module.ID)
		}
		ids[module.ID] = true
	}

	for _, module := range modules {
		dependencySeen := make(map[string]bool, len(module.Dependencies))
		for _, dependency := range module.Dependencies {
			if dependency == "" {
				return fmt.Errorf("module %q has an empty dependency", module.ID)
			}
			if !ids[dependency] {
				return fmt.Errorf("module %q depends on missing module %q", module.ID, dependency)
			}
			if dependencySeen[dependency] {
				return fmt.Errorf("module %q has duplicate dependency %q", module.ID, dependency)
			}
			dependencySeen[dependency] = true
		}

		exportSeen := make(map[exportKey]bool, len(module.Exports))
		for _, export := range module.Exports {
			if export.Name == "" {
				return fmt.Errorf("module %q has an export with an empty name", module.ID)
			}
			if export.Version == "" {
				return fmt.Errorf("module %q export %q has an empty version", module.ID, export.Name)
			}
			if export.Strength != Weak && export.Strength != Strong {
				return fmt.Errorf("module %q export %s/%s has invalid strength %q", module.ID, export.Name, export.Version, export.Strength)
			}
			key := exportKey{export.Name, export.Version}
			if exportSeen[key] {
				return fmt.Errorf("module %q has duplicate export %s/%s", module.ID, export.Name, export.Version)
			}
			exportSeen[key] = true

			if export.AliasOf != nil {
				if export.AliasOf.ModuleID == "" || export.AliasOf.Name == "" || export.AliasOf.Version == "" {
					return fmt.Errorf("module %q alias %s/%s has an incomplete target", module.ID, export.Name, export.Version)
				}
				if !ids[export.AliasOf.ModuleID] {
					return fmt.Errorf("module %q alias %s/%s targets missing module %q", module.ID, export.Name, export.Version, export.AliasOf.ModuleID)
				}
				targetModule := findModule(modules, export.AliasOf.ModuleID)
				if !moduleHasExport(targetModule, export.AliasOf.Name, export.AliasOf.Version) {
					return fmt.Errorf("module %q alias %s/%s targets missing export %s/%s in %q", module.ID, export.Name, export.Version, export.AliasOf.Name, export.AliasOf.Version, export.AliasOf.ModuleID)
				}
				if aliasChainHasCycle(modules, module.ID, export) {
					return fmt.Errorf("alias %s/%s in module %q reaches a cycle", export.Name, export.Version, module.ID)
				}
			}
		}
	}

	return nil
}

func aliasChainHasCycle(modules []Module, startModuleID string, startExport Export) bool {
	visitedExports := map[string]bool{}
	currentModuleID := startModuleID
	currentExport := startExport

	for currentExport.AliasOf != nil {
		key := currentModuleID + "\x00" + currentExport.Name + "\x00" + currentExport.Version
		if visitedExports[key] {
			return true
		}
		visitedExports[key] = true

		targetModule := findModule(modules, currentExport.AliasOf.ModuleID)
		if targetModule == nil {
			return false
		}
		targetExport := findExport(targetModule, currentExport.AliasOf.Name, currentExport.AliasOf.Version)
		if targetExport == nil {
			return false
		}

		currentModuleID = targetModule.ID
		currentExport = *targetExport
	}

	return false
}

func findExport(module *Module, name, version string) *Export {
	if module == nil {
		return nil
	}
	for index := range module.Exports {
		if module.Exports[index].Name == name && module.Exports[index].Version == version {
			return &module.Exports[index]
		}
	}
	return nil
}

func findModule(modules []Module, id string) *Module {
	for index := range modules {
		if modules[index].ID == id {
			return &modules[index]
		}
	}
	return nil
}

func moduleHasExport(module *Module, name, version string) bool {
	if module == nil {
		return false
	}
	for _, export := range module.Exports {
		if export.Name == name && export.Version == version {
			return true
		}
	}
	return false
}

func validateRelocations(modules []Module, relocations []Relocation) error {
	ids := make(map[string]bool, len(modules))
	for _, module := range modules {
		ids[module.ID] = true
	}

	for _, relocation := range relocations {
		if relocation.SourceID == "" {
			return errors.New("relocation has an empty source module")
		}
		if !ids[relocation.SourceID] {
			return fmt.Errorf("relocation source %q is missing", relocation.SourceID)
		}
		if relocation.Name == "" {
			return fmt.Errorf("relocation from %q has an empty symbol name", relocation.SourceID)
		}
		if relocation.Version == "" {
			return fmt.Errorf("relocation %q from %q has an empty version", relocation.Name, relocation.SourceID)
		}
	}

	return nil
}
