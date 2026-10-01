import unittest

from cardenc import (ATMOST, ATLEAST, EXACTLY, IncrementalAtMost, VarPool,
                     analyze_propagation, check_direct, encode, solve,
                     verify_model_independent, verify_projection_equivalence)
from cardenc.verify import count_models_per_projection

OPS = (ATMOST, ATLEAST, EXACTLY)
METHODS = ("seq", "net")


class TestBoundarySemantics(unittest.TestCase):
    """k < 0 / k > n decided by semantics; n = 0, exactly 0 satisfiable."""

    def test_statuses(self):
        cases = [
            (ATMOST, -1, 3, "trivial-false"),
            (ATMOST, 3, 3, "trivial-true"),
            (ATMOST, 4, 3, "trivial-true"),
            (ATMOST, 0, 3, "simplified"),
            (ATLEAST, -1, 3, "trivial-true"),
            (ATLEAST, 0, 3, "trivial-true"),
            (ATLEAST, 4, 3, "trivial-false"),
            (ATLEAST, 3, 3, "simplified"),
            (EXACTLY, -1, 3, "trivial-false"),
            (EXACTLY, 4, 3, "trivial-false"),
            (EXACTLY, 0, 0, "trivial-true"),   # n = 0, exactly 0 holds
            (EXACTLY, 1, 0, "trivial-false"),
            (ATMOST, 0, 0, "trivial-true"),
            (ATLEAST, 0, 0, "trivial-true"),
            (ATLEAST, 1, 0, "trivial-false"),
        ]
        for op, k, n, expected in cases:
            for method in METHODS:
                pool = VarPool()
                lits = pool.reserve(n, "x")
                enc = encode(pool, lits, op, k, method=method)
                self.assertEqual(enc.status, expected, (op, k, n, method))

    def test_boundary_equivalence(self):
        for n in range(0, 5):
            for k in range(-2, n + 3):
                for op in OPS:
                    for method in METHODS:
                        rep = verify_projection_equivalence(
                            VarPool(), n, op, k, method)
                        self.assertTrue(rep["ok"], rep)


class TestProjectionEquivalence(unittest.TestCase):
    """All small inputs agree with direct counting, both encodings."""

    def test_exhaustive_small(self):
        for n in range(1, 6):
            for k in range(-1, n + 2):
                for op in OPS:
                    for method in METHODS:
                        rep = verify_projection_equivalence(
                            VarPool(), n, op, k, method)
                        self.assertTrue(rep["ok"], rep)
                        self.assertEqual(rep["checked"], 1 << n)

    def test_spot_n6(self):
        for op, k in [(ATMOST, 2), (ATLEAST, 4), (EXACTLY, 3)]:
            for method in METHODS:
                rep = verify_projection_equivalence(VarPool(), 6, op, k, method)
                self.assertTrue(rep["ok"], rep)

    def test_exhaustive_aux_enumeration(self):
        # n=4, at-most 2: enumerate every auxiliary assignment explicitly.
        for method in METHODS:
            pool = VarPool()
            lits = pool.reserve(4, "x")
            enc = encode(pool, lits, ATMOST, 2, method=method)
            counts = count_models_per_projection(enc)
            self.assertEqual(len(counts), 16)
            for bits, num in counts.items():
                self.assertEqual(num > 0, check_direct(ATMOST, 2, bits),
                                 (method, bits))
            self.assertEqual(sum(1 for c in counts.values() if c > 0), 11)


class TestWitnessLift(unittest.TestCase):
    """Original assignment -> auxiliary assignment witness validation."""

    def test_lift_satisfies_cnf(self):
        for n in range(1, 6):
            for k in range(0, n + 1):
                for op in OPS:
                    for method in METHODS:
                        pool = VarPool()
                        lits = pool.reserve(n, "x")
                        enc = encode(pool, lits, op, k, method=method)
                        for mask in range(1 << n):
                            values = {v: bool((mask >> i) & 1)
                                      for i, v in enumerate(lits)}
                            if not check_direct(op, k, [values[v] for v in lits]):
                                continue
                            witness = dict(values)
                            witness.update(enc.lift(values))
                            self.assertIs(enc.cnf.evaluate(witness), True,
                                          (n, k, op, method, mask))


class TestVariableNumbering(unittest.TestCase):
    def test_no_conflicts_between_encodings(self):
        pool = VarPool()
        lits = pool.reserve(5, "x")
        enc1 = encode(pool, lits, ATMOST, 2, method="seq")
        enc2 = encode(pool, lits, EXACTLY, 3, method="net")
        enc3 = encode(pool, lits, ATLEAST, 1, method="seq")
        all_aux = enc1.aux + enc2.aux + enc3.aux
        self.assertEqual(len(all_aux), len(set(all_aux)))
        self.assertTrue(all(v > max(lits) for v in all_aux))
        self.assertTrue(set(all_aux).isdisjoint(lits))

    def test_duplicate_names_rejected(self):
        pool = VarPool()
        pool.new_var("a")
        with self.assertRaises(ValueError):
            pool.new_var("a")


