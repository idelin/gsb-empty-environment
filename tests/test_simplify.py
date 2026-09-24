"""unittest suite for the symbolic DAG partial evaluator.

Cross-check policy: for small finite rational inputs, the simplified
expression and the independent interpreter on the *original* expression must
agree on the value, on undefined-ness, and on the exact error position.
"""

import random
import unittest
from fractions import Fraction

from dag import Factory, canon_key, free_vars, show
from engine import Engine
from interp import UnboundError, UndefinedError, evaluate, run
from parser import parse

VARS = ("x", "y", "z")


def make(text):
    f = Factory()
    return f, parse(f, text)


def rand_expr(f, rng, depth):
    if depth <= 0 or rng.random() < 0.35:
        if rng.random() < 0.5:
            return f.const(Fraction(rng.randint(-3, 3), rng.randint(1, 3)))
        return f.var(rng.choice(VARS))
    op = rng.choice(["add", "sub", "mul", "div", "neg", "cond"])
    if op == "neg":
        return f.neg(rand_expr(f, rng, depth - 1))
    if op == "cond":
        return f.cond(rand_expr(f, rng, depth - 1),
                      rand_expr(f, rng, depth - 1),
                      rand_expr(f, rng, depth - 1))
    a = rand_expr(f, rng, depth - 1)
    b = rand_expr(f, rng, depth - 1)
    return getattr(f, op)(a, b)


def rand_env(rng, names=VARS):
    return {n: Fraction(rng.randint(-3, 3), rng.randint(1, 3)) for n in names}


class TestBasicRules(unittest.TestCase):
    def test_exact_rational_arithmetic(self):
        f, root = make("1/3 + 1/6")
        r = Engine(f, root).result()
        self.assertEqual(r.op, "const")
        self.assertEqual(r.value, Fraction(1, 2))  # exact, not 0.5 float

    def test_partial_binding_keeps_unbound_nodes(self):
        f, root = make("x * 0 + y * 1")
        eng = Engine(f, root)
        eng.set("x", 5)
        r = eng.result()
        self.assertEqual(free_vars(r), {"y"})       # y stays symbolic
        self.assertNotIn("x", free_vars(r))
        self.assertEqual(evaluate(r, {"y": Fraction(4)}), Fraction(4))

    def test_repeated_variable_uses_same_binding(self):
        f, root = make("x * x + x")
        eng = Engine(f, root)
        eng.set("x", 3)
        self.assertEqual(eng.result().value, Fraction(12))
        self.assertEqual(evaluate(root, {"x": Fraction(3)}), Fraction(12))

    def test_sub_self_total_var(self):
        f, root = make("x - x")
        r = Engine(f, root).result()
        self.assertEqual((r.op, r.value), ("const", Fraction(0)))
        rules = [c.rule for c in Engine(f, root).certificates]
        # fresh engine to inspect certificates
        eng = Engine(f, root)
        eng.result()
        self.assertIn("sub-self", [c.rule for c in eng.certificates])

    def test_sub_self_nontotal_kept(self):
        f, root = make("(1 / y) - (1 / y)")
        eng = Engine(f, root)
        r = eng.result()
        self.assertEqual(r.op, "sub")               # NOT folded to 0
        self.assertTrue(any(e.kind == "note" for e in eng.journal))

    def test_zero_times_undefined_is_not_zero(self):
        f, root = make("0 * (1 / y)")
        eng = Engine(f, root)
        r = eng.result()
        self.assertEqual(r.op, "mul")               # kept, not folded to 0
        self.assertTrue(any("0 · undefined" in e.detail
                            for e in eng.journal if e.kind == "note"))

    def test_zero_times_total_folds(self):
        f, root = make("0 * (y + 1)")
        eng = Engine(f, root)
        self.assertEqual(eng.result().op, "const")
        self.assertEqual(eng.result().value, 0)
        self.assertIn("mul-zero-left", [c.rule for c in eng.certificates])

    def test_zero_times_undef_node_becomes_undef_not_zero(self):
        f, root = make("0 * (1 / 0)")
        r = Engine(f, root).result()
        self.assertEqual(r.op, "undef")             # undefined, never 0

    def test_div_by_literal_zero_is_undef_with_position(self):
        f, root = make("1 / 0")
        r = Engine(f, root).result()
        self.assertEqual(r.op, "undef")
        self.assertEqual(r.origin, root.origin)
        with self.assertRaises(UndefinedError) as cm:
            evaluate(root, {})
        self.assertEqual(cm.exception.pos, r.origin)

    def test_cond_fold_selects_branch(self):
        f, root = make("cond(2, a, b)")
        r = Engine(f, root).result()
        self.assertEqual((r.op, r.name), ("var", "a"))
        f, root = make("cond(0, a, b)")
        r = Engine(f, root).result()
        self.assertEqual((r.op, r.name), ("var", "b"))

    def test_cond_same_branch_requires_total_condition(self):
        f, root = make("cond(x, y + 1, y + 1)")
        eng = Engine(f, root)
        r = eng.result()
        self.assertEqual(show(r), "(+ y 1)")
        self.assertIn("cond-same-branch", [c.rule for c in eng.certificates])

    def test_unbound_variable_raises(self):
        f, root = make("x + 1")
        with self.assertRaises(UnboundError):
            evaluate(root, {})

    def test_certificates_carry_domain_info(self):
        f, root = make("(x + 0) * 1 + 0 * (y + 2)")
        eng = Engine(f, root)
        eng.result()
        self.assertTrue(eng.certificates)
        for c in eng.certificates:
            self.assertTrue(c.rule)
            self.assertTrue(c.before)
            self.assertTrue(c.after)
            self.assertTrue(c.requires)   # applicable domain stated
            self.assertTrue(c.holds)      # and why it holds here


