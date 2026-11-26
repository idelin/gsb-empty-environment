package binding

import (
	"fmt"
	"sort"
)

const (
	defaultAttackBudget   = 100000
	defaultMaxAttackPlans = 20
	maxAllowedPlugins     = 3
)

// FindAttackPlans searches minimum-size insertions of the supplied plugins
// that make relocation bind an untrusted actual definer. It preserves the
// relative order of base modules and returns at most MaxPlans equally sized
// plans, sorted by plugin ID and zero-based insertion position.
func FindAttackPlans(base []Module, globalOrder []string, plugins []Module, relocation Relocation, options AttackOptions) (AttackReport, error) {
	if !relocation.RequireTrusted {
		return AttackReport{}, fmt.Errorf("attack relocation %s/%s must require a trusted target", relocation.Name, relocation.Version)
	}

	maxPlugins := normalizeMaxPlugins(options.MaxPlugins)
	budget := normalizeBudget(options.SearchBudget)
	maxPlans := normalizeMaxPlans(options.MaxPlans)

	allModules := append(append([]Module(nil), base...), plugins...)
	if err := validateModuleSet(allModules, false); err != nil {
		return AttackReport{}, err
	}
	if len(base) > 16 {
		return AttackReport{}, fmt.Errorf("base module count %d exceeds limit 16", len(base))
	}
	if err := validateRelocations(allModules, []Relocation{relocation}); err != nil {
		return AttackReport{}, err
	}
	if err := validateBaseGlobalOrder(base, globalOrder); err != nil {
		return AttackReport{}, err
	}

	report := AttackReport{
		MinimumPlugins: -1,
		BudgetUsed:     0,
	}

	for count := 0; count <= maxPlugins; count++ {
		plans, truncated, budgetExhausted, budgetUsed, err := enumerateAttacks(base, globalOrder, plugins, relocation, count, budget-report.BudgetUsed, maxPlans)
		report.BudgetUsed += budgetUsed
		if err != nil {
			return AttackReport{}, err
		}

		if len(plans) > 0 {
			report.Status = AttackSolved
			report.MinimumPlugins = count
			report.Plans = plans
			report.Truncated = truncated
			report.Reason = fmt.Sprintf("found minimum %d-plugin attack; output is capped at %d plans", count, maxPlans)
			if budgetExhausted {
				report.Status = AttackUnknown
				report.Reason = fmt.Sprintf("search budget %d exhausted after finding %d candidates; minimum and completeness are unknown", budget, len(plans))
			}
			return report, nil
		}

		if budgetExhausted {
			report.Status = AttackUnknown
			report.Reason = fmt.Sprintf("search budget %d exhausted before proving a minimum attack", budget)
			return report, nil
		}
	}

	report.Status = AttackNoPlan
	report.MinimumPlugins = -1
	report.Reason = fmt.Sprintf("no attack with at most %d plugins binds the trusted relocation to an untrusted definition", maxPlugins)
	return report, nil
}

func enumerateAttacks(base []Module, globalOrder []string, plugins []Module, relocation Relocation, count, budget, maxPlans int) ([]AttackPlan, bool, bool, int, error) {
	budgetUsed := 0
	var plans []AttackPlan
	truncated := false

	if count == 0 {
		budgetUsed++
		if budgetUsed > budget {
			return nil, false, true, budgetUsed, nil
		}

		plan, ok, err := buildAttackPlan(base, globalOrder, nil, globalOrder, relocation)
		if err != nil {
			return nil, false, false, budgetUsed, err
		}
		if ok {
			plans = append(plans, plan)
		}
		return plans, false, false, budgetUsed, nil
	}

	indices := make([]int, count)
	var enumerateIndices func(start, depth int) (bool, error)
	enumerateIndices = func(start, depth int) (bool, error) {
		if depth == count {
			return enumeratePositions(indices, base, globalOrder, plugins, relocation, budget, maxPlans, &budgetUsed, &plans, &truncated)
		}

		for index := start; index < len(plugins); index++ {
			indices[depth] = index
			stop, err := enumerateIndices(index+1, depth+1)
			if err != nil {
				return true, err
			}
			if stop {
				return true, nil
			}
		}
		return false, nil
	}

	stop, err := enumerateIndices(0, 0)
	if err != nil {
		return nil, false, false, budgetUsed, err
	}
	if stop {
		return plans, truncated, true, budgetUsed, nil
	}

	return plans, truncated, false, budgetUsed, nil
}

