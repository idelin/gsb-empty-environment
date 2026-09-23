"""Unit and differential tests for symdag (stdlib unittest only)."""
from __future__ import annotations

import pathlib
import random
import unittest
from fractions import Fraction

import symdag
from symdag import Factory, Session, UndefinedError, interpret, simplify_once, to_str
from symdag.gen import DEFAULT_CONSTS, DEFAULT_VARS, random_spec


def run(node, env):
    """Return ('ok', Fraction) or ('undef', path) for comparison."""
    try:
        return ("ok", interpret(node, env))
    except UndefinedError as err:
        return ("undef", err.path)


class TestExactArithmetic(unittest.TestCase):
    def test_exact_rational_folding(self):
        f = Factory()
        expr = f.parse(("add", ("const", Fraction(1, 3)),
                        ("mul", ("const", Fraction(1, 2)), ("const", Fraction(1, 6)))))
        result, certs, _ = simplify_once(expr, factory=f)
        self.assertEqual(result.op, "const")
        self.assertEqual(result.value, Fraction(5, 12))
        self.assertTrue(any(c.rule == "const-fold" for c in certs))

    def test_rational_no_rounding(self):
        f = Factory()
        expr = f.parse(("mul", ("div", ("const", 1), ("const", 3)), ("const", 3)))
        result, _, _ = simplify_once(expr, factory=f)
        self.assertEqual(result.value, Fraction(1))  # exact, not 0.999...


class TestPartialBinding(unittest.TestCase):
    def test_unbound_nodes_preserved(self):
        f = Factory()
        expr = f.parse(("add", ("var", "x"), ("var", "y")))
        result, _, _ = simplify_once(expr, {"x": 2}, f)
        self.assertEqual(to_str(result), "(+ 2 y)")
        self.assertEqual(f.free_vars(result), frozenset({"y"}))

    def test_repeated_variable_single_binding(self):
        f = Factory()
        expr = f.parse(("add", ("mul", ("var", "x"), ("var", "x")), ("var", "x")))
        result, _, session = simplify_once(expr, {"x": 3}, f)
        self.assertEqual(result.value, Fraction(12))  # 3*3 + 3
        entry = session.cache[expr]
        self.assertEqual(entry.reads, frozenset({"x"}))  # one binding, used by all occurrences

    def test_certificates_carry_domain(self):
        f = Factory()
        expr = f.parse(("add", ("mul", ("var", "x"), ("const", 1)), ("const", 0)))
        result, certs, _ = simplify_once(expr, factory=f)
        self.assertEqual(to_str(result), "x")
        rules = [c.rule for c in certs]
        self.assertIn("mul-one", rules)
        self.assertIn("add-zero", rules)
        for cert in certs:
            self.assertTrue(cert.domain.strip())
            self.assertTrue(cert.before.strip())
            self.assertTrue(cert.after.strip())


