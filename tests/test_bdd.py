import random
import unittest

from invcheck.bdd import BDDManager, ResourceExceeded
from invcheck import expr as exprmod

from tests.helpers import VARS, random_ast, all_assignments, lex_key


class TestBDDSemantics(unittest.TestCase):
    def test_random_functions_match_ast_eval(self):
        rng = random.Random(42)
        for _ in range(60):
            order = VARS[:]
            rng.shuffle(order)
            mgr = BDDManager(order)
            ast = random_ast(rng, VARS, 3)
            f = exprmod.build(ast, mgr)
            for asg in all_assignments(VARS):
                self.assertEqual(mgr.eval(f, asg), exprmod.eval_ast(ast, asg))

    def test_order_invariance(self):
        rng = random.Random(7)
        asts = [random_ast(rng, VARS, 3) for _ in range(20)]
        tables = {}
        for order in (VARS, list(reversed(VARS)), ["c", "a", "d", "b"]):
            mgr = BDDManager(order)
            tables[str(order)] = [
                tuple(mgr.eval(exprmod.build(ast, mgr), asg)
                      for asg in all_assignments(VARS))
                for ast in asts
            ]
        self.assertEqual(tables[str(VARS)], tables[str(list(reversed(VARS)))])
        self.assertEqual(tables[str(VARS)], tables[str(["c", "a", "d", "b"])])

    def test_exists(self):
        rng = random.Random(1)
        mgr = BDDManager(VARS)
        for _ in range(40):
            ast = random_ast(rng, VARS, 3)
            f = exprmod.build(ast, mgr)
            qvars = rng.sample(VARS, rng.randint(1, 3))
            g = mgr.exists(f, qvars)
            rest = [v for v in VARS if v not in qvars]
            for asg in all_assignments(rest):
                expected = any(
                    exprmod.eval_ast(ast, {**asg, **q})
                    for q in all_assignments(qvars)
                )
                self.assertEqual(mgr.eval(g, asg), expected)

    def test_rename(self):
        rng = random.Random(2)
        cur = ["a", "b"]
        nxt = ["a@n", "b@n"]
        mgr = BDDManager(["a", "a@n", "b", "b@n"])
        mapping = dict(zip(cur, nxt))
        for _ in range(30):
            ast = random_ast(rng, cur, 3)
            f = exprmod.build(ast, mgr)
            g = mgr.rename(f, mapping)
            for asg in all_assignments(cur):
                renamed = {mapping[v]: val for v, val in asg.items()}
                self.assertEqual(mgr.eval(g, renamed), mgr.eval(f, asg))

    def test_restrict(self):
        rng = random.Random(3)
        mgr = BDDManager(VARS)
        for _ in range(30):
            ast = random_ast(rng, VARS, 3)
            f = exprmod.build(ast, mgr)
            fixed = {v: rng.random() < 0.5 for v in rng.sample(VARS, 2)}
            g = mgr.restrict(f, fixed)
            rest = [v for v in VARS if v not in fixed]
            for asg in all_assignments(rest):
                self.assertEqual(
                    mgr.eval(g, asg), exprmod.eval_ast(ast, {**asg, **fixed})
                )

    def test_sat_count(self):
        rng = random.Random(4)
        mgr = BDDManager(VARS)
        for _ in range(30):
            ast = random_ast(rng, VARS, 3)
            f = exprmod.build(ast, mgr)
            expected = sum(
                1 for asg in all_assignments(VARS) if exprmod.eval_ast(ast, asg)
            )
            self.assertEqual(mgr.sat_count(f), expected)

    def test_pick_sat_is_lexicographically_minimal(self):
        rng = random.Random(5)
        for _ in range(40):
            order = VARS[:]
            rng.shuffle(order)
            mgr = BDDManager(order)
            ast = random_ast(rng, VARS, 3)
            f = exprmod.build(ast, mgr)
            sats = [asg for asg in all_assignments(VARS) if mgr.eval(f, asg)]
            picked = mgr.pick_sat(f)
            if not sats:
                self.assertIsNone(picked)
            else:
                key = lex_key(order)
                self.assertEqual(key(picked), min(key(s) for s in sats))

    def test_node_limit_raises(self):
        mgr = BDDManager(VARS, node_limit=4)
        with self.assertRaises(ResourceExceeded):
            exprmod.build(exprmod.parse("a ^ b ^ c ^ d"), mgr)

    def test_time_limit_zero_raises(self):
        mgr = BDDManager(VARS, time_limit=0)
        with self.assertRaises(ResourceExceeded):
            mgr.var("a")


if __name__ == "__main__":
    unittest.main()