func enumeratePositions(indices []int, base []Module, globalOrder []string, plugins []Module, relocation Relocation, budget, maxPlans int, budgetUsed *int, plans *[]AttackPlan, truncated *bool) (bool, error) {
	positions := make([]int, len(indices))
	var walk func(depth int) bool
	var walkErr error
	walk = func(depth int) bool {
		if depth == len(indices) {
			*budgetUsed++
			if *budgetUsed > budget {
				return true
			}

			selectedModules := make([]Module, 0, len(indices))
			placements := make([]PluginPlacement, 0, len(indices))
			for slot, pluginIndex := range indices {
				selectedModules = append(selectedModules, plugins[pluginIndex])
				placements = append(placements, PluginPlacement{
					PluginID: plugins[pluginIndex].ID,
					Position: positions[slot],
				})
			}

			finalOrder := insertAtPositions(globalOrder, placements)
			plan, ok, err := buildAttackPlan(base, globalOrder, selectedModules, finalOrder, relocation)
			if err != nil {
				walkErr = err
				return true
			}
			if ok {
				addSortedPlan(plans, plan, maxPlans, truncated)
			}
			return false
		}

		minimum := 0
		if depth > 0 {
			minimum = positions[depth-1]
		}
		for position := minimum; position <= len(globalOrder); position++ {
			positions[depth] = position
			if walk(depth + 1) {
				return true
			}
		}
		return false
	}

	exhausted := walk(0)
	return exhausted, walkErr
}

func buildAttackPlan(base []Module, baseOrder []string, selectedPlugins []Module, finalOrder []string, relocation Relocation) (AttackPlan, bool, error) {
	loadedModules := append(append([]Module(nil), base...), selectedPlugins...)
	if len(loadedModules) > 16 {
		return AttackPlan{}, false, nil
	}
	if !arrangementIsLoadable(loadedModules, finalOrder) {
		return AttackPlan{}, false, nil
	}

	result, err := Bind(loadedModules, finalOrder, relocation)
	if err != nil {
		return AttackPlan{}, false, err
	}
	if !result.Found || !result.TrustViolation || result.DefinerTrusted {
		return AttackPlan{}, false, nil
	}

	return AttackPlan{
		Placements:  placementsForOrder(baseOrder, finalOrder, selectedPlugins),
		GlobalOrder: append([]string(nil), finalOrder...),
		Binding:     result,
	}, true, nil
}

func arrangementIsLoadable(modules []Module, order []string) bool {
	byID := make(map[string]Module, len(modules))
	for _, module := range modules {
		byID[module.ID] = module
	}

	seen := make(map[string]bool, len(order))
	for _, id := range order {
		module, ok := byID[id]
		if !ok || seen[id] {
			return false
		}
		seen[id] = true

		for _, dependency := range module.Dependencies {
			if _, ok := byID[dependency]; !ok {
				return false
			}
		}
		for _, export := range module.Exports {
			if export.AliasOf != nil {
				if _, ok := byID[export.AliasOf.ModuleID]; !ok {
					return false
				}
			}
		}
	}

	return len(order) == len(modules)
}

func insertAtPositions(baseOrder []string, placements []PluginPlacement) []string {
	sortedPlacements := append([]PluginPlacement(nil), placements...)
	sort.SliceStable(sortedPlacements, func(i, j int) bool {
		if sortedPlacements[i].Position != sortedPlacements[j].Position {
			return sortedPlacements[i].Position < sortedPlacements[j].Position
		}
		return sortedPlacements[i].PluginID < sortedPlacements[j].PluginID
	})

	finalOrder := make([]string, 0, len(baseOrder)+len(sortedPlacements))
	baseIndex := 0
	for baseIndex < len(baseOrder) {
		position := len(finalOrder)
		for _, placement := range sortedPlacements {
			if placement.Position == position {
				finalOrder = append(finalOrder, placement.PluginID)
			}
		}
		finalOrder = append(finalOrder, baseOrder[baseIndex])
		baseIndex++
	}

	position := len(finalOrder)
	for _, placement := range sortedPlacements {
		if placement.Position == position {
			finalOrder = append(finalOrder, placement.PluginID)
		}
	}

	return finalOrder
}

