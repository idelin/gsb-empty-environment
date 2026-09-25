"""Unittest suite for the pure-Python global assembler.

Small-scale results are verified against an independent, deliberately
naive full reassembly and hand-computed dense elimination.
"""

import unittest

from assembler import Assembler, AssemblyError, Constraint, solve_dense


def reference_assemble(n, elements):
    """Independent full assembly: rebuild the global system from scratch."""
    a = [[0.0] * n for _ in range(n)]
    b = [0.0] * n
    for _eid, dof_map, ke, fe in elements:
        for li, gi in enumerate(dof_map):
            b[gi] += fe[li]
            for lj, gj in enumerate(dof_map):
                a[gi][gj] += ke[li][lj]
    return a, b


def reference_reduce(a, b, n, constraints):
    """Independent dense elimination: A_red = T^T A T, b_red = T^T (b - A g)."""
    free = [i for i in range(n) if i not in constraints]
    pos = {d: k for k, d in enumerate(free)}
    m = len(free)
    T = [[0.0] * m for _ in range(n)]
    g = [0.0] * n
    for k, d in enumerate(free):
        T[d][k] = 1.0
    for c, (terms, const) in constraints.items():
        g[c] = const
        for d, coef in terms:
            T[c][pos[d]] += coef
    a_red = [[sum(T[i][p] * a[i][j] * T[j][q]
                  for i in range(n) for j in range(n))
              for q in range(m)] for p in range(m)]
    b_red = [sum(T[i][p] * (b[i] - sum(a[i][j] * g[j] for j in range(n)))
                 for i in range(n)) for p in range(m)]
    return a_red, b_red, T, g


def spring_chain(n_elem, k=1.0, f=0.5):
    return [(f"e{e}", [e, e + 1], [[k, -k], [-k, k]], [f, f])
            for e in range(n_elem)]


class TestSharedDofsAndProvenance(unittest.TestCase):
    def test_shared_entry_sums_two_elements(self):
        asm = Assembler(3)
        asm.add_element("a", [0, 1], [[1, -1], [-1, 1]], [0, 1])
        asm.add_element("b", [1, 2], [[2, -2], [-2, 2]], [1, 0])
        self.assertEqual(asm.matrix[1][1], 3.0)   # 1.0 + 2.0
        self.assertEqual(asm.rhs[1], 2.0)         # 1.0 + 1.0
        sources = {c.source for c in asm.provenance(1, 1)}
        self.assertEqual(sources, {"a", "b"})
        total = sum(c.value for c in asm.provenance(1, 1))
        self.assertAlmostEqual(total, asm.matrix[1][1])

    def test_repeated_contributions_accumulate(self):
        asm = Assembler(2)
        for i in range(4):
            asm.add_element(i, [0, 1], [[1, 0], [0, 1]], [1, 1])
        self.assertEqual(asm.matrix[0][0], 4.0)
        self.assertEqual(len(asm.provenance(0, 0)), 4)

    def test_sign_cancellation_keeps_provenance(self):
        asm = Assembler(2)
        asm.add_element("pos", [0, 1], [[5.0, 0], [0, 0]], [0, 0])
        asm.add_element("neg", [0, 1], [[-5.0, 0], [0, 0]], [0, 0])
        self.assertEqual(asm.matrix[0][0], 0.0)
        prov = asm.provenance(0, 0)
        self.assertEqual(len(prov), 2)
        self.assertAlmostEqual(sum(c.value for c in prov), 0.0)


