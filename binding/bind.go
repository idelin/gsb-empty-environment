package binding

import (
	"fmt"
	"sort"
)

// Bind resolves one relocation using the package's custom symbol rules.
// Missing modules, missing dependencies, duplicate module IDs, and duplicate
// name/version exports are input errors; an absent exact symbol leaves
// BindingResult.Found false instead of returning an error.
func Bind(modules []Module, globalOrder []string, relocation Relocation) (BindingResult, error) {
	cat, err := newCatalog(modules, globalOrder)
	if err != nil {
		return BindingResult{}, err
	}
	if err := validateRelocations(modules, []Relocation{relocation}); err != nil {
		return BindingResult{}, err
	}

	searchOrder, err := cat.searchOrder(relocation)
	if err != nil {
		return BindingResult{}, err
	}

	result := BindingResult{
		Relocation:  relocation,
		SearchOrder: append([]string(nil), searchOrder...),
	}

	for _, moduleID := range searchOrder {
		module := cat.modules[moduleID]
		moduleExports := cat.exports[moduleID]
		event := SearchEvent{
			ModuleID:      moduleID,
			ModuleTrusted: module.Trusted,
			Status:        NoSymbol,
		}

		versions := make([]string, 0, len(moduleExports))
		for key := range moduleExports {
			if key.name == relocation.Name {
				versions = append(versions, key.version)
			}
		}
		sort.Strings(versions)
		event.AvailableVersions = versions

		export, ok := moduleExports[exportKey{relocation.Name, relocation.Version}]
		if !ok {
			if len(versions) > 0 {
				event.Status = VersionMismatch
			}
			result.Events = append(result.Events, event)
			continue
		}

		event.Status = Matched
		event.Strength = export.Strength
		event.Alias = export.AliasOf != nil
		event.AliasTarget = cloneSymbolRef(export.AliasOf)

		definerID, definerTrusted, err := cat.resolveDefiner(moduleID, export)
		if err != nil {
			return BindingResult{}, err
		}
		event.DefinerID = definerID
		event.DefinerTrusted = definerTrusted
		result.Events = append(result.Events, event)

		binding := SymbolBinding{
			ModuleID:       moduleID,
			DefinerID:      definerID,
			Export:         export,
			DefinerTrusted: definerTrusted,
		}
		if export.Strength == Strong {
			result.StrongCandidates = append(result.StrongCandidates, binding)
		} else {
			result.WeakCandidates = append(result.WeakCandidates, binding)
		}
	}

	winner := chooseWinner(result.StrongCandidates, result.WeakCandidates)
	if winner == nil {
		result.Reason = fmt.Sprintf("no exact %s/%s definition was found in the complete search sequence", relocation.Name, relocation.Version)
		return result, nil
	}

	result.Found = true
	result.WinnerModuleID = winner.ModuleID
	result.DefinerModuleID = winner.DefinerID
	result.Export = winner.Export
	result.DefinerTrusted = winner.DefinerTrusted
	result.TrustViolation = relocation.RequireTrusted && !winner.DefinerTrusted

	result.Reason = winnerReason(relocation, *winner, result.StrongCandidates, result.WeakCandidates)
	return result, nil
}

func (cat *catalog) searchOrder(relocation Relocation) ([]string, error) {
	source, ok := cat.modules[relocation.SourceID]
	if !ok {
		return nil, fmt.Errorf("relocation source %q is missing", relocation.SourceID)
	}

	seen := map[string]bool{source.ID: true}
	order := make([]string, 0, len(cat.modules))
	order = append(order, source.ID)

	if relocation.AllowPreemption {
		for _, id := range cat.order {
			if !seen[id] {
				seen[id] = true
				order = append(order, id)
			}
		}
	}

	queue := make([]string, 0, len(source.Dependencies))
	for _, dependency := range source.Dependencies {
		if _, ok := cat.modules[dependency]; !ok {
			return nil, fmt.Errorf("source module %q depends on missing module %q", source.ID, dependency)
		}
		if !seen[dependency] {
			seen[dependency] = true
			queue = append(queue, dependency)
		}
	}

	for len(queue) > 0 {
		moduleID := queue[0]
		queue = queue[1:]
		order = append(order, moduleID)

		for _, dependency := range cat.modules[moduleID].Dependencies {
			if _, ok := cat.modules[dependency]; !ok {
				return nil, fmt.Errorf("module %q depends on missing module %q", moduleID, dependency)
			}
			if !seen[dependency] {
				seen[dependency] = true
				queue = append(queue, dependency)
			}
		}
	}

	if !relocation.AllowPreemption {
		for _, id := range cat.order {
			if !seen[id] {
				seen[id] = true
				order = append(order, id)
			}
		}
	}

	return order, nil
}

func (cat *catalog) resolveDefiner(moduleID string, export Export) (string, bool, error) {
	currentModuleID := moduleID
	currentExport := export
	visited := map[string]bool{}

	for currentExport.AliasOf != nil {
		key := currentModuleID + "\x00" + currentExport.Name + "\x00" + currentExport.Version
		if visited[key] {
			return "", false, fmt.Errorf("alias chain starting at %s/%s in %q contains a cycle", export.Name, export.Version, moduleID)
		}
		visited[key] = true

		target := currentExport.AliasOf
		targetModule, ok := cat.modules[target.ModuleID]
		if !ok {
			return "", false, fmt.Errorf("alias in %q targets missing module %q", currentModuleID, target.ModuleID)
		}
		targetExport, ok := cat.exports[target.ModuleID][exportKey{target.Name, target.Version}]
		if !ok {
			return "", false, fmt.Errorf("alias in %q targets missing export %s/%s in %q", currentModuleID, target.Name, target.Version, target.ModuleID)
		}

		currentModuleID = targetModule.ID
		currentExport = targetExport
	}

	return currentModuleID, cat.modules[currentModuleID].Trusted, nil
}

func chooseWinner(strong, weak []SymbolBinding) *SymbolBinding {
	if len(strong) > 0 {
		return &strong[0]
	}
	if len(weak) > 0 {
		return &weak[0]
	}
	return nil
}

func winnerReason(relocation Relocation, winner SymbolBinding, strong, weak []SymbolBinding) string {
	selection := "first weak definition"
	if winner.Export.Strength == Strong {
		selection = "first strong definition"
	} else if len(strong) > 0 {
		selection = "first weak definition because no strong definition was present"
	}

	aliasSuffix := ""
	if winner.Export.AliasOf != nil {
		aliasSuffix = fmt.Sprintf(" through alias %s/%s; trust is taken from actual definer %q", winner.Export.Name, winner.Export.Version, winner.DefinerID)
	}

	trustSuffix := ""
	if relocation.RequireTrusted && !winner.DefinerTrusted {
		trustSuffix = "; required trusted target but the actual definer is untrusted"
	}

	return fmt.Sprintf("selected %s in module %q%s; %d strong and %d weak exact-version candidates were considered%s", selection, winner.ModuleID, aliasSuffix, len(strong), len(weak), trustSuffix)
}

func cloneSymbolRef(ref *SymbolRef) *SymbolRef {
	if ref == nil {
		return nil
	}
	copyRef := *ref
	return &copyRef
}
