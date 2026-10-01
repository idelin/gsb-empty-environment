import itertools
import os
import unittest

from invcheck import (FAIL_BASE, FAIL_STEP, PASS, UNKNOWN, InvariantChecker,
                      load_model)

EXAMPLES = os.path.join(os.path.dirname(os.path.dirname(
    os.path.abspath(__file__))), "examples")


def load_example(name):
    return load_model(os.path.join(EXAMPLES, name))


class TestCheckerVerdicts(unittest.TestCase):
    def test_mod3_inductive(self):
        res = InvariantChecker(load_example("mod3_counter.json")).check()
        self.assertEqual(res.verdict, PASS)
        self.assertTrue(res.base_ok)
        self.assertTrue(res.step_ok)
        self.assertFalse(res.init_empty)
        self.assertTrue(res.reachable_complete)
        self.assertEqual(res.reachable_states, 3)
        self.assertTrue(res.holds_on_reachable)

    def test_mod5_step_fail_unreachable_source(self):
        res = InvariantChecker(load_example("mod5_unreachable_step.json")).check()
        self.assertEqual(res.verdict, FAIL_STEP)
        self.assertTrue(res.base_ok)
        self.assertIsNotNone(res.step_cex)
        # the only bad transitions start at unreachable states 6 and 7
        self.assertEqual(res.step_cex.reachability, "unreachable")
        self.assertTrue(res.holds_on_reachable)
        self.assertTrue(any("not inductive" in n for n in res.notes))
        # replay the cube: source must satisfy invariant, successor must not
        model = load_example("mod5_unreachable_step.json")
        src = {v: res.step_cex.state.get(v, False) for v in model.variables}
        dst = {v: res.step_cex.next_state.get(v, False) for v in model.variables}
        self.assertTrue(model.invariant_holds(src))
        self.assertTrue(model.transition_holds(src, dst))
        self.assertFalse(model.invariant_holds(dst))

    def test_mod3_bad_invariant_reachable_failure(self):
        res = InvariantChecker(load_example("mod3_bad_invariant.json")).check()
        self.assertEqual(res.verdict, FAIL_STEP)
        self.assertEqual(res.step_cex.reachability, "reachable")
        self.assertFalse(res.holds_on_reachable)

    def test_base_fail(self):
        res = InvariantChecker(load_example("base_fail.json")).check()
        self.assertEqual(res.verdict, FAIL_BASE)
        self.assertIsNotNone(res.base_cex)
        model = load_example("base_fail.json")
        state = {v: res.base_cex.get(v, False) for v in model.variables}
        self.assertTrue(model.init_holds(state))
        self.assertFalse(model.invariant_holds(state))

    def test_empty_init_reported_separately(self):
        res = InvariantChecker(load_example("empty_init.json")).check()
        self.assertTrue(res.init_empty)
        self.assertTrue(res.base_ok)  # vacuous
        self.assertTrue(any("EMPTY" in n for n in res.notes))
        self.assertEqual(res.reachable_states, 0)

    def test_next_functions_form(self):
        res = InvariantChecker(load_example("next_functions.json")).check()
        self.assertEqual(res.verdict, PASS)
        self.assertEqual(res.reachable_states, 3)

    def test_relation_form(self):
        model = {
            "name": "rel",
            "variables": ["x"],
            "init": "~x",
            "invariant": "~x",
            "transition": {"relation": "~x'"},
        }
        res = InvariantChecker(model).check()
        self.assertEqual(res.verdict, PASS)

    def test_zero_variables(self):
        model = {
            "name": "trivial",
            "variables": [],
            "init": "1",
            "invariant": "1",
            "transition": {"edges": [[0, 0]]},
        }
        res = InvariantChecker(model).check()
        self.assertEqual(res.verdict, PASS)

    def test_unknown_on_tight_node_budget(self):
        res = InvariantChecker(load_example("mod5_unreachable_step.json"),
                               max_nodes=4).check()
        self.assertEqual(res.verdict, UNKNOWN)
        self.assertIsNotNone(res.unknown_reason)

    def test_unknown_on_tight_op_budget(self):
        res = InvariantChecker(load_example("mod5_unreachable_step.json"),
                               max_ops=10).check()
        self.assertEqual(res.verdict, UNKNOWN)

    def test_reachability_iter_cap_keeps_undecided(self):
        checker = InvariantChecker(load_example("mod5_unreachable_step.json"),
                                   max_reach_iter=1)
        res = checker.check()
        # base/step are still decided symbolically...
        self.assertEqual(res.verdict, FAIL_STEP)
        # ...but reachability facts stay undecided
        self.assertFalse(res.reachable_complete)
        self.assertIsNone(res.holds_on_reachable)
        self.assertEqual(res.step_cex.reachability, "unknown")
        self.assertTrue(any("did not converge" in n for n in res.notes))


class TestOrderIndependence(unittest.TestCase):
    def _orders(self, model):
        base = model.all_variables()
        orders = [base, list(reversed(base))]
        # a few deterministic permutations
        orders.append(sorted(base))
        orders.append(sorted(base, reverse=True))
        odd = base[::2] + base[1::2]
        orders.append(odd)
        return orders

    def test_verdicts_identical_across_orders(self):
        for name in ("mod3_counter.json", "mod5_unreachable_step.json",
                     "mod3_bad_invariant.json", "base_fail.json",
                     "empty_init.json", "next_functions.json"):
            model = load_example(name)
            verdicts = []
            details = []
            for order in self._orders(model):
                res = InvariantChecker(model, variable_order=order).check()
                verdicts.append(res.verdict)
                details.append((res.base_ok, res.step_ok, res.init_empty,
                                res.holds_on_reachable, res.reachable_states,
                                res.step_cex.reachability if res.step_cex else None))
            self.assertEqual(len(set(verdicts)), 1, name)
            self.assertEqual(len(set(details)), 1, name)


class TestMinimality(unittest.TestCase):
    def test_step_cex_is_irredundant(self):
        model = load_example("mod5_unreachable_step.json")
        checker = InvariantChecker(model)
        res = checker.check()
        mgr = checker.mgr
        from invcheck import expr as expr_mod
        inv = expr_mod.to_bdd(mgr, expr_mod.parse(model.invariant))
        inv_next = mgr.rename(inv, checker._cur_to_next)
        bad = mgr.and_(mgr.and_(inv, checker.trans_bdd), mgr.negate(inv_next))
        cube = dict(res.step_cex.state)
        cube.update({k + "'": v for k, v in res.step_cex.next_state.items()})
        self.assertTrue(mgr.implies_true(mgr.cube(cube), bad))
        for name in cube:
            trial = {k: v for k, v in cube.items() if k != name}
            self.assertFalse(mgr.implies_true(mgr.cube(trial), bad),
                             "literal %s is redundant" % name)


if __name__ == "__main__":
    unittest.main()