func placementsForOrder(baseOrder, finalOrder []string, selectedPlugins []Module) []PluginPlacement {
	pluginIDs := make(map[string]bool, len(selectedPlugins))
	for _, plugin := range selectedPlugins {
		pluginIDs[plugin.ID] = true
	}

	placements := make([]PluginPlacement, 0, len(selectedPlugins))
	for index, id := range finalOrder {
		if pluginIDs[id] {
			placements = append(placements, PluginPlacement{PluginID: id, Position: index})
		}
	}

	sort.Slice(placements, func(i, j int) bool {
		if placements[i].PluginID != placements[j].PluginID {
			return placements[i].PluginID < placements[j].PluginID
		}
		return placements[i].Position < placements[j].Position
	})
	return placements
}

func addSortedPlan(plans *[]AttackPlan, plan AttackPlan, maxPlans int, truncated *bool) {
	for _, existing := range *plans {
		if planKey(plan) == planKey(existing) {
			return
		}
	}

	*plans = append(*plans, plan)
	sort.SliceStable(*plans, func(i, j int) bool {
		return planLess((*plans)[i], (*plans)[j])
	})

	if len(*plans) > maxPlans {
		*plans = (*plans)[:maxPlans]
		*truncated = true
	}
}

func sortedPlacementsCopy(plan AttackPlan) []PluginPlacement {
	placements := append([]PluginPlacement(nil), plan.Placements...)
	sort.Slice(placements, func(i, j int) bool {
		if placements[i].PluginID != placements[j].PluginID {
			return placements[i].PluginID < placements[j].PluginID
		}
		return placements[i].Position < placements[j].Position
	})
	return placements
}

func planKey(plan AttackPlan) string {
	key := ""
	for _, placement := range sortedPlacementsCopy(plan) {
		key += placement.PluginID + "@" + fmt.Sprint(placement.Position) + ";"
	}
	return key
}

func planLess(left, right AttackPlan) bool {
	leftPlacements := sortedPlacementsCopy(left)
	rightPlacements := sortedPlacementsCopy(right)
	shared := len(leftPlacements)
	if len(rightPlacements) < shared {
		shared = len(rightPlacements)
	}

	for index := 0; index < shared; index++ {
		if leftPlacements[index].PluginID != rightPlacements[index].PluginID {
			return leftPlacements[index].PluginID < rightPlacements[index].PluginID
		}
		if leftPlacements[index].Position != rightPlacements[index].Position {
			return leftPlacements[index].Position < rightPlacements[index].Position
		}
	}

	if len(leftPlacements) != len(rightPlacements) {
		return len(leftPlacements) < len(rightPlacements)
	}
	return planKey(left) < planKey(right)
}

func validateBaseGlobalOrder(base []Module, globalOrder []string) error {
	baseIDs := make(map[string]bool, len(base))
	for _, module := range base {
		baseIDs[module.ID] = true
	}

	seen := make(map[string]bool, len(globalOrder))
	for _, id := range globalOrder {
		if !baseIDs[id] {
			return fmt.Errorf("global order contains non-base module %q", id)
		}
		if seen[id] {
			return fmt.Errorf("global order contains duplicate module %q", id)
		}
		seen[id] = true
	}
	for _, module := range base {
		if !seen[module.ID] {
			return fmt.Errorf("base module %q is missing from global order", module.ID)
		}
	}
	return nil
}

func normalizeMaxPlugins(value int) int {
	if value <= 0 || value > maxAllowedPlugins {
		return maxAllowedPlugins
	}
	return value
}

func normalizeBudget(value int) int {
	if value <= 0 {
		return defaultAttackBudget
	}
	return value
}

func normalizeMaxPlans(value int) int {
	if value <= 0 {
		return defaultMaxAttackPlans
	}
	return value
}
