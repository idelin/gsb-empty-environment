import itertools
import random
import unittest
from fractions import Fraction

from project_select import (
    Status, CycleError, compile_projects, check_feasibility, optimize,
    undecide, _initial_state, _mask_to_projects,
)


def brute_force(projects, budget=None, required=(), mutex_groups=()):
    """Reference solver: iterate all subsets."""
    ids = list(projects)
    req = set(required)

    def closure(chosen):
        chosen = set(chosen)
        changed = True
        while changed:
            changed = False
            for pid in list(chosen):
                for dep in projects[pid].get("deps", ()):
                    if dep not in chosen:
                        chosen.add(dep)
                        changed = True
        return chosen

    feasible_sets = []
    for r in range(len(ids) + 1):
        for subset in itertools.combinations(ids, r):
            closed = closure(subset)
            if set(subset) != closed:
                continue
            for group in mutex_groups:
                if sum(1 for m in group if m in closed) > 1:
                    break
            else:
                feasible_sets.append(frozenset(closed))
    feasible_sets = set(feasible_sets)
    required_feasible = [s for s in feasible_sets if req <= s]

    def cost(s):
        return sum((projects[p]["cost"] for p in s), 0)

    def value(s):
        return sum((projects[p]["value"] for p in s), 0)

    if budget is None:
        return required_feasible, cost, value

    affordable = [s for s in required_feasible if cost(s) <= budget]
    return feasible_sets, required_feasible, affordable, cost, value


class SharedDependencyTests(unittest.TestCase):
    PROJECTS = {
        "platform": {"cost": 10, "value": -2, "deps": []},
        "app_a": {"cost": 3, "value": 8, "deps": ["platform"]},
        "app_b": {"cost": 4, "value": 6, "deps": ["platform"]},
        "standalone": {"cost": 5, "value": 4, "deps": []},
    }

    def test_shared_dependency_paid_once(self):
        r = optimize(self.PROJECTS, budget=100)
        self.assertEqual(r.status, Status.OPTIMAL)
        self.assertEqual(set(r.projects),
                         {"platform", "app_a", "app_b", "standalone"})
        self.assertEqual(r.cost, 22)  # 10 + 3 + 4 + 5, platform only once
        self.assertEqual(r.value, 16)

    def test_child_without_parent_never_chosen(self):
        r = optimize(self.PROJECTS, budget=100)
        self.assertIn("platform", r.projects)

    def test_negative_value_dependency_pulled_in(self):
        r = optimize({"base": {"cost": 1, "value": -10},
                      "killer": {"cost": 1, "value": 20, "deps": ["base"]}},
                     budget=100)
        self.assertEqual(set(r.projects), {"base", "killer"})
        self.assertEqual(r.value, 10)

    def test_budget_excludes_all(self):
        r = optimize(self.PROJECTS, budget=2)
        self.assertEqual(r.status, Status.OPTIMAL)
        self.assertEqual(r.projects, ())  # empty choice is the optimum

    def test_required_closure_over_budget(self):
        r = optimize(self.PROJECTS, budget=2, required=["app_a"])
        self.assertEqual(r.status, Status.OVER_BUDGET)
        self.assertIn("app_a", r.projects)

    def test_small_budget_picks_best_affordable(self):
        r = optimize(self.PROJECTS, budget=5)
        self.assertEqual(r.status, Status.OPTIMAL)
        self.assertEqual(set(r.projects), {"standalone"})

    def test_tight_budget_forces_choice(self):
        r = optimize(self.PROJECTS, budget=13)
        self.assertEqual(set(r.projects), {"platform", "app_a"})
        self.assertEqual(r.value, 6)


class MutexTests(unittest.TestCase):
    PROJECTS = {
        "platform": {"cost": 10, "value": -2, "deps": []},
        "app_a": {"cost": 3, "value": 8, "deps": ["platform"]},
        "app_b": {"cost": 4, "value": 6, "deps": ["platform"]},
    }

    def test_mutex_selects_only_one(self):
        r = optimize(self.PROJECTS, budget=100,
                     mutex_groups=[("app_a", "app_b")])
        self.assertEqual(set(r.projects), {"platform", "app_a"})

    def test_mutex_via_dependency_makes_required_infeasible(self):
        f = check_feasibility(
            self.PROJECTS, required=["app_a"],
            mutex_groups=[("platform", "app_a")])
        self.assertFalse(f.feasible)
        self.assertEqual(f.status, Status.INFEASIBLE)

    def test_mutex_required_both_infeasible(self):
        f = check_feasibility(
            self.PROJECTS, required=["app_a", "app_b"],
            mutex_groups=[("app_a", "app_b")])
        self.assertEqual(f.status, Status.INFEASIBLE)

    def test_member_in_two_groups_rejected(self):
        with self.assertRaises(ValueError):
            compile_projects(
                self.PROJECTS, mutex_groups=[("app_a", "app_b"),
                                             ("app_a", "platform")])

    def test_no_nonempty_choice_returns_empty_optimum(self):
        projs = {"a": {"cost": 1, "value": 5, "deps": ["b"]},
                 "b": {"cost": 1, "value": 5, "deps": ["a"]}}
        r = optimize(projs, budget=100, mutex_groups=[("a", "b")],
                     cycle_policy="group")
        self.assertEqual(r.status, Status.OPTIMAL)
        self.assertEqual(r.projects, ())

    def test_mutex_pair_can_still_pick_independent_member(self):
        projs = {"a": {"cost": 1, "value": 5, "deps": ["b"]},
                 "b": {"cost": 1, "value": 5, "deps": []}}
        r = optimize(projs, budget=100, mutex_groups=[("a", "b")])
        self.assertEqual(set(r.projects), {"b"})

    def test_structural_infeasibility_inside_cycle_group(self):
        projs = {"a": {"cost": 1, "value": 5, "deps": ["b"]},
                 "b": {"cost": 1, "value": 5, "deps": ["a"]}}
        with self.assertRaises(CycleError):
            compile_projects(projs, cycle_policy="reject")
        r = optimize(projs, budget=100, required=["a"],
                     mutex_groups=[("a", "b")], cycle_policy="group")
        self.assertEqual(r.status, Status.INFEASIBLE)