class TestDomainPreservation(unittest.TestCase):
    def test_zero_times_undefined_is_not_zero(self):
        f = Factory()
        # 0 * (1 / (x - x)): undefined everywhere, must NOT become 0.
        expr = f.parse(("mul", ("const", 0),
                        ("div", ("const", 1), ("sub", ("var", "x"), ("var", "x")))))
        result, _, _ = simplify_once(expr, factory=f)
        self.assertEqual(result.op, "undef")
        self.assertEqual(run(expr, {"x": Fraction(5)}), run(result, {}))

    def test_zero_times_maybe_undefined_kept(self):
        f = Factory()
        # 0 * (1 / y): y may be 0, so folding to 0 would enlarge the domain.
        expr = f.parse(("mul", ("const", 0), ("div", ("const", 1), ("var", "y"))))
        session = Session(f)
        result = session.simplify(expr)
        self.assertEqual(result.op, "mul")
        self.assertTrue(any(c.rule == "mul-zero" for c in session.suppressed))
        session.set("y", 0)
        self.assertEqual(session.simplify(expr).op, "undef")
        session.set("y", 4)
        self.assertEqual(session.simplify(expr).value, Fraction(0))

    def test_sub_self_requires_totality(self):
        f = Factory()
        # (1/x) - (1/x): 1/x may be undefined, so this must NOT fold to 0.
        expr = f.parse(("sub", ("div", ("const", 1), ("var", "x")),
                        ("div", ("const", 1), ("var", "x"))))
        result, _, session = simplify_once(expr, factory=f)
        self.assertEqual(result.op, "sub")
        self.assertTrue(any(c.rule == "sub-self" for c in session.suppressed))
        self.assertEqual(run(expr, {"x": Fraction(0)}), run(result, {"x": Fraction(0)}))
        self.assertEqual(run(expr, {"x": Fraction(2)}), ("ok", Fraction(0)))

    def test_error_position_localization(self):
        f = Factory()
        expr = f.parse(("add", ("const", 1),
                        ("div", ("const", 1), ("sub", ("var", "x"), ("const", 2)))))
        result, _, _ = simplify_once(expr, {"x": 2}, f)
        self.assertEqual(result.op, "undef")
        self.assertEqual(result.origin, (1,))  # the division node
        status, path = run(expr, {"x": Fraction(2)})
        self.assertEqual((status, path), ("undef", (1,)))

    def test_conditional_constant_folds(self):
        f = Factory()
        expr = f.parse(("if", ("const", 0), ("var", "a"), ("var", "b")))
        result, _, _ = simplify_once(expr, factory=f)
        self.assertEqual(to_str(result), "b")
        expr2 = f.parse(("if", ("const", 3), ("var", "a"), ("var", "b")))
        result2, _, _ = simplify_once(expr2, factory=f)
        self.assertEqual(to_str(result2), "a")


class TestIncremental(unittest.TestCase):
    def test_binding_update_retracts_constants(self):
        f = Factory()
        expr = f.parse(("add", ("var", "x"), ("const", 1)))
        session = Session(f)
        session.set("x", 1)
        self.assertEqual(session.simplify(expr).value, Fraction(2))
        session.set("x", 5)
        self.assertGreaterEqual(session.stats["retracted_constants"], 1)
        self.assertEqual(session.simplify(expr).value, Fraction(6))
        session.unset("x")
        self.assertEqual(session.simplify(expr), f.add(f.var("x"), f.const(1)))

    def test_unrelated_subtree_survives_update(self):
        f = Factory()
        expr = f.parse(("mul", ("add", ("var", "x"), ("const", 1)),
                        ("add", ("var", "y"), ("const", 2))))
        session = Session(f)
        session.set("x", 1)
        first = session.simplify(expr)
        self.assertEqual(first, f.mul(f.const(2), f.add(f.var("y"), f.const(2))))
        hits_before = session.stats["hits"]
        invalidated_before = session.stats["invalidated"]
        session.set("x", 2)  # only x-entries may be retracted
        self.assertEqual(session.stats["invalidated"] - invalidated_before, 3)
        second = session.simplify(expr)
        self.assertGreater(session.stats["hits"], hits_before)  # y-subtree reused
        self.assertEqual(second, f.mul(f.const(3), f.add(f.var("y"), f.const(2))))

    def test_conditional_branch_switch_drops_dependencies(self):
        f = Factory()
        expr = f.parse(("if", ("sub", ("var", "x"), ("const", 1)),
                        ("add", ("var", "y"), ("const", 1)),
                        ("mul", ("var", "z"), ("const", 2))))
        session = Session(f)
        session.set("x", 1)  # condition 0 -> else branch (z * 2)
        result = session.simplify(expr)
        self.assertEqual(result, f.mul(f.var("z"), f.const(2)))
        # the root no longer depends on the dead branch's variable y
        self.assertEqual(session.cache[expr].frees, frozenset({"z"}))
        root_entry = session.cache[expr]
        session.set("y", 9)  # y is only in the dead branch
        self.assertIs(session.cache[expr], root_entry)  # root not retracted
        computed = session.stats["computed"]
        self.assertEqual(session.simplify(expr), result)  # no recomputation
        self.assertEqual(session.stats["computed"], computed)
        session.unset("y")
        session.set("x", 2)  # condition nonzero -> then branch (y + 1)
        result = session.simplify(expr)
        self.assertEqual(result, f.add(f.var("y"), f.const(1)))
        # now z is the dead-branch variable and is gone from the root deps
        self.assertEqual(session.cache[expr].frees, frozenset({"y"}))
        root_entry = session.cache[expr]
        session.set("z", 7)
        self.assertIs(session.cache[expr], root_entry)
        computed = session.stats["computed"]
        self.assertEqual(session.simplify(expr), result)
        self.assertEqual(session.stats["computed"], computed)

    def test_binding_order_independence(self):
        spec = ("add",
                ("mul", ("var", "x"), ("var", "y")),
                ("if", ("sub", ("var", "z"), ("const", 1)),
                 ("div", ("var", "x"), ("const", 2)),
                 ("div", ("var", "y"), ("add", ("var", "x"), ("const", 1)))))
        final = {"x": Fraction(3), "y": Fraction(4), "z": Fraction(5)}
        results = []
        for order in (("x", "y", "z"), ("z", "y", "x"), ("y", "z", "x")):
            f = Factory()
            session = Session(f)
            for name in order:
                session.set(name, final[name])
            results.append(session.simplify(session.f.parse(spec)))
        f = Factory()
        one_shot, _, _ = simplify_once(f.parse(spec), final, f)
        results.append(one_shot)
        keys = {r.key for r in results}
        self.assertEqual(len(keys), 1)  # canonical result independent of order


