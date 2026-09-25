"""unittest suite for the dependency-scheduled sparse triangular solver."""

import random
import unittest

from trisolve import (
    LOWER,
    UPPER,
    MissingPivotError,
    StructureError,
    TriangularMatrix,
    TriangularSolver,
    ZeroPivotError,
)
from trisolve.analysis import analyze
from trisolve.dense import dense_matvec, dense_solve, residual_norm


def build_matrix(n, entries, uplo=LOWER, unit=False, index_base=0):
    rows = [e[0] for e in entries]
    cols = [e[1] for e in entries]
    vals = [e[2] for e in entries]
    return TriangularMatrix(
        n, rows, cols, vals,
        uplo=uplo, unit_diagonal=unit, index_base=index_base,
    )


# A 4x4 lower matrix whose edges produce non-trivial level waves:
# row 0: pivot
# row 1: edge (1,0)
# row 2: edge (2,1)
# row 3: edges (3,0) and (3,2) -> depends on 0 and 2
HAND_ENTRIES = [
    (0, 0, 2.0),
    (1, 0, 1.0), (1, 1, 4.0),
    (2, 1, 2.0), (2, 2, -1.0),
    (3, 0, -1.0), (3, 2, 3.0), (3, 3, 5.0),
]


class StructureTests(unittest.TestCase):
    def test_duplicate_coordinates_are_merged(self):
        m = build_matrix(2, [
            (0, 0, 1.0), (0, 0, 2.0),
            (1, 0, 0.5), (1, 0, 0.25), (1, 1, 3.0),
        ])
        self.assertEqual(m.value(0, 0), 3.0)
        self.assertAlmostEqual(m.value(1, 0), 0.75)
        coords = [(e.row, e.col) for e in m.entries]
        self.assertEqual(len(coords), len(set(coords)))

    def test_one_based_indices(self):
        m = build_matrix(1, [(1, 1, 7.0)], index_base=1)
        self.assertEqual(m.value(0, 0), 7.0)

    def test_out_of_range_index_rejected(self):
        with self.assertRaises(StructureError):
            build_matrix(2, [(2, 2, 1.0), (0, 0, 1.0)], index_base=0)
        with self.assertRaises(StructureError):
            build_matrix(2, [(0, 0, 1.0)], index_base=1)

    def test_out_of_triangle_entries_rejected_not_ignored(self):
        with self.assertRaises(StructureError):
            build_matrix(2, [(0, 0, 1.0), (0, 1, 1.0), (1, 1, 1.0)], uplo=LOWER)
        with self.assertRaises(StructureError):
            build_matrix(2, [(0, 0, 1.0), (1, 0, 1.0), (1, 1, 1.0)], uplo=UPPER)

    def test_missing_pivot_is_specific_error(self):
        with self.assertRaises(MissingPivotError) as ctx:
            build_matrix(3, [(0, 0, 1.0), (1, 0, 1.0), (2, 2, 1.0)])
        self.assertEqual(ctx.exception.missing, [1])
        self.assertIsInstance(ctx.exception, StructureError)

    def test_unit_diagonal_rejects_explicit_diagonal(self):
        with self.assertRaises(StructureError):
            build_matrix(2, [(0, 0, 1.0)], uplo=LOWER, unit=True)

    def test_unit_diagonal_allows_empty(self):
        m = build_matrix(2, [(1, 0, 5.0)], uplo=LOWER, unit=True)
        self.assertEqual(m.diagonal_value(0), 1.0)
        self.assertEqual(m.diagonal_value(1), 1.0)

    def test_dimension_mismatch(self):
        from trisolve.errors import DimensionError
        m = build_matrix(2, [(0, 0, 1.0), (1, 1, 1.0)])
        with self.assertRaises(DimensionError):
            TriangularSolver(m).solve([1.0])

    def test_empty_system(self):
        m = build_matrix(0, [])
        self.assertEqual(TriangularSolver(m).solve([]), [])
        self.assertEqual(TriangularSolver(m).solve_multi([]), [])


