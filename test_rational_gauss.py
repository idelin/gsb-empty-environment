"""rational_gauss 的单元测试（标准库 unittest）。"""

import unittest
from fractions import Fraction

from rational_gauss import (
    Operation,
    matvec,
    rank,
    rref,
    solve,
    to_fraction,
)


def F(*values):
    return [Fraction(v) for v in values]


class TestToFraction(unittest.TestCase):
    def test_int_and_string(self):
        self.assertEqual(to_fraction(3), Fraction(3))
        self.assertEqual(to_fraction("3/2"), Fraction(3, 2))
        self.assertEqual(to_fraction("-4"), Fraction(-4))
        self.assertEqual(to_fraction(Fraction(7, 9)), Fraction(7, 9))

    def test_rejects_float(self):
        with self.assertRaises(TypeError):
            to_fraction(0.5)
        with self.assertRaises(TypeError):
            to_fraction(True)


class TestRank(unittest.TestCase):
    def test_full_rank(self):
        self.assertEqual(rank([[1, 2], [3, 4]]), 2)

    def test_rank_deficient(self):
        self.assertEqual(rank([[1, 2, 3], [4, 5, 6], [7, 8, 9]]), 2)

    def test_zero_matrix(self):
        self.assertEqual(rank([[0, 0], [0, 0]]), 0)

    def test_fraction_matrix(self):
        self.assertEqual(rank([["1/3", "1/6"], ["2/3", "1/3"]]), 1)


class TestRREF(unittest.TestCase):
    def test_rref_form(self):
        result = rref([[2, 1, -1], [-3, -1, 2], [-2, 1, 2]])
        self.assertEqual(result.rank, 3)
        for i, row in enumerate(result.matrix):
            self.assertEqual(row, [Fraction(int(j == i)) for j in range(3)])

    def test_trace_has_domain_for_every_step(self):
        result = rref([[1, 2], [3, 4]])
        self.assertTrue(result.operations)
        for op in result.operations:
            self.assertIsInstance(op, Operation)
            self.assertTrue(op.domain, "每步变换必须注明适用域")
            self.assertTrue(op.description)

    def test_column_swap_recorded(self):
        result = rref([[0, 0, 2], [0, 3, 1]], column_pivoting=True)
        kinds = [op.kind for op in result.operations]
        self.assertIn("col_swap", kinds)
        self.assertEqual(sorted(result.column_permutation), [0, 1, 2])


class TestSolveUnique(unittest.TestCase):
    def test_unique_solution(self):
        sol = solve([[2, 1, -1], [-3, -1, 2], [-2, 1, 2]], [8, -11, -3])
        self.assertTrue(sol.consistent)
        self.assertTrue(sol.unique)
        self.assertEqual(sol.particular, F(2, 3, -1))
        self.assertEqual(sol.nullspace_basis, [])
        self.assertEqual(sol.rank_a, 3)

    def test_fraction_solution_exact(self):
        # 3x = 1 的解必须恰好是 1/3，而不是 0.333...
        sol = solve([[3]], [1])
        self.assertEqual(sol.particular, [Fraction(1, 3)])
        self.assertIsInstance(sol.particular[0], Fraction)

    def test_fraction_coefficients(self):
        sol = solve([["1/3", "1/2"], ["1/4", "-1/5"]], ["5/6", "1/20"])
        self.assertEqual(sol.particular, F(1, 1))
        self.assertEqual(matvec([["1/3", "1/2"], ["1/4", "-1/5"]],
                                sol.particular), F("5/6", "1/20"))


class TestSolveUnderdetermined(unittest.TestCase):
    def test_particular_and_nullspace(self):
        A = [[1, 1, 1], [1, -1, 1]]
        b = [6, 2]
        sol = solve(A, b)
        self.assertTrue(sol.consistent)
        self.assertFalse(sol.unique)
        self.assertEqual(sol.particular, F(4, 2, 0))
        self.assertEqual(len(sol.nullspace_basis), 1)
        self.assertEqual(sol.nullspace_basis[0], F(-1, 0, 1))
        # 验证：A*x_p = b，A*v = 0
        self.assertEqual(matvec(A, sol.particular), F(6, 2))
        self.assertEqual(matvec(A, sol.nullspace_basis[0]), F(0, 0))

    def test_nullspace_dimension(self):
        A = [[1, 2, 3], [4, 5, 6], [7, 8, 9]]
        sol = solve(A, [1, 2, 3])
        self.assertTrue(sol.consistent)
        self.assertEqual(len(sol.nullspace_basis),
                         sol.num_variables - sol.rank_a)  # 零化度 = n - rank
        for vec in sol.nullspace_basis:
            self.assertEqual(matvec(A, vec), F(0, 0, 0))


class TestSolveInconsistent(unittest.TestCase):
    def test_zero_equals_one_evidence(self):
        sol = solve([[1, 1], [2, 2], [1, -1]], [1, 5, 0])
        self.assertFalse(sol.consistent)
        self.assertIsNone(sol.particular)
        self.assertEqual(sol.nullspace_basis, [])
        text = "\n".join(sol.inconsistency_evidence)
        self.assertIn("0 = 1", text)          # 0 = 1 型矛盾行
        self.assertIn("rank(A) = 2 < rank([A|b]) = 3", text)

    def test_parallel_lines(self):
        sol = solve([[1, 1], [1, 1]], [1, 2])
        self.assertFalse(sol.consistent)
        self.assertTrue(any("0 = 1" in e for e in sol.inconsistency_evidence))


class TestColumnSwapRestoresOrder(unittest.TestCase):
    A = [[0, 0, 2], [0, 3, 1]]
    b = [4, 5]

    def test_variable_order_restored(self):
        sol = solve(self.A, self.b, column_pivoting=True)
        self.assertTrue(sol.consistent)
        # 原变量顺序下的特解：x1 自由取 0，x2 = 1，x3 = 2
        self.assertEqual(sol.particular, F(0, 1, 2))
        self.assertEqual(sol.free_variables, [0])
        self.assertEqual(sol.pivot_variables, [1, 2])
        self.assertEqual(matvec(self.A, sol.particular), F(4, 5))
        for vec in sol.nullspace_basis:
            self.assertEqual(matvec(self.A, vec), F(0, 0))

    def test_swap_steps_logged(self):
        sol = solve(self.A, self.b, column_pivoting=True)
        swaps = [op for op in sol.operations if op.kind == "col_swap"]
        self.assertTrue(swaps)
        for op in swaps:
            self.assertIn("置换", op.domain + op.description)

    def test_no_swap_mode_agrees(self):
        # 不做列交换时，解必须与做列交换并还原后一致
        plain = solve(self.A, self.b)
        swapped = solve(self.A, self.b, column_pivoting=True)
        self.assertEqual(plain.particular, swapped.particular)
        self.assertEqual(plain.nullspace_basis, swapped.nullspace_basis)


class TestEdgeCases(unittest.TestCase):
    def test_zero_rhs(self):
        sol = solve([[1, 2], [3, 6]], [0, 0])
        self.assertTrue(sol.consistent)
        self.assertEqual(sol.particular, F(0, 0))
        self.assertEqual(len(sol.nullspace_basis), 1)
        self.assertEqual(matvec([[1, 2], [3, 6]], sol.nullspace_basis[0]), F(0, 0))

    def test_ragged_matrix_rejected(self):
        with self.assertRaises(ValueError):
            solve([[1, 2], [3]], [1, 2])

    def test_b_length_mismatch(self):
        with self.assertRaises(ValueError):
            solve([[1, 2]], [1, 2])


if __name__ == "__main__":
    unittest.main(verbosity=2)