class TestClauseCounts(unittest.TestCase):
    def test_sequential_counter_size(self):
        n, k = 5, 2
        pool = VarPool()
        lits = pool.reserve(n, "x")
        enc = encode(pool, lits, ATMOST, k, method="seq")
        self.assertEqual(enc.num_aux, n * k)
        self.assertEqual(enc.num_clauses, 1 + 3 * (n - 1) + 2 * (n - 1) * (k - 1))

    def test_network_size(self):
        n, k = 4, 1
        pool = VarPool()
        lits = pool.reserve(n, "x")
        enc = encode(pool, lits, ATMOST, k, method="net")
        self.assertEqual(enc.num_aux, 2 * 5)  # 5 comparators, 2 wires each
        self.assertEqual(enc.num_clauses, 5 * 6 + 1)


class TestPropagation(unittest.TestCase):
    """Both encodings must be GAC and detect violations by unit propagation."""

    def test_gac_atmost_and_exactly(self):
        for n in range(2, 7):
            for k in range(1, n):
                for op in (ATMOST, EXACTLY):
                    for method in METHODS:
                        rep = analyze_propagation(VarPool(), n, op, k, method)
                        self.assertTrue(rep["gac"], rep)
                        self.assertTrue(rep["conflict_detected"], rep)

    def test_gac_atleast(self):
        for n in range(2, 7):
            for k in range(1, n + 1):
                for method in METHODS:
                    rep = analyze_propagation(VarPool(), n, ATLEAST, k, method)
                    self.assertTrue(rep["gac"], rep)
                    self.assertTrue(rep["conflict_detected"], rep)


class TestIndependentChecker(unittest.TestCase):
    def test_claimed_models_verified_directly(self):
        pool = VarPool()
        lits = pool.reserve(5, "x")
        enc = encode(pool, lits, EXACTLY, 2, method="net")
        model = solve(enc.cnf)
        self.assertIsNotNone(model)
        self.assertTrue(verify_model_independent(enc, model))
        self.assertIs(enc.cnf.evaluate(model), True)

    def test_unsat_claim(self):
        pool = VarPool()
        lits = pool.reserve(3, "x")
        enc = encode(pool, lits, ATMOST, 1, method="seq")
        self.assertIsNone(solve(enc.cnf, assumptions=[lits[0], lits[1]]))


class TestIncremental(unittest.TestCase):
    def test_tighten_and_retract_via_assumptions(self):
        pool = VarPool()
        x = pool.reserve(5, "x")
        inc = IncrementalAtMost(pool, x, capacity=3)

        a2 = inc.tighten(2)
        model = solve(inc.cnf, assumptions=[a2])
        self.assertIsNotNone(model)
        self.assertTrue(check_direct(ATMOST, 2, [model[v] for v in x]))

        # Tighten to 1: only adds clauses, a2 simply no longer assumed.
        a1 = inc.tighten(1)
        model1 = solve(inc.cnf, assumptions=[a1])
        self.assertIsNotNone(model1)
        self.assertTrue(check_direct(ATMOST, 1, [model1[v] for v in x]))
        # Bound 1 really enforced.
        self.assertIsNone(solve(inc.cnf, assumptions=[a1, x[0], x[1]]))

        # Retract back to 2 by assuming a2 again (no clause removal).
        model2 = solve(inc.cnf, assumptions=[a2, x[0], x[1]])
        self.assertIsNotNone(model2)
        self.assertTrue(check_direct(ATMOST, 2, [model2[v] for v in x]))

        # Witness for the incremental encoding.
        values = {v: model2[v] for v in x}
        witness = dict(values)
        witness.update(inc.lift(values, 2))
        self.assertIs(inc.cnf.evaluate(witness), True)

    def test_aux_naming_no_conflict(self):
        pool = VarPool()
        x = pool.reserve(4, "x")
        inc = IncrementalAtMost(pool, x, capacity=3)
        inc.tighten(1)
        inc.tighten(2)
        enc = encode(pool, x, ATMOST, 2, method="seq")
        self.assertTrue(set(inc.aux).isdisjoint(enc.aux))
        self.assertEqual(len(inc.aux), len(set(inc.aux)))

    def test_bound_outside_capacity_rejected(self):
        pool = VarPool()
        x = pool.reserve(3, "x")
        inc = IncrementalAtMost(pool, x, capacity=2)
        with self.assertRaises(ValueError):
            inc.tighten(3)
        with self.assertRaises(ValueError):
            inc.tighten(-1)


if __name__ == "__main__":
    unittest.main()