class TestErrorLocalisation(unittest.TestCase):
    def test_position_preserved_through_simplification(self):
        f, root = make("(1 / (x - 2)) + (1 / (y - y))")
        eng = Engine(f, root)
        eng.set("x", 2)
        eng.set("y", 9)
        s = eng.result()
        env = {"x": Fraction(2), "y": Fraction(9)}
        self.assertEqual(run(root, env), run(s, env))
        self.assertEqual(run(root, env)[0], "undef")

    def test_leftmost_error_wins(self):
        f, root = make("(1 / 0) + (1 / 0)")
        # both divs hash-cons to one node; use structurally distinct ones
        f, root = make("(1 / 0) + (2 / 0)")
        r = Engine(f, root).result()
        self.assertEqual(r.op, "undef")
        left_div = root.args[0]
        self.assertEqual(r.origin, left_div.origin)
        self.assertEqual(run(root, {}), ("undef", left_div.origin))

    def test_right_undef_not_propagated_past_nontotal_left(self):
        f, root = make("(1 / y) + (1 / 0)")
        eng = Engine(f, root)
        s = eng.result()
        self.assertEqual(s.op, "add")   # must NOT collapse to the right ⊥
        env0 = {"y": Fraction(0)}
        env2 = {"y": Fraction(2)}
        self.assertEqual(run(root, env0), run(s, env0))  # left error first
        self.assertEqual(run(root, env2), run(s, env2))  # then right error
        self.assertEqual(run(root, env0)[0], "undef")

    def test_div_zero_waits_for_nontotal_numerator(self):
        f, root = make("(1 / y) / 0")
        eng = Engine(f, root)
        s = eng.result()
        self.assertEqual(s.op, "div")   # numerator may fail first: keep
        env = {"y": Fraction(0)}
        self.assertEqual(run(root, env), run(s, env))
        env = {"y": Fraction(2)}
        self.assertEqual(run(root, env), run(s, env))

    def test_undef_position_after_binding_makes_divisor_zero(self):
        f, root = make("0 * (1 / y)")
        eng = Engine(f, root)
        eng.set("y", 0)
        s = eng.result()
        self.assertEqual(s.op, "undef")
        env = {"y": Fraction(0)}
        self.assertEqual(run(root, env), run(s, env))


