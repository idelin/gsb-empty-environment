import random
import unittest

from invcheck import expr as exprmod
from invcheck.checker import InvariantChecker
from invcheck.model import parse_model
from invcheck.refcheck import enumerate_check, compare

from tests.helpers import MOD3, MOD4, LATCH, random_ast, random_model


class TestReferenceCrossCheck(unittest.TestCase):
    def test_random_models_symbolic_matches_enumeration(self):
        rng = random.Random(2024)
        for trial in range(40):
            n_vars = rng.randint(2, 4)
            model = random_model(rng, n_vars)
            inv_ast = random_ast(rng, model.variables, 2)
            res = InvariantChecker(model).check(inv_ast)
            enum = enumerate_check(model, inv_ast)
            self.assertEqual(enum["status"], "decided")
            mismatches = compare(res, enum)
            self.assertEqual(mismatches, [],
                             msg=f"trial {trial}: {mismatches}")

    def test_random_models_order_invariance_against_enumeration(self):
        rng = random.Random(99)
        for trial in range(20):
            model = random_model(rng, 3)
            inv_ast = random_ast(rng, model.variables, 2)
            enum = enumerate_check(model, inv_ast)
            for style in ("interleave", "block"):
                perm = model.variables[:]
                rng.shuffle(perm)
                res = InvariantChecker(
                    model, order_style=style, permutation=perm
                ).check(inv_ast)
                self.assertEqual(compare(res, enum), [], msg=f"trial {trial}")

    def test_example_models_match_enumeration(self):
        for model_dict, inv_text in [
            (MOD4, "!(a & b)"),
            (MOD3, "!(a & b)"),
            (MOD3, "a"),
            (LATCH, "!a"),
            (LATCH, "!a & !b"),
            (dict(MOD3, init="false"), "!(a & b)"),
        ]:
            model = parse_model(model_dict)
            inv_ast = exprmod.parse(inv_text)
            res = InvariantChecker(model).check(inv_ast)
            enum = enumerate_check(model, inv_ast)
            self.assertEqual(compare(res, enum), [], msg=inv_text)

    def test_enumeration_limit_stays_undecided(self):
        model = parse_model(MOD4)
        enum = enumerate_check(model, exprmod.parse("true"), max_states=2)
        self.assertEqual(enum["status"], "undecided")


if __name__ == "__main__":
    unittest.main()