class CycleTests(unittest.TestCase):
    CYCLIC = {
        "x": {"cost": 2, "value": 1, "deps": ["y"]},
        "y": {"cost": 3, "value": 7, "deps": ["x"]},
        "z": {"cost": 1, "value": 2, "deps": ["x"]},
    }

    def test_group_policy_takes_cycle_as_whole(self):
        r = optimize(self.CYCLIC, budget=100, cycle_policy="group")
        self.assertEqual(set(r.projects), {"x", "y", "z"})
        self.assertEqual(r.cost, 6)
        self.assertEqual(r.value, 10)

    def test_group_policy_empty_optimum_at_zero_budget(self):
        r = optimize(self.CYCLIC, budget=0, cycle_policy="group")
        self.assertEqual(r.status, Status.OPTIMAL)
        self.assertEqual(r.projects, ())

    def test_group_cycle_not_partly_selectable(self):
        r = optimize(self.CYCLIC, budget=4, cycle_policy="group")
        self.assertEqual(r.status, Status.OPTIMAL)
        self.assertEqual(r.projects, ())

    def test_required_cycle_over_budget(self):
        r = optimize(self.CYCLIC, budget=4, required=["z"],
                     cycle_policy="group")
        self.assertEqual(r.status, Status.OVER_BUDGET)
        self.assertEqual(set(r.projects), {"x", "y", "z"})

    def test_reject_policy_raises(self):
        with self.assertRaises(CycleError):
            compile_projects(self.CYCLIC, cycle_policy="reject")

    def test_check_feasibility_reject_policy_raises(self):
        with self.assertRaises(CycleError):
            check_feasibility(self.CYCLIC, cycle_policy="reject")

    def test_self_dependency_rejected(self):
        with self.assertRaises(ValueError):
            compile_projects({"a": {"cost": 1, "value": 1, "deps": ["a"]}})


class RationalTests(unittest.TestCase):
    def test_fraction_strings_exact(self):
        projs = {"base": {"cost": "1/3", "value": 0},
                 "feat": {"cost": "2/3", "value": "5/2", "deps": ["base"]}}
        r = optimize(projs, budget="1")
        self.assertEqual(r.cost, Fraction(1, 1))
        self.assertEqual(r.value, Fraction(5, 2))

    def test_fraction_objects(self):
        projs = {"a": {"cost": Fraction(1, 10), "value": Fraction(1, 3)}}
        r = optimize(projs, budget=Fraction(1, 5))
        self.assertEqual(r.value, Fraction(1, 3))

    def test_fraction_budget_too_small(self):
        projs = {"a": {"cost": "1/3", "value": 10}}
        r = optimize(projs, budget="1/4")
        self.assertEqual(r.status, Status.OPTIMAL)
        self.assertEqual(r.projects, ())

    def test_fraction_required_over_budget(self):
        projs = {"a": {"cost": "1/3", "value": 10}}
        r = optimize(projs, budget="1/4", required=["a"])
        self.assertEqual(r.status, Status.OVER_BUDGET)

    def test_float_rejected(self):
        with self.assertRaises(TypeError):
            optimize({"a": {"cost": 0.5, "value": 1}}, budget=1)


class ValidationTests(unittest.TestCase):
    def test_unknown_dependency(self):
        with self.assertRaises(KeyError):
            compile_projects({"a": {"cost": 1, "value": 1, "deps": ["ghost"]}})

    def test_unknown_required(self):
        with self.assertRaises(KeyError):
            optimize({"a": {"cost": 1, "value": 1}}, required=["ghost"])

    def test_negative_cost_rejected(self):
        with self.assertRaises(ValueError):
            compile_projects({"a": {"cost": -1, "value": 1}})

    def test_duplicate_dependency_rejected(self):
        with self.assertRaises(ValueError):
            compile_projects({"a": {"cost": 1, "value": 1, "deps": []},
                              "b": {"cost": 1, "value": 1,
                                    "deps": ["a", "a"]}})

    def test_negative_budget_rejected(self):
        with self.assertRaises(ValueError):
            optimize({"a": {"cost": 1, "value": 1}}, budget=-1)