class TestIncremental(unittest.TestCase):
    def test_retracts_stale_constant_conclusions(self):
        f, root = make("(x + 1) * 2")
        eng = Engine(f, root)
        eng.set("x", 2)
        self.assertEqual(eng.result().value, Fraction(6))
        n0 = len(eng.journal)
        eng.set("x", 5)
        self.assertEqual(eng.result().value, Fraction(12))
        retracts = [e for e in eng.journal[n0:] if e.kind == "retract"]
        self.assertTrue(retracts)
        self.assertTrue(any("6" in e.detail for e in retracts))

    def test_unbind_restores_symbolic_form(self):
        f, root = make("x * 2")
        eng = Engine(f, root)
        eng.set("x", 4)
        self.assertEqual(eng.result().value, Fraction(8))
        eng.set("x", None)
        r = eng.result()
        self.assertEqual(free_vars(r), {"x"})
        fresh = Engine(f, root).result()
        self.assertEqual(canon_key(r), canon_key(fresh))

    def test_cond_branch_switch_garbage_collects_dependencies(self):
        f = Factory()
        x, a, b = f.var("x"), f.var("a"), f.var("b")
        then_br = f.add(a, f.const(1))
        else_br = f.mul(b, f.const(2))
        root = f.cond(x, then_br, else_br)
        eng = Engine(f, root)
        eng.result()
        self.assertIn(else_br.id, eng.live())
        j = len(eng.journal)
        eng.set("x", 1)
        self.assertNotIn(else_br.id, eng.live())     # dependency removed
        self.assertNotIn(b.id, eng.live())
        self.assertTrue(any(e.kind == "gc" for e in eng.journal[j:]))
        j = len(eng.journal)
        eng.set("x", 0)
        self.assertIn(else_br.id, eng.live())        # recomputed on demand
        self.assertNotIn(then_br.id, eng.live())     # old branch released
        self.assertTrue(any(e.kind == "gc" for e in eng.journal[j:]))

    def test_incremental_matches_fresh_recompute(self):
        rng = random.Random(7)
        for _ in range(60):
            f = Factory()
            root = rand_expr(f, rng, 4)
            env = rand_env(rng)
            eng = Engine(f, root)
            # apply bindings one at a time, with a change of mind in between
            for name in VARS:
                eng.set(name, Fraction(1))
            for name in VARS:
                eng.set(name, env[name])
            eng.set("x", None)
            eng.set("x", env["x"])
            fresh = Engine(f, root)
            for name in VARS:
                fresh.set(name, env[name])
            self.assertEqual(canon_key(eng.result()), canon_key(fresh.result()))
            self.assertEqual(run(root, env), run(eng.result(), env))

    def test_binding_order_independence(self):
        rng = random.Random(11)
        for _ in range(60):
            text_env = rand_env(rng)
            f = Factory()
            root = rand_expr(f, rng, 4)
            results = []
            orders = [list(VARS), list(reversed(VARS)),
                      [VARS[1], VARS[2], VARS[0]]]
            for order in orders:
                eng = Engine(f, root)
                for name in order:
                    eng.set(name, text_env[name])
                results.append(canon_key(eng.result()))
            self.assertEqual(results[0], results[1])
            self.assertEqual(results[0], results[2])


class TestCrossCheck(unittest.TestCase):
    """Randomised agreement with the independent interpreter."""

    def test_full_binding_crosscheck(self):
        rng = random.Random(2024)
        for _ in range(400):
            f = Factory()
            root = rand_expr(f, rng, 4)
            env = rand_env(rng)
            eng = Engine(f, root)
            for name, v in env.items():
                eng.set(name, v)
            s = eng.result()
            self.assertEqual(run(root, env), run(s, env),
                             msg=f"expr={show(root)} env={env}")

    def test_partial_binding_crosscheck_with_completion(self):
        rng = random.Random(99)
        for _ in range(400):
            f = Factory()
            root = rand_expr(f, rng, 4)
            full = rand_env(rng)
            partial = {n: v for n, v in full.items() if rng.random() < 0.5}
            eng = Engine(f, root)
            for name, v in partial.items():
                eng.set(name, v)
            s = eng.result()
            # unbound nodes must be preserved as variables
            self.assertTrue(free_vars(s) <= (set(VARS) - set(partial)))
            # same value / same undefined position under any completion
            self.assertEqual(run(root, full), run(s, full),
                             msg=f"expr={show(root)} partial={partial} "
                                 f"full={full} simplified={show(s)}")

    def test_certificates_produced_for_random_runs(self):
        rng = random.Random(5)
        saw = set()
        for _ in range(200):
            f = Factory()
            root = rand_expr(f, rng, 3)
            eng = Engine(f, root)
            for name in VARS:
                if rng.random() < 0.7:
                    eng.set(name, Fraction(rng.randint(-2, 2)))
            eng.result()
            saw.update(c.rule for c in eng.certificates)
        self.assertIn("bind", saw)
        self.assertIn("add-const", saw)
        self.assertIn("div-zero-const", saw)


if __name__ == "__main__":
    unittest.main(verbosity=2)
