package migrationaudit

import (
	"reflect"
	"testing"
)

func TestSaltDomainIsolationAndEraseMemory(t *testing.T) {
	problem := Problem{
		TargetCost: 10,
		Digests: []Digest{
			{ID: "low-a", SaltDomain: "salt-a", Family: "fam", Cost: 1, Accounts: []AccountID{"acct-a"}},
			{ID: "high-b", SaltDomain: "salt-b", Family: "fam", Cost: 12, Accounts: []AccountID{"acct-b"}},
		},
		Slots: []Slot{
			{ID: "slot-a", Replica: "r1", InitialDigest: "low-a"},
			{ID: "slot-b", Replica: "r2", InitialDigest: "high-b"},
		},
		Families: []FamilyMembership{
			{Family: "fam", SaltDomain: "salt-a", Accounts: []AccountID{"acct-a"}},
			{Family: "fam", SaltDomain: "salt-b", Accounts: []AccountID{"acct-b"}},
		},
		ObserverGroups: []ObserverGroup{{ID: "obs", InitialReplicas: []ReplicaID{"r1"}}},
		ValidatorSlots: []SlotID{"slot-a", "slot-b"},
		Actions: []Action{
			{ID: 1, Kind: ActionErase, Digest: "low-a", Target: "slot-a"},
			{ID: 2, Kind: ActionOverwrite, Digest: "high-b", Target: "slot-b"},
		},
	}
	result, err := Solve(problem)
	if err != nil {
		t.Fatalf("Solve returned error: %v", err)
	}
	if !result.Feasible || result.Peak != 1 || result.TotalPrefixExposure != 2 {
		t.Fatalf("unexpected result: feasible=%v peak=%d sum=%d", result.Feasible, result.Peak, result.TotalPrefixExposure)
	}
	prefix := result.Prefixes[len(result.Prefixes)-1]
	exposure := prefix.GroupExposures[0]
	if !reflect.DeepEqual(exposure.ExposedAccounts, []AccountID{"acct-a"}) {
		t.Fatalf("salt isolation or memory failed: %#v", exposure.ExposedAccounts)
	}
	if err := VerifyResult(problem, result); err != nil {
		t.Fatalf("VerifyResult failed: %v", err)
	}
}

func TestTwoLowCostDigestsSameAccountCountOnce(t *testing.T) {
	problem := Problem{
		TargetCost: 10,
		Digests: []Digest{
			{ID: "low-1", SaltDomain: "salt", Family: "fam", Cost: 2, Accounts: []AccountID{"acct"}},
			{ID: "low-2", SaltDomain: "salt", Family: "fam", Cost: 3, Accounts: []AccountID{"acct"}},
			{ID: "high", SaltDomain: "salt", Family: "fam", Cost: 12, Accounts: []AccountID{"acct"}},
		},
		Slots: []Slot{
			{ID: "slot-1", Replica: "r1", InitialDigest: "low-1"},
			{ID: "slot-2", Replica: "r1", InitialDigest: "low-2"},
		},
		Families:       []FamilyMembership{{Family: "fam", SaltDomain: "salt", Accounts: []AccountID{"acct"}}},
		ObserverGroups: []ObserverGroup{{ID: "obs", InitialReplicas: []ReplicaID{"r1"}}},
		ValidatorSlots: []SlotID{"slot-2"},
		Actions: []Action{
			{ID: 1, Kind: ActionErase, Digest: "low-1", Target: "slot-1"},
			{ID: 2, Kind: ActionOverwrite, Digest: "high", Target: "slot-2"},
		},
	}
	result, err := Solve(problem)
	if err != nil {
		t.Fatalf("Solve returned error: %v", err)
	}
	if result.Peak != 1 || result.TotalPrefixExposure != 2 {
		t.Fatalf("unexpected peak/sum: %d/%d", result.Peak, result.TotalPrefixExposure)
	}
	finalGroup := result.Prefixes[1].GroupExposures[0]
	if len(finalGroup.ExposedAccounts) != 1 || finalGroup.ExposedAccounts[0] != "acct" {
		t.Fatalf("duplicate account not deduplicated: %#v", finalGroup.ExposedAccounts)
	}
	if len(finalGroup.LowCostObservedDigests) != 2 {
		t.Fatalf("both observed low-cost digests should be retained: %#v", finalGroup.LowCostObservedDigests)
	}
	if err := VerifyResult(problem, result); err != nil {
		t.Fatalf("VerifyResult failed: %v", err)
	}
}