class TestNoHostEval(unittest.TestCase):
    def test_package_uses_no_eval_or_exec(self):
        root = pathlib.Path(symdag.__file__).parent
        for src in root.glob("*.py"):
            text = src.read_text(encoding="utf-8")
            self.assertNotIn("eval(", text, src.name)
            self.assertNotIn("exec(", text, src.name)


class TestDifferential(unittest.TestCase):
    """Simplified expression vs. independent interpreter on the original."""

    def test_random_exprs_match_on_small_rational_grid(self):
        rng = random.Random(20260923)
        mismatches = 0
        checked = 0
        for _ in range(150):
            f = Factory()
            spec = random_spec(rng, depth=4)
            original = f.parse(spec)
            variables = sorted(f.free_vars(original))
            # random partial binding
            bindings = {v: rng.choice(DEFAULT_CONSTS)
                        for v in variables if rng.random() < 0.5}
            simplified, _, _ = simplify_once(original, bindings, f)
            for _ in range(20):
                env = dict(bindings)
                for v in variables:
                    if v not in env:
                        env[v] = rng.choice(DEFAULT_CONSTS)
                expected = run(original, env)
                got = run(simplified, env)
                checked += 1
                if expected != got:
                    mismatches += 1
                    self.fail(
                        f"mismatch on {to_str(original)} -> {to_str(simplified)} "
                        f"with env {env}: original={expected} simplified={got}")
        self.assertEqual(mismatches, 0)
        self.assertGreater(checked, 1000)

    def test_error_paths_match_when_undefined(self):
        rng = random.Random(7)
        seen_undefined = 0
        for _ in range(60):
            f = Factory()
            spec = random_spec(rng, variables=("x",), depth=4)
            original = f.parse(spec)
            simplified, _, _ = simplify_once(original, factory=f)
            for value in DEFAULT_CONSTS:
                env = {"x": value}
                expected = run(original, env)
                got = run(simplified, env)
                self.assertEqual(expected, got,
                                 f"{to_str(original)} vs {to_str(simplified)} at {env}")
                if expected[0] == "undef":
                    seen_undefined += 1
        self.assertGreater(seen_undefined, 0)  # the grid really hits errors


if __name__ == "__main__":
    unittest.main()