class ScheduleTests(unittest.TestCase):
    def test_hand_levels_and_evidence(self):
        m = build_matrix(4, HAND_ENTRIES)
        sched = analyze(m, transpose=False)
        # Levels: 0 -> {0}, 1 -> {1}, 2 -> {2}, 3 -> {3}
        self.assertEqual(sched.levels, [[0], [1], [2], [3]])
        self.assertEqual(
            [d.depends_on for d in sched.dependency_evidence(3)], [0, 2]
        )
        self.assertEqual(
            [d.coefficient_position for d in sched.dependency_evidence(3)],
            [(3, 0), (3, 2)],
        )

    def test_parallel_level_when_independent(self):
        # Both 1 and 2 depend only on 0 -> they share a level.
        entries = [
            (0, 0, 1.0),
            (1, 0, 1.0), (1, 1, 1.0),
            (2, 0, 1.0), (2, 2, 1.0),
        ]
        m = build_matrix(3, entries)
        sched = analyze(m)
        self.assertEqual(sched.levels, [[0], [1, 2]])

    def test_schedule_depends_on_graph_not_storage_order(self):
        # Coordinates deliberately supplied "back to front" in storage.
        shuffled = [HAND_ENTRIES[i] for i in (7, 6, 5, 4, 3, 2, 1, 0)]
        m = build_matrix(4, shuffled)
        sched = analyze(m)
        self.assertEqual(sched.levels, [[0], [1], [2], [3]])

    def test_lower_transpose_is_treated_as_upper(self):
        m = build_matrix(4, HAND_ENTRIES, uplo=LOWER)
        sched = analyze(m, transpose=True)
        self.assertEqual(sched.uplo_effective, UPPER)
        # L^T: node 0 waits for 1 and 3; node 1 waits for 2; node 2 waits for 3.
        self.assertEqual(sorted(d.depends_on for d in sched.dependency_evidence(0)), [1, 3])
        self.assertEqual([d.depends_on for d in sched.dependency_evidence(1)], [2])
        self.assertEqual([d.depends_on for d in sched.dependency_evidence(2)], [3])
        self.assertEqual(sched.levels[0], [3])

    def test_native_upper_schedule_matches_transposed_lower(self):
        # L^T is upper triangular with the same numeric pattern; the schedule
        # of solving L^T x = b must equal solving U x = b for U = L^T.
        lower = build_matrix(4, HAND_ENTRIES, uplo=LOWER)
        upper_entries = [(c, r, v) for (r, c, v) in HAND_ENTRIES]
        upper = build_matrix(4, upper_entries, uplo=UPPER)
        self.assertEqual(analyze(lower, transpose=True).levels, analyze(upper).levels)
        self.assertEqual(analyze(lower).levels, analyze(upper, transpose=True).levels)


class NumericTests(unittest.TestCase):
    def test_hand_lower(self):
        m = build_matrix(4, HAND_ENTRIES)
        b = [2.0, 5.0, 0.0, 8.0]
        x = TriangularSolver(m).solve(b)
        self.assertLessEqual(residual_norm(m, x, b), 1e-12)
        for got, want in zip(x, dense_solve(m, b)):
            self.assertAlmostEqual(got, want, places=12)

    def test_lower_transpose_matches_dense(self):
        m = build_matrix(4, HAND_ENTRIES)
        b = [1.5, -2.0, 3.0, 0.25]
        x = TriangularSolver(m).solve(b, transpose=True)
        self.assertLessEqual(residual_norm(m, x, b, transpose=True), 1e-12)
        for got, want in zip(x, dense_solve(m, b, transpose=True)):
            self.assertAlmostEqual(got, want, places=12)

    def test_upper_native_and_transpose(self):
        upper_entries = [(c, r, v) for (r, c, v) in HAND_ENTRIES]
        m = build_matrix(4, upper_entries, uplo=UPPER)
        b = [1.0, 2.0, 3.0, 4.0]
        for tr in (False, True):
            with self.subTest(transpose=tr):
                x = TriangularSolver(m).solve(b, transpose=tr)
                self.assertLessEqual(residual_norm(m, x, b, transpose=tr), 1e-12)
                want = dense_solve(m, b, transpose=tr)
                for got, w in zip(x, want):
                    self.assertAlmostEqual(got, w, places=12)

    def test_unit_diagonal_solve(self):
        m = build_matrix(4, [(1, 0, 2.0), (2, 1, -1.0), (3, 0, 0.5), (3, 2, 1.0)], unit=True)
        b = [3.0, 1.0, 2.0, 0.0]
        x = TriangularSolver(m).solve(b)
        self.assertLessEqual(residual_norm(m, x, b), 1e-12)

    def test_multi_rhs_share_analysis(self):
        m = build_matrix(4, HAND_ENTRIES)
        solver = TriangularSolver(m)
        rhs = [[1.0, 0.0, 0.0, 0.0], [0.0, 1.0, 0.0, 0.0], [1.0, 2.0, 3.0, 4.0]]
        X = solver.solve_multi(rhs)
        for b, x in zip(rhs, X):
            self.assertLessEqual(residual_norm(m, x, b), 1e-12)
        self.assertEqual(solver.stats.analyses_performed, 1)
        self.assertEqual(solver.stats.right_hand_sides, 3)


