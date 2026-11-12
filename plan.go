package taskpkg

import (
	"fmt"
	"strings"
)

// Plan computes a complete minimum-length rollout and verifies every reachable
// intermediate configuration before returning it. On ErrNoSafeOrder the
// returned plan is nil, so a caller cannot accidentally publish a first step
// from a rollout that has no safe completion.
func Plan(req Request) (*UpdatePlan, error) {
	m, err := compile(req)
	if err != nil {
		return nil, err
	}
	if err := m.rejectLostRequiredRegions(); err != nil {
		return nil, err
	}

	order, err := m.findOrder()
	if err != nil {
		return nil, err
	}
	verified, err := m.verify(req, order)
	if err != nil {
		return nil, err
	}

	steps := make([]Step, 0, len(order))
	for _, regionIndex := range order {
		region := m.regions[regionIndex]
		steps = append(steps, Step{
			Region:      region.id,
			FromVersion: region.current,
			ToVersion:   region.target,
		})
	}

	return &UpdatePlan{
		Steps:                  steps,
		MinimumSteps:           len(steps),
		VerifiedConfigurations: verified,
	}, nil
}

// BuildPlan is an alias for Plan.
func BuildPlan(req Request) (*UpdatePlan, error) {
	return Plan(req)
}

// PlanUpdates is an alias for Plan.
func PlanUpdates(req Request) (*UpdatePlan, error) {
	return Plan(req)
}

// ValidatePlan checks that plan contains exactly one atomic replacement for
// every required region and that the requested receipt/loss assumptions hold
// for all of those steps.
func ValidatePlan(req Request, plan *UpdatePlan) error {
	if plan == nil {
		return invalid("nil plan")
	}
	m, err := compile(req)
	if err != nil {
		return err
	}
	if err := m.rejectLostRequiredRegions(); err != nil {
		return err
	}

	order := make([]int, 0, len(plan.Steps))
	seen := make(map[string]bool, len(plan.Steps))
	for _, step := range plan.Steps {
		regionIndex, ok := m.byID[step.Region]
		if !ok {
			return invalid("plan names unknown region %q", step.Region)
		}
		region := m.regions[regionIndex]
		if !region.required {
			return invalid("plan updates region %q whose versions do not differ", step.Region)
		}
		if seen[step.Region] {
			return invalid("plan updates region %q more than once", step.Region)
		}
		if step.FromVersion != region.current || step.ToVersion != region.target {
			return invalid("plan step for region %q is not a %s -> %s replacement",
				step.Region, region.current, region.target)
		}
		seen[step.Region] = true
		order = append(order, regionIndex)
	}
	if len(order) != len(m.required) {
		return invalid("plan updates %d regions; %d require updates", len(order), len(m.required))
	}

	_, err = m.verify(req, order)
	return err
}

func (m *model) rejectLostRequiredRegions() error {
	for id := range m.lost {
		region := m.regions[m.byID[id]]
		if region.required {
			return fmt.Errorf("%w: required region %q is unreachable, so its %s -> %s replacement cannot be guaranteed",
				ErrNoSafeOrder, id, region.current, region.target)
		}
	}
	return nil
}

func (m *model) findOrder() ([]int, error) {
	if len(m.required) == 0 {
		return nil, nil
	}

	size := 1 << len(m.required)
	goal := size - 1
	parent := make([]int, size)
	for i := range parent {
		parent[i] = -2
	}
	parent[0] = -1
	queue := []int{0}

	for head := 0; head < len(queue); head++ {
		current := queue[head]
		for _, regionIndex := range m.required {
			bit := int(m.regions[regionIndex].bit)
			if current&bit != 0 {
				continue
			}
			next := current | bit
			if parent[next] != -2 || !m.worlds[next].safe() {
				continue
			}
			parent[next] = current
			queue = append(queue, next)
		}
	}

	if parent[goal] == -2 {
		return nil, fmt.Errorf("%w: no sequence of individually safe atomic replacements reaches the target versions",
			ErrNoSafeOrder)
	}

	order := make([]int, 0, len(m.required))
	for current := goal; current != 0; {
		previous := parent[current]
		diff := current ^ previous
		bitIndex := trailingZeroBit(diff)
		order = append(order, m.required[bitIndex])
		current = previous
	}
	reverseInts(order)
	return order, nil
}

func (m *model) verify(req Request, order []int) (int, error) {
	// An out-of-order receipt or a lost region means that any subset of the
	// issued replacements may be installed. At completion every subset is
	// therefore possible, so enumerating all worlds is sufficient.
	if !req.AssumeOrderedReceipts || !req.AssumeNoRegionLoss {
		for mask := range m.worlds {
			if err := m.checkMask(mask); err != nil {
				return 0, err
			}
		}
		return len(m.worlds), nil
	}

	// With ordered receipts and no loss, only the exact prefix is installed.
	// A region without confirmation capability still has to be checked with
	// its old edge set, because the host cannot prove the new set is active.
	if len(order) == 0 {
		if err := m.checkMask(0); err != nil {
			return 0, err
		}
		return 1, nil
	}

	checked := 0
	prefix := 0
	for _, regionIndex := range order {
		prefix |= int(m.regions[regionIndex].bit)
		uncertain := 0
		for _, requiredIndex := range m.required {
			region := m.regions[requiredIndex]
			if prefix&int(region.bit) != 0 && !region.canConfirm {
				uncertain |= int(region.bit)
			}
		}

		for oldMask := uncertain; ; oldMask = (oldMask - 1) & uncertain {
			actual := prefix &^ oldMask
			if err := m.checkMask(actual); err != nil {
				return 0, err
			}
			checked++
			if oldMask == 0 {
				break
			}
		}
	}
	return checked, nil
}

func (m *model) checkMask(mask int) error {
	info := m.worlds[mask]
	if !info.attackBlocked {
		return fmt.Errorf("%w: configuration %s leaves attack path %s -> %s",
			ErrNoSafeOrder, m.installedLabel(mask), info.attackFrom, info.attackTo)
	}
	if !info.managementPreserved {
		return fmt.Errorf("%w: configuration %s does not preserve management path %s -> %s",
			ErrNoSafeOrder, m.installedLabel(mask), info.managementFrom, info.managementTo)
	}
	return nil
}

func (m *model) installedLabel(mask int) string {
	if mask == 0 {
		return "no installed updates"
	}
	names := make([]string, 0, len(m.required))
	for _, regionIndex := range m.required {
		region := m.regions[regionIndex]
		if mask&int(region.bit) != 0 {
			names = append(names, region.id)
		}
	}
	return "installed={" + strings.Join(names, ",") + "}"
}

func trailingZeroBit(value int) int {
	bit := 0
	for value&1 == 0 {
		value >>= 1
		bit++
	}
	return bit
}

func reverseInts(values []int) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}