class TestIncrementalUpdates(unittest.TestCase):
    def test_update_and_remove_match_full_reassembly(self):
        asm = Assembler(6)
        elements = spring_chain(5, k=1.5, f=0.25)
        asm.apply_batch([("add",) + tuple(e) for e in elements])
        # incremental: update e2, remove e4
        asm.update_element("e2", [2, 3], [[3.0, -3.0], [-3.0, 3.0]], [1.0, 0.0])
        asm.remove_element("e4")
        remaining = [e for e in elements if e[0] != "e4"]
        remaining[2] = ("e2", [2, 3], [[3.0, -3.0], [-3.0, 3.0]], [1.0, 0.0])
        a_ref, b_ref = reference_assemble(6, remaining)
        self.assertEqual(asm.matrix, a_ref)
        self.assertEqual(asm.rhs, b_ref)
        # provenance agrees with the surviving elements
        self.assertEqual({c.source for c in asm.provenance(2, 2)}, {"e1", "e2"})
        # e4 withdrawn from (4,4); only e3's contribution remains there
        self.assertEqual({c.source for c in asm.provenance(4, 4)}, {"e3"})
        self.assertEqual(asm.provenance(5, 5), [])

    def test_update_does_not_traverse_all_elements(self):
        asm = Assembler(12)
        asm.apply_batch([("add",) + tuple(e) for e in spring_chain(11)])
        asm.reset_touch_count()
        asm.update_element("e5", [5, 6], [[2, -2], [-2, 2]], [0, 0])
        # 2x2 matrix + 2 rhs entries, withdrawn then re-applied: 2*(4+2)
        self.assertEqual(asm.touch_count, 12)
        # same cost with a different element count -> local, not global
        asm2 = Assembler(6)
        asm2.apply_batch([("add",) + tuple(e) for e in spring_chain(5)])
        asm2.reset_touch_count()
        asm2.update_element("e1", [1, 2], [[2, -2], [-2, 2]], [0, 0])
        self.assertEqual(asm2.touch_count, 12)


class TestAtomicBatch(unittest.TestCase):
    def test_invalid_mapping_rejects_whole_batch(self):
        asm = Assembler(3)
        with self.assertRaises(AssemblyError):
            asm.apply_batch([
                ("add", "ok", [0, 1], [[1, 0], [0, 1]], [1, 1]),
                ("add", "bad", [1, 7], [[1, 0], [0, 1]], [1, 1]),
            ])
        self.assertEqual(asm.element_ids(), [])
        self.assertEqual(asm.matrix, [[0.0] * 3 for _ in range(3)])
        self.assertEqual(asm.rhs, [0.0] * 3)

    def test_invalid_mappings(self):
        asm = Assembler(3)
        bad = [
            ("add", "x", [0, 3], [[1, 0], [0, 1]], [0, 0]),   # out of range
            ("add", "x", [0, -1], [[1, 0], [0, 1]], [0, 0]),  # negative
            ("add", "x", [0, 0], [[1, 0], [0, 1]], [0, 0]),   # duplicate dof
            ("add", "x", [0, 1], [[1, 0]], [0, 0]),           # ke shape
            ("add", "x", [0, 1], [[1, 0], [0, 1]], [0]),      # fe length
            ("add", "x", [0, 1.5], [[1, 0], [0, 1]], [0, 0]),  # non-integer dof
        ]
        for op in bad:
            with self.assertRaises(AssemblyError, msg=op):
                asm.apply_batch([op])
        self.assertEqual(asm.element_ids(), [])

    def test_duplicate_add_and_unknown_update_rejected(self):
        asm = Assembler(2)
        asm.add_element("a", [0, 1], [[1, 0], [0, 1]], [0, 0])
        with self.assertRaises(AssemblyError):
            asm.add_element("a", [0, 1], [[1, 0], [0, 1]], [0, 0])
        with self.assertRaises(AssemblyError):
            asm.update_element("ghost", [0, 1], [[1, 0], [0, 1]], [0, 0])
        with self.assertRaises(AssemblyError):
            asm.remove_element("ghost")


