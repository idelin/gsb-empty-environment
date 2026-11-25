package symbolbind

import (
	"fmt"
)

type exportKey struct {
	name    string
	version string
}

func Validate(input Input) error {
	if len(input.Modules)+len(input.Plugins) > 16 {
		return fmt.Errorf("total module count %d exceeds 16", len(input.Modules)+len(input.Plugins))
	}
	if len(input.Modules) == 0 {
		return fmt.Errorf("at least one host module is required")
	}

	modules := make(map[string]Module, len(input.Modules)+len(input.Plugins))
	for _, module := range input.Modules {
		if module.ID == "" {
			return fmt.Errorf("module ID cannot be empty")
		}
		if _, exists := modules[module.ID]; exists {
			return fmt.Errorf("duplicate module ID %q", module.ID)
		}
		modules[module.ID] = module
	}
	for _, plugin := range input.Plugins {
		if plugin.ID == "" {
			return fmt.Errorf("plugin ID cannot be empty")
		}
		if _, exists := modules[plugin.ID]; exists {
			return fmt.Errorf("duplicate module ID %q", plugin.ID)
		}
		modules[plugin.ID] = plugin
	}

	if len(input.GlobalOrder) != len(input.Modules) {
		return fmt.Errorf("global order must contain exactly the host modules")
	}
	seenGlobal := make(map[string]bool, len(input.GlobalOrder))
	for _, id := range input.GlobalOrder {
		if _, ok := modules[id]; !ok {
			return fmt.Errorf("global order references unknown module %q", id)
		}
		if seenGlobal[id] {
			return fmt.Errorf("global order contains duplicate module %q", id)
		}
		seenGlobal[id] = true
	}

	for _, module := range modules {
		for _, dependency := range module.Dependencies {
			if _, ok := modules[dependency]; !ok {
				return fmt.Errorf("module %q depends on missing module %q", module.ID, dependency)
			}
		}
		if err := validateExports(module, modules); err != nil {
			return err
		}
	}
	return nil
}

func validateExports(module Module, modules map[string]Module) error {
	exports := make(map[exportKey]bool, len(module.Exports))
	for _, export := range module.Exports {
		if export.Name == "" || export.Version == "" {
			return fmt.Errorf("module %q has an export with an empty name or version", module.ID)
		}
		if export.Strength != Weak && export.Strength != Strong {
			return fmt.Errorf("module %q export %s@%s has invalid strength %q", module.ID, export.Name, export.Version, export.Strength)
		}
		key := exportKey{export.Name, export.Version}
		if exports[key] {
			return fmt.Errorf("module %q has duplicate export %s@%s", module.ID, export.Name, export.Version)
		}
		exports[key] = true

		seen := map[string]bool{fmt.Sprintf("%s/%s/%s", module.ID, export.Name, export.Version): true}
		currentModuleID := module.ID
		currentExport := export
		for currentExport.Definition != nil {
			targetModuleID := currentExport.Definition.ModuleID
			if targetModuleID == "" {
				targetModuleID = currentModuleID
			}
			targetModule, ok := modules[targetModuleID]
			if !ok {
				return fmt.Errorf("module %q export %s@%s points to missing module %q", currentModuleID, currentExport.Name, currentExport.Version, targetModuleID)
			}
			targetName := currentExport.Definition.Name
			if targetName == "" {
				targetName = currentExport.Name
			}
			targetVersion := currentExport.Definition.Version
			if targetVersion == "" {
				targetVersion = currentExport.Version
			}
			next, ok := findExport(targetModule.Exports, targetName, targetVersion)
			if !ok {
				return fmt.Errorf("module %q export %s@%s points to missing export %s@%s in %q", currentModuleID, currentExport.Name, currentExport.Version, targetName, targetVersion, targetModuleID)
			}
			cycleKey := fmt.Sprintf("%s/%s/%s", targetModuleID, targetName, targetVersion)
			if seen[cycleKey] {
				return fmt.Errorf("alias cycle reaches %s@%s in module %q", targetName, targetVersion, targetModuleID)
			}
			seen[cycleKey] = true
			currentModuleID = targetModuleID
			currentExport = next
		}
	}
	return nil
}

func findExport(exports []Export, name, version string) (Export, bool) {
	for _, export := range exports {
		if export.Name == name && export.Version == version {
			return export, true
		}
	}
	return Export{}, false
}
