import random
import unittest

from invcheck import expr as exprmod
from invcheck.checker import InvariantChecker, check_incremental
from invcheck.model import parse_model, next_states

from tests.helpers import MOD3, MOD4, LATCH


def check(model_dict, inv_text, **kw):
    model = parse_model(model_dict)
    return InvariantChecker(model, **kw).check(exprmod.parse(inv_text), inv_text)


class TestChecker(unittest.TestCase):
    def test_mod4_inductive_failure_reachable(self):
        res = check(MOD4, "!(a & b)")
        self.assertEqual(res.verdict, "inductive_failed")
        self.assertEqual(res.base_status, "pass")
        self.assertEqual(res.inductive_status, "fail")
        s, sp = res.inductive_counterexample
        self.assertEqual((s, sp), ({"a": True, "b": False}, {"a": True, "b": True}))
        self.assertTrue(res.cex_source_reachable)
        self.assertEqual(res.reachable_count, 4)
        self.assertFalse(res.holds_on_reachable)

    def test_mod3_verified(self):
        res = check(MOD3, "!(a & b)")
        self.assertEqual(res.verdict, "verified")
        self.assertEqual(res.base_status, "pass")
        self.assertEqual(res.inductive_status, "pass")
        self.assertEqual(res.reachable_count, 3)
        self.assertTrue(res.holds_on_reachable)

    def test_base_case_failure(self):
        res = check(MOD3, "a")
        self.assertEqual(res.verdict, "base_case_failed")
        self.assertEqual(res.base_status, "fail")
        self.assertEqual(res.base_counterexample, {"a": False, "b": False})

    def test_unreachable_premise_is_explained(self):
        res = check(LATCH, "!a")
        self.assertEqual(res.verdict, "inductive_failed")
        self.assertFalse(res.cex_source_reachable)
        self.assertTrue(res.holds_on_reachable)
        self.assertEqual(res.reachable_count, 1)
        self.assertTrue(any("UNREACHABLE" in n for n in res.notes))
        self.assertTrue(any("does not imply inductiveness" in n for n in res.notes))

    def test_incremental_refinement_to_verified(self):
        model = parse_model(LATCH)
        stages = check_incremental(
            model,
            exprmod.parse("!a"), "!a",
            [(exprmod.parse("!b"), "!b")],
        )
        self.assertEqual([s.verdict for s in stages],
                         ["inductive_failed", "verified"])

    def test_empty_init_reported_separately(self):
        model_dict = dict(MOD3, init="false")
        res = check(model_dict, "!(a & b)")
        self.assertEqual(res.base_status, "empty_init")
        self.assertEqual(res.inductive_status, "pass")
        self.assertEqual(res.verdict, "verified")
        self.assertEqual(res.reachable_count, 0)
        self.assertTrue(any("empty" in n for n in res.notes))

    def test_node_limit_yields_undecided(self):
        res = check(MOD4, "!(a & b)", node_limit=5)
        self.assertEqual(res.verdict, "undecided")
        self.assertEqual(res.base_status, "undecided")
        self.assertTrue(any("resource limit" in n for n in res.notes))

    def test_time_limit_zero_yields_undecided(self):
        res = check(MOD4, "!(a & b)", time_limit=0)
        self.assertEqual(res.verdict, "undecided")

    def test_counterexample_is_replayable_concretely(self):
        model = parse_model(MOD4)
        inv_ast = exprmod.parse("!(a & b)")
        res = InvariantChecker(model).check(inv_ast)
        s, sp = res.inductive_counterexample
        self.assertTrue(exprmod.eval_ast(inv_ast, s))
        self.assertIn(sp, next_states(model, s))
        self.assertFalse(exprmod.eval_ast(inv_ast, sp))

    def test_variable_order_does_not_change_conclusions(self):
        cases = [
            (MOD4, "!(a & b)"),
            (MOD3, "!(a & b)"),
            (MOD3, "a"),
            (LATCH, "!a"),
            (LATCH, "!a & !b"),
        ]
        rng = random.Random(11)
        for model_dict, inv_text in cases:
            model = parse_model(model_dict)
            inv_ast = exprmod.parse(inv_text)
            baseline = InvariantChecker(model).check(inv_ast)
            for style in ("interleave", "block"):
                for _ in range(3):
                    perm = model.variables[:]
                    rng.shuffle(perm)
                    res = InvariantChecker(
                        model, order_style=style, permutation=perm
                    ).check(inv_ast)
                    self.assertEqual(res.verdict, baseline.verdict)
                    self.assertEqual(res.base_status, baseline.base_status)
                    self.assertEqual(res.inductive_status, baseline.inductive_status)
                    self.assertEqual(res.reachable_count, baseline.reachable_count)
                    self.assertEqual(res.holds_on_reachable,
                                     baseline.holds_on_reachable)
                    self.assertEqual(res.cex_source_reachable,
                                     baseline.cex_source_reachable)
                    # witnesses may differ across orders but must stay valid
                    if res.inductive_counterexample is not None:
                        s, sp = res.inductive_counterexample
                        self.assertTrue(exprmod.eval_ast(inv_ast, s))
                        self.assertIn(sp, next_states(model, s))
                        self.assertFalse(exprmod.eval_ast(inv_ast, sp))
                    if res.base_counterexample is not None:
                        s = res.base_counterexample
                        self.assertTrue(exprmod.eval_ast(model.init, s))
                        self.assertFalse(exprmod.eval_ast(inv_ast, s))


if __name__ == "__main__":
    unittest.main()