class TestConstraintElimination(unittest.TestCase):
    def test_elimination_matches_independent_dense_reduction(self):
        n = 4
        elements = spring_chain(3)
        asm = Assembler(n)
        asm.apply_batch([("add",) + tuple(e) for e in elements])
        asm.set_constraints([
            Constraint(0, (), 0.0),
            Constraint(3, ((1, 0.5),), 0.25),   # u3 = 0.5 u1 + 0.25
        ])
        a, b = reference_assemble(n, elements)
        a_ref, b_ref, T, g = reference_reduce(
            a, b, n, {0: ((), 0.0), 3: (((1, 0.5),), 0.25)})
        rs = asm.constrained_system()
        self.assertEqual(rs.version, 1)
        for row_a, row_b in zip(rs.matrix, a_ref):
            for x, y in zip(row_a, row_b):
                self.assertAlmostEqual(x, y)
        for x, y in zip(rs.rhs, b_ref):
            self.assertAlmostEqual(x, y)

    def test_rhs_correction_not_omitted(self):
        # A = [[2,-1],[-1,2]], b = 0, constraint u1 = u0 + 1.
        # A_red = [2], b_red = T^T (b - A g) = -1  (pure correction term).
        asm = Assembler(2)
        asm.add_element("e", [0, 1], [[2, -1], [-1, 2]], [0, 0])
        asm.set_constraints([Constraint(1, ((0, 1.0),), 1.0)])
        rs = asm.constrained_system()
        self.assertEqual(rs.free_dofs, [0])
        self.assertAlmostEqual(rs.matrix[0][0], 2.0)
        self.assertAlmostEqual(rs.rhs[0], -1.0)   # would be 0 if correction omitted
        corr = [c for c in rs.rhs_provenance[0] if c.kind == "constraint"]
        self.assertTrue(corr, "constraint correction missing from provenance")
        self.assertAlmostEqual(sum(c.value for c in corr), -1.0)
        u = asm.solve()
        self.assertAlmostEqual(u[0], -0.5)
        self.assertAlmostEqual(u[1], 0.5)         # u1 = u0 + 1 recovered

    def test_solve_matches_direct_dense_solution(self):
        asm = Assembler(4)
        asm.apply_batch([("add",) + tuple(e) for e in spring_chain(3)])
        asm.set_constraints([Constraint(0, (), 0.0), Constraint(3, (), 0.5)])
        u = asm.solve()
        # hand-computed: free dofs 1,2; A_red=[[2,-1],[-1,2]], b_red=[1,1.5]
        self.assertAlmostEqual(u[0], 0.0)
        self.assertAlmostEqual(u[3], 0.5)
        self.assertAlmostEqual(u[1], 7.0 / 6.0)
        self.assertAlmostEqual(u[2], 8.0 / 6.0)
        # projected residual T^T (A u - b) vanishes
        rs = asm.constrained_system()
        for p in range(len(rs.free_dofs)):
            r = sum(rs.transform[i][p] * (
                sum(asm.matrix[i][j] * u[j] for j in range(4)) - asm.rhs[i])
                for i in range(4))
            self.assertAlmostEqual(r, 0.0)

    def test_constraint_change_rebuilds_with_new_version(self):
        elements = spring_chain(3)
        asm = Assembler(4)
        asm.apply_batch([("add",) + tuple(e) for e in elements])
        asm.set_constraints([Constraint(0, (), 0.0), Constraint(3, (), 0.5)])
        u1 = asm.solve()
        v1 = asm.constrained_system().version
        asm.set_constraints([Constraint(0, (), 0.0), Constraint(3, (), 1.0)])
        rs = asm.constrained_system()
        self.assertEqual(rs.version, v1 + 1)
        self.assertTrue(all(c.version == rs.version
                            for row in rs.matrix_provenance
                            for cell in row for c in cell))
        # matches a fresh, independent full assembly under the new constraint
        fresh = Assembler(4)
        fresh.apply_batch([("add",) + tuple(e) for e in elements])
        fresh.set_constraints([Constraint(0, (), 0.0), Constraint(3, (), 1.0)])
        for x, y in zip(asm.solve(), fresh.solve()):
            self.assertAlmostEqual(x, y)
        self.assertNotEqual(u1, asm.solve())

    def test_invalid_constraints_rejected_without_side_effects(self):
        asm = Assembler(3)
        version = asm.constraint_version
        for bad in [
            [Constraint(5, (), 0.0)],                    # dof out of range
            [Constraint(1, ((1, 2.0),), 0.0)],           # self reference
            [Constraint(1, (), 0.0), Constraint(1, (), 1.0)],  # twice
            [Constraint(1, ((2, 1.0),), 0.0), Constraint(2, (), 0.0)],  # chained
            [(1, ((0, "x"),), 0.0)],                     # non-numeric coef
        ]:
            with self.assertRaises(AssemblyError, msg=bad):
                asm.set_constraints(bad)
        self.assertEqual(asm.constraint_version, version)
        self.assertEqual(asm._constraints, {})


class TestDenseSolve(unittest.TestCase):
    def test_solve_dense(self):
        x = solve_dense([[2.0, 1.0], [1.0, 3.0]], [1.0, 2.0])
        self.assertAlmostEqual(x[0], 0.2)
        self.assertAlmostEqual(x[1], 0.6)
        with self.assertRaises(AssemblyError):
            solve_dense([[1.0, 2.0], [2.0, 4.0]], [1.0, 2.0])


if __name__ == "__main__":
    unittest.main()