func TestSameExecutedSetDifferentObservedHistory(t *testing.T) {
	problem := Problem{
		TargetCost: 10,
		Digests: []Digest{
			{ID: "low", SaltDomain: "salt", Family: "fam", Cost: 1, Accounts: []AccountID{"acct"}},
		},
		Slots: []Slot{
			{ID: "hidden", Replica: "r1", InitialDigest: "low"},
			{ID: "visible", Replica: "r2", InitialDigest: ""},
			{ID: "offline", Replica: "r3", InitialDigest: ""},
		},
		Families:       []FamilyMembership{{Family: "fam", SaltDomain: "salt", Accounts: []AccountID{"acct"}}},
		ObserverGroups: []ObserverGroup{{ID: "obs", InitialReplicas: []ReplicaID{"r2"}}},
		ValidatorSlots: []SlotID{"visible", "hidden", "offline"},
		Actions: []Action{
			{ID: 1, Kind: ActionMove, Digest: "low", Source: "hidden", Target: "visible"},
			{ID: 2, Kind: ActionChangePolicy, Group: "obs", Replicas: []ReplicaID{"r3"}},
		},
	}
	indexed, err := prepareProblem(problem)
	if err != nil {
		t.Fatalf("prepareProblem returned error: %v", err)
	}
	initial := indexed.initialState()
	moveThenNarrow := indexed.applyAction(indexed.applyAction(initial, 0), 1)
	narrowThenMove := indexed.applyAction(indexed.applyAction(initial, 1), 0)

	moveExposure := indexed.exposureAt(moveThenNarrow)[0].exposedAccounts.count()
	narrowExposure := indexed.exposureAt(narrowThenMove)[0].exposedAccounts.count()
	if moveExposure != 1 || narrowExposure != 0 {
		t.Fatalf("unexpected history exposures: move-first=%d narrow-first=%d", moveExposure, narrowExposure)
	}
	if moveThenNarrow.observed[0][0].count() != 1 || narrowThenMove.observed[0][0].count() != 0 {
		t.Fatalf("unexpected retained histories: move-first=%d narrow-first=%d", moveThenNarrow.observed[0][0].count(), narrowThenMove.observed[0][0].count())
	}
	if moveThenNarrow.key() == narrowThenMove.key() {
		t.Fatal("same executed set with different observed history must produce different state keys")
	}
}

func TestPolicyNarrowingRetainsTransientRead(t *testing.T) {
	problem := Problem{
		TargetCost: 10,
		Digests: []Digest{
			{ID: "low", SaltDomain: "salt", Family: "fam", Cost: 1, Accounts: []AccountID{"acct"}},
		},
		Slots: []Slot{
			{ID: "holding", Replica: "r1", InitialDigest: "low"},
			{ID: "offline", Replica: "r2", InitialDigest: ""},
		},
		Families:       []FamilyMembership{{Family: "fam", SaltDomain: "salt", Accounts: []AccountID{"acct"}}},
		ObserverGroups: []ObserverGroup{{ID: "obs", InitialReplicas: []ReplicaID{"r1"}}},
		ValidatorSlots: []SlotID{"holding", "offline"},
		Actions: []Action{
			{ID: 1, Kind: ActionChangePolicy, Group: "obs", Replicas: []ReplicaID{"r2"}},
		},
	}
	result, err := Solve(problem)
	if err != nil {
		t.Fatalf("Solve returned error: %v", err)
	}
	group := result.Prefixes[0].GroupExposures[0]
	if !reflect.DeepEqual(group.ExposedAccounts, []AccountID{"acct"}) {
		t.Fatalf("transient low-cost read was not retained: %#v", group.ExposedAccounts)
	}
	if !reflect.DeepEqual(group.LowCostObservedDigests, []DigestID{"low"}) {
		t.Fatalf("unexpected retained digest: %#v", group.LowCostObservedDigests)
	}
	if err := VerifyResult(problem, result); err != nil {
		t.Fatalf("VerifyResult failed: %v", err)
	}
}

func TestOptimalOrderMatchesFullPermutation(t *testing.T) {
	problem := Problem{
		TargetCost: 10,
		Digests: []Digest{
			{ID: "low-a", SaltDomain: "salt", Family: "fam-a", Cost: 1, Accounts: []AccountID{"a"}},
			{ID: "low-b", SaltDomain: "salt", Family: "fam-b", Cost: 2, Accounts: []AccountID{"b"}},
			{ID: "high-a", SaltDomain: "salt", Family: "fam-a", Cost: 12, Accounts: []AccountID{"a"}},
			{ID: "high-b", SaltDomain: "salt", Family: "fam-b", Cost: 12, Accounts: []AccountID{"b"}},
		},
		Slots: []Slot{
			{ID: "a", Replica: "r1", InitialDigest: "low-a"},
			{ID: "b", Replica: "r1", InitialDigest: "low-b"},
		},
		Families: []FamilyMembership{
			{Family: "fam-a", SaltDomain: "salt", Accounts: []AccountID{"a"}},
			{Family: "fam-b", SaltDomain: "salt", Accounts: []AccountID{"b"}},
		},
		ObserverGroups: []ObserverGroup{{ID: "obs", InitialReplicas: []ReplicaID{"r1"}}},
		ValidatorSlots: []SlotID{"a", "b"},
		Actions: []Action{
			{ID: 1, Kind: ActionOverwrite, Digest: "high-a", Target: "a"},
			{ID: 2, Kind: ActionOverwrite, Digest: "high-b", Target: "b"},
		},
	}
	result, err := Solve(problem)
	if err != nil {
		t.Fatalf("Solve returned error: %v", err)
	}
	naive := naiveOptimal(t, problem)
	if !reflect.DeepEqual(result.Order, naive.order) {
		t.Fatalf("order = %v, want %v", result.Order, naive.order)
	}
	if result.Peak != naive.peak || result.TotalPrefixExposure != naive.sum {
		t.Fatalf("peak/sum = %d/%d, want %d/%d", result.Peak, result.TotalPrefixExposure, naive.peak, naive.sum)
	}
	if err := VerifyResult(problem, result); err != nil {
		t.Fatalf("VerifyResult failed: %v", err)
	}
}

