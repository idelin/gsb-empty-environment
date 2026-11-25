package symbolbind

import (
	"fmt"
	"strings"
)

func Bind(input Input, relocation Relocation) (*Trace, error) {
	return BindWithGlobal(input, input.GlobalOrder, relocation)
}

func BindWithGlobal(input Input, globalOrder []string, relocation Relocation) (*Trace, error) {
	if err := Validate(input); err != nil {
		return nil, err
	}
	modules := make(map[string]Module, len(input.Modules)+len(input.Plugins))
	for _, module := range input.Modules {
		modules[module.ID] = module
	}
	for _, plugin := range input.Plugins {
		modules[plugin.ID] = plugin
	}
	if _, ok := modules[relocation.OriginID]; !ok {
		return nil, fmt.Errorf("relocation origin %q is unknown", relocation.OriginID)
	}
	seenGlobal := make(map[string]bool, len(globalOrder))
	for _, id := range globalOrder {
		if _, ok := modules[id]; !ok {
			return nil, fmt.Errorf("global order references unknown module %q", id)
		}
		if seenGlobal[id] {
			return nil, fmt.Errorf("global order contains duplicate module %q", id)
		}
		seenGlobal[id] = true
	}
	return resolve(modules, globalOrder, relocation)
}

func resolve(modules map[string]Module, globalOrder []string, relocation Relocation) (*Trace, error) {
	loaded := make(map[string]bool, len(globalOrder))
	for _, id := range globalOrder {
		loaded[id] = true
	}
	sequence, err := buildSequence(modules, globalOrder, loaded, relocation)
	if err != nil {
		return nil, err
	}

	trace := &Trace{
		Relocation: relocation,
		Sequence:   sequence,
	}

	for index, entry := range sequence {
		module := modules[entry.ModuleID]
		export, ok := findExport(module.Exports, relocation.Name, relocation.Version)
		inspection := Inspection{
			Step:     index + 1,
			ModuleID: module.ID,
			Name:     relocation.Name,
			Version:  relocation.Version,
		}
		if !ok {
			inspection.SkipReason, inspection.Available = missingExportReason(module.Exports, relocation.Name, relocation.Version)
			trace.Inspections = append(trace.Inspections, inspection)
			continue
		}

		definerID, err := resolveDefiner(module.ID, export, modules, loaded)
		if err != nil {
			inspection.SkipReason = err.Error()
			trace.Inspections = append(trace.Inspections, inspection)
			continue
		}
		definer := modules[definerID]
		inspection.Matched = true
		inspection.Strength = export.Strength
		inspection.ExporterID = module.ID
		inspection.DefinerID = definerID
		trace.Inspections = append(trace.Inspections, inspection)

		trace.Candidates = append(trace.Candidates, Candidate{
			Step:            index + 1,
			ExporterID:      module.ID,
			DefinerID:       definerID,
			DefinerTrusted:  definer.Trusted,
			Name:            relocation.Name,
			Version:         relocation.Version,
			Strength:        export.Strength,
			AllowInterposed: relocation.AllowInterposition,
		})
	}

	trace.Winner = chooseWinner(trace.Candidates)
	if trace.Winner == nil {
		trace.Reason = fmt.Sprintf("no exact %s@%s definition exists in the complete search sequence", relocation.Name, relocation.Version)
		return trace, nil
	}

	trace.Resolved = true
	trace.Untrusted = relocation.RequireTrusted && !trace.Winner.DefinerTrusted
	if trace.Winner.Strength == Strong {
		trace.Reason = fmt.Sprintf(
			"%s at step %d is the first strong exact-version definition in the complete sequence, so it wins over every weak definition; its actual definer is %s with trusted=%t",
			trace.Winner.ExporterID, trace.Winner.Step, trace.Winner.DefinerID, trace.Winner.DefinerTrusted,
		)
	} else {
		trace.Reason = fmt.Sprintf(
			"no strong exact-version definition exists in the complete sequence, so %s at step %d is the first weak definition and wins; its actual definer is %s with trusted=%t",
			trace.Winner.ExporterID, trace.Winner.Step, trace.Winner.DefinerID, trace.Winner.DefinerTrusted,
		)
	}
	return trace, nil
}

func buildSequence(modules map[string]Module, globalOrder []string, loaded map[string]bool, relocation Relocation) ([]SequenceEntry, error) {
	seen := make(map[string]bool)
	sequence := make([]SequenceEntry, 0, len(globalOrder)+len(modules))
	add := func(id, source string) {
		if !seen[id] {
			seen[id] = true
			sequence = append(sequence, SequenceEntry{ModuleID: id, Source: source})
		}
	}

	if !relocation.AllowInterposition {
		add(relocation.OriginID, "origin")
	}
	for _, id := range globalOrder {
		add(id, "global")
	}

	queue := []string{relocation.OriginID}
	enqueued := map[string]bool{relocation.OriginID: true}
	for len(queue) > 0 {
		currentID := queue[0]
		queue = queue[1:]
		module := modules[currentID]
		for _, dependency := range module.Dependencies {
			if _, ok := modules[dependency]; !ok || !loaded[dependency] {
				return nil, fmt.Errorf("module %q depends on missing module %q", currentID, dependency)
			}
			if !enqueued[dependency] {
				enqueued[dependency] = true
				queue = append(queue, dependency)
				add(dependency, "dependency")
			}
		}
	}
	return sequence, nil
}

func resolveDefiner(moduleID string, export Export, modules map[string]Module, loaded map[string]bool) (string, error) {
	seen := map[string]bool{fmt.Sprintf("%s/%s/%s", moduleID, export.Name, export.Version): true}
	currentModuleID := moduleID
	currentExport := export
	for currentExport.Definition != nil {
		targetModuleID := currentExport.Definition.ModuleID
		if targetModuleID == "" {
			targetModuleID = currentModuleID
		}
		if !loaded[targetModuleID] {
			return "", fmt.Errorf("alias target module %q is not loaded", targetModuleID)
		}
		targetName := currentExport.Definition.Name
		if targetName == "" {
			targetName = currentExport.Name
		}
		targetVersion := currentExport.Definition.Version
		if targetVersion == "" {
			targetVersion = currentExport.Version
		}
		targetModule := modules[targetModuleID]
		next, ok := findExport(targetModule.Exports, targetName, targetVersion)
		if !ok {
			return "", fmt.Errorf("alias target %s@%s is missing from module %q", targetName, targetVersion, targetModuleID)
		}
		cycleKey := fmt.Sprintf("%s/%s/%s", targetModuleID, targetName, targetVersion)
		if seen[cycleKey] {
			return "", fmt.Errorf("alias cycle reaches %s@%s in module %q", targetName, targetVersion, targetModuleID)
		}
		seen[cycleKey] = true
		currentModuleID = targetModuleID
		currentExport = next
	}
	return currentModuleID, nil
}

func chooseWinner(candidates []Candidate) *Candidate {
	for index := range candidates {
		if candidates[index].Strength == Strong {
			return &candidates[index]
		}
	}
	if len(candidates) > 0 {
		return &candidates[0]
	}
	return nil
}

func missingExportReason(exports []Export, name, version string) (string, []string) {
	sameName := make([]string, 0)
	for _, export := range exports {
		if export.Name == name {
			sameName = append(sameName, fmt.Sprintf("%s@%s", export.Name, export.Version))
		}
	}
	if len(sameName) > 0 {
		return fmt.Sprintf("no exact %s@%s; same-name versions: %s", name, version, strings.Join(sameName, ", ")), sameName
	}
	return fmt.Sprintf("module does not export %s", name), nil
}