class FrontierTests(unittest.TestCase):
    def test_undecided_returns_pareto_frontier(self):
        projs = {"a": {"cost": 2, "value": 3},
                 "b": {"cost": 3, "value": 5},
                 "c": {"cost": 1, "value": 1}}
        r = undecide(projs)
        self.assertEqual(r.status, Status.UNDECIDED)
        costs = [p.cost for p in r.frontier]
        self.assertEqual(costs, [0, 1, 2, 3, 4, 5, 6])
        values = [p.value for p in r.frontier]
        self.assertEqual(values, [0, 1, 3, 5, 6, 8, 9])

    def test_frontier_contains_empty_choice(self):
        r = undecide({"a": {"cost": 1, "value": 1}})
        self.assertEqual(r.frontier[0].projects, ())

    def test_dominated_portfolio_absent(self):
        projs = {"cheap_good": {"cost": 1, "value": 10},
                 "expensive_bad": {"cost": 5, "value": -1}}
        r = undecide(projs)
        costs = [p.cost for p in r.frontier]
        self.assertNotIn(6, costs)  # taking both is dominated by cheap_good
        self.assertEqual(len(r.frontier), 2)


class BruteForceCrossCheck(unittest.TestCase):
    def _random_instance(self, rng, n):
        ids = [f"p{i}" for i in range(n)]
        projs = {}
        for i, pid in enumerate(ids):
            deps = tuple(ids[j] for j in range(i)
                         if rng.random() < 0.35)
            projs[pid] = {"cost": rng.randint(0, 6),
                          "value": rng.randint(-4, 10),
                          "deps": deps}
        groups = []
        pool = ids[:]
        rng.shuffle(pool)
        i = 0
        while i + 1 < len(pool) and rng.random() < 0.3:
            groups.append((pool[i], pool[i + 1]))
            i += 2
        return projs, groups

    def test_optimize_matches_brute_force(self):
        rng = random.Random(20260926)
        for seed_trial in range(40):
            n = rng.randint(1, 7)
            projs, groups = self._random_instance(rng, n)
            required = tuple(p for p in projs
                             if rng.random() < 0.15)
            for policy in ("group",):
                try:
                    model = compile_projects(projs, required, groups, policy)
                except KeyError:
                    continue
                state = _initial_state(model)
                if state is None:
                    continue
                for budget in range(0, 20):
                    r = optimize(projs, budget=budget, required=required,
                                 mutex_groups=groups, cycle_policy=policy)
                    feas, req_feas, affordable, cost, value = brute_force(
                        projs, budget, required, groups)
                    if not req_feas:
                        self.assertEqual(r.status, Status.INFEASIBLE,
                                         (projs, budget, required, groups))
                        continue
                    if not affordable:
                        if required:
                            self.assertEqual(r.status, Status.OVER_BUDGET,
                                             (projs, budget, required, groups))
                        else:
                            self.assertEqual(r.status, Status.OPTIMAL,
                                             (projs, budget, required, groups))
                            self.assertEqual(r.projects, ())
                        continue
                    best = max(affordable,
                               key=lambda s: (value(s), -cost(s)))
                    self.assertEqual(r.status, Status.OPTIMAL)
                    self.assertEqual(frozenset(r.projects), best)
                    self.assertEqual(r.cost, cost(best))
                    self.assertEqual(r.value, value(best))

    def test_frontier_matches_brute_force(self):
        rng = random.Random(777)
        for _ in range(20):
            n = rng.randint(1, 7)
            projs, groups = self._random_instance(rng, n)
            try:
                model = compile_projects(projs, (), groups, "group")
            except KeyError:
                continue
            if _initial_state(model) is None:
                continue
            r = undecide(projs, mutex_groups=groups)
            req_feas, cost, value = brute_force(projs, None, (), groups)
            expected = []
            for s in req_feas:
                dominated = any(
                    other is not s and cost(other) <= cost(s)
                    and value(other) >= value(s)
                    and (cost(other) < cost(s) or value(other) > value(s))
                    for other in req_feas)
                if not dominated:
                    expected.append((cost(s), value(s), frozenset(s)))
            got = [(p.cost, p.value, frozenset(p.projects))
                   for p in r.frontier]
            self.assertEqual(sorted(got), sorted(expected))

    def test_reject_policy_brute_force_acyclic(self):
        rng = random.Random(31)
        for _ in range(15):
            n = rng.randint(1, 6)
            projs, groups = self._random_instance(rng, n)
            ids = list(projs)
            cyclic = False
            for pid in projs:
                for dep in projs[pid]["deps"]:
                    if ids.index(dep) >= ids.index(pid):
                        cyclic = True
            if cyclic:
                with self.assertRaises(CycleError):
                    compile_projects(projs, cycle_policy="reject")
            else:
                compile_projects(projs, cycle_policy="reject")


if __name__ == "__main__":
    unittest.main(verbosity=2)