func TestActionIDBreaksExposureTie(t *testing.T) {
	problem := Problem{
		TargetCost: 10,
		Digests: []Digest{
			{ID: "low-a", SaltDomain: "salt", Family: "fam-a", Cost: 1, Accounts: []AccountID{"a"}},
			{ID: "low-b", SaltDomain: "salt", Family: "fam-b", Cost: 1, Accounts: []AccountID{"b"}},
			{ID: "high-a", SaltDomain: "salt", Family: "fam-a", Cost: 12, Accounts: []AccountID{"a"}},
			{ID: "high-b", SaltDomain: "salt", Family: "fam-b", Cost: 12, Accounts: []AccountID{"b"}},
		},
		Slots: []Slot{
			{ID: "a", Replica: "r1", InitialDigest: "low-a"},
			{ID: "b", Replica: "r1", InitialDigest: "low-b"},
		},
		Families: []FamilyMembership{
			{Family: "fam-a", SaltDomain: "salt", Accounts: []AccountID{"a"}},
			{Family: "fam-b", SaltDomain: "salt", Accounts: []AccountID{"b"}},
		},
		ObserverGroups: []ObserverGroup{{ID: "obs", InitialReplicas: []ReplicaID{"r1"}}},
		ValidatorSlots: []SlotID{"a", "b"},
		Actions: []Action{
			{ID: 7, Kind: ActionOverwrite, Digest: "high-a", Target: "a"},
			{ID: 3, Kind: ActionOverwrite, Digest: "high-b", Target: "b"},
		},
	}
	result, err := Solve(problem)
	if err != nil {
		t.Fatalf("Solve returned error: %v", err)
	}
	if !reflect.DeepEqual(result.Order, []int{3, 7}) {
		t.Fatalf("tie-breaking order = %v, want [3 7]", result.Order)
	}
	naive := naiveOptimal(t, problem)
	if !reflect.DeepEqual(result.Order, naive.order) {
		t.Fatalf("order = %v, naive = %v", result.Order, naive.order)
	}
	if err := VerifyResult(problem, result); err != nil {
		t.Fatalf("VerifyResult failed: %v", err)
	}
}

type naiveOptimum struct {
	order []int
	peak  int
	sum   int
}

func naiveOptimal(t *testing.T, problem Problem) naiveOptimum {
	t.Helper()
	indexed, err := prepareProblem(problem)
	if err != nil {
		t.Fatal(err)
	}
	initial := indexed.initialState()
	best := naiveOptimum{}
	var search func(state *auditState, order []int, peak int, sum int)
	search = func(state *auditState, order []int, peak int, sum int) {
		if state.executed.count() == len(indexed.problem.Actions) {
			current := naiveOptimum{order: append([]int(nil), order...), peak: peak, sum: sum}
			if betterNaive(current, best) {
				best = current
			}
			return
		}
		for _, actionIndex := range indexed.legalActions(state) {
			next := indexed.applyAction(state, actionIndex)
			if !indexed.validatorAccepts(next) {
				continue
			}
			exposures := indexed.exposureAt(next)
			worst, _ := indexed.worstExposure(exposures)
			search(next, append(order, indexed.problem.Actions[actionIndex].ID), maxInt(peak, worst), sum+worst)
		}
	}
	initialExposures := indexed.exposureAt(initial)
	initialPeak, _ := indexed.worstExposure(initialExposures)
	search(initial, nil, initialPeak, 0)
	if best.order == nil {
		t.Fatal("naive search found no feasible order")
	}
	return best
}

func betterNaive(candidate naiveOptimum, best naiveOptimum) bool {
	if best.order == nil {
		return true
	}
	if candidate.peak != best.peak {
		return candidate.peak < best.peak
	}
	if candidate.sum != best.sum {
		return candidate.sum < best.sum
	}
	for index := range candidate.order {
		if candidate.order[index] != best.order[index] {
			return candidate.order[index] < best.order[index]
		}
	}
	return false
}

func maxInt(left int, right int) int {
	if left > right {
		return left
	}
	return right
}