class FailureTests(unittest.TestCase):
    def test_zero_pivot_is_distinct_from_missing_pivot(self):
        m = build_matrix(2, [(0, 0, 1.0), (1, 0, 1.0), (1, 1, 0.0)])
        with self.assertRaises(ZeroPivotError) as ctx:
            TriangularSolver(m).solve([1.0, 1.0])
        self.assertEqual(ctx.exception.pivots, [1])
        self.assertNotIsInstance(ctx.exception, MissingPivotError)

    def test_near_zero_pivot_tolerance(self):
        m = build_matrix(1, [(0, 0, 1e-16)])
        with self.assertRaises(ZeroPivotError):
            TriangularSolver(m, pivot_tol=1e-12).solve([1.0])

    def test_no_half_solution_published(self):
        m = build_matrix(3, [
            (0, 0, 2.0),
            (1, 0, 1.0), (1, 1, 0.0),   # node 1 dead
            (2, 1, 1.0), (2, 2, 4.0),
        ])
        original_b = [8.0, 3.0, 7.0]
        b = list(original_b)
        with self.assertRaises(ZeroPivotError):
            TriangularSolver(m).solve(b)
        # New list API never mutated the input...
        self.assertEqual(b, original_b)
        # ...and solve_into leaves its argument untouched on failure.
        with self.assertRaises(ZeroPivotError):
            TriangularSolver(m).solve_into(b)
        self.assertEqual(b, original_b)

    def test_zero_pivot_all_detected_up_front(self):
        m = build_matrix(3, [
            (0, 0, 0.0),
            (1, 0, 1.0), (1, 1, 0.0),
            (2, 1, 1.0), (2, 2, 4.0),
        ])
        with self.assertRaises(ZeroPivotError) as ctx:
            TriangularSolver(m).solve([1.0, 1.0, 1.0])
        self.assertEqual(ctx.exception.pivots, [0, 1])


class ReuseTests(unittest.TestCase):
    def test_value_change_keeps_analysis(self):
        m = build_matrix(4, HAND_ENTRIES)
        solver = TriangularSolver(m)
        solver.solve([1.0, 1.0, 1.0, 1.0])
        token = m.structure_token
        m.set_value(3, 2, 9.0)
        self.assertEqual(m.structure_token, token)
        solver.solve([1.0, 0.0, 0.0, 1.0])
        self.assertEqual(solver.stats.analyses_performed, 1)
        self.assertEqual(solver.stats.analyses_reused, 1)
        self.assertEqual(solver.stats.reuse_rate, 0.5)

    def test_structure_change_locally_invalidates(self):
        m = build_matrix(4, HAND_ENTRIES)
        solver = TriangularSolver(m)
        solver.solve([1.0, 1.0, 1.0, 1.0])
        solver.solve([1.0, 1.0, 1.0, 1.0], transpose=True)
        self.assertEqual(solver.cached_schedules(), 2)
        m.add_entry(2, 0, 0.5)
        solver.solve([1.0, 1.0, 1.0, 1.0])
        # Both old schedules were stale; one new analysis exists.
        self.assertEqual(solver.cached_schedules(), 1)
        self.assertEqual(solver.stats.analyses_performed, 3)
        # One structural change evicted the old (identity + transpose) pair.
        self.assertEqual(solver.stats.local_invalidations, 1)
        # The added edge gives node 2 an extra dependency and still solves.
        b = [2.0, 3.0, 4.0, 5.0]
        x = solver.solve(b)
        self.assertLessEqual(residual_norm(m, x, b), 1e-12)

    def test_remove_entry_then_reanalyze(self):
        m = build_matrix(3, [
            (0, 0, 1.0),
            (1, 0, 1.0), (1, 1, 2.0),
            (2, 0, 1.0), (2, 1, 1.0), (2, 2, 3.0),
        ])
        solver = TriangularSolver(m)
        sched_before = solver.analyze()
        self.assertEqual(sched_before.levels, [[0], [1], [2]])
        m.remove_entry(2, 1)
        sched_after = solver.analyze()
        self.assertEqual(sched_after.levels, [[0], [1, 2]])


class RandomizedCrossCheck(unittest.TestCase):
    def _random_matrix(self, rng, n, uplo, unit, transpose):
        entries = []
        for i in range(n):
            if not unit:
                pivot = rng.choice((-1.0, 1.0)) * rng.uniform(0.5, 5.0)
                entries.append((i, i, pivot))
            for j in range(i):
                if rng.random() < 0.4:
                    r, c = (i, j) if uplo == LOWER else (j, i)
                    entries.append((r, c, rng.uniform(-3.0, 3.0)))
        rng.shuffle(entries)  # storage order must not matter
        m = build_matrix(n, entries, uplo=uplo, unit=unit)
        b = [rng.uniform(-5.0, 5.0) for _ in range(n)]
        x = TriangularSolver(m).solve(b, transpose=transpose)
        self.assertLessEqual(residual_norm(m, x, b, transpose=transpose), 1e-9)
        want = dense_solve(m, b, transpose=transpose)
        for got, w in zip(x, want):
            self.assertAlmostEqual(got, w, places=9)

    def test_random_cases(self):
        rng = random.Random(20260925)
        for n in (1, 2, 5, 8, 12):
            for uplo in (LOWER, UPPER):
                for unit in (False, True):
                    for tr in (False, True):
                        with self.subTest(n=n, uplo=uplo, unit=unit, tr=tr):
                            self._random_matrix(rng, n, uplo, unit, tr)


if __name__ == "__main__":
    unittest.main(verbosity=2)
