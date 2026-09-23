"""rational_gauss 的单元测试（Python 3.11，标准库 unittest）。"""

import unittest
from fractions import Fraction as F

import rational_gauss as rg


class TestRank(unittest.TestCase):
    def test_full_rank(self):
        self.assertEqual(rg.rank([[1, 2], [3, 4]]), 2)

    def test_rank_deficient(self):
        self.assertEqual(rg.rank([[1, 2, 3], [2, 4, 6], [1, 1, 1]]), 2)

    def test_zero_matrix(self):
        self.assertEqual(rg.rank([[0, 0], [0, 0]]), 0)

    def test_fraction_entries(self):
        # 第二行恰为第一行的 2 倍（分数精确比较，不受浮点误差影响）
        self.assertEqual(rg.rank([[F(1, 3), F(1, 2)], [F(2, 3), 1]]), 1)

    def test_tall_matrix(self):
        self.assertEqual(rg.rank([[1, 0], [0, 1], [1, 1], [2, 2]]), 2)


class TestRrefStructure(unittest.TestCase):
    def test_pivot_structure(self):
        res = rg.rref([[1, 2, 3], [4, 5, 6], [7, 8, 9]])
        self.assertEqual(res.rank, 2)
        self.assertEqual(res.pivot_cols, [0, 1])
        for k, c in enumerate(res.pivot_cols):
            self.assertEqual(res.rref[k][c], F(1))  # 主元为 1
            for i, row in enumerate(res.rref):
                if i != k:
                    self.assertEqual(row[c], F(0))  # 主元列其余位置为 0

    def test_entries_are_fractions(self):
        res = rg.rref([[2, 4], [1, 3]])
        for row in res.rref:
            for v in row:
                self.assertIsInstance(v, F)


class TestUniqueSolution(unittest.TestCase):
    def test_integer_system(self):
        sol = rg.solve(
            [[2, 1, -1], [-3, -1, 2], [-2, 1, 2]],
            [8, -11, -3],
        )
        self.assertTrue(sol.consistent)
        self.assertEqual(sol.rank, 3)
        self.assertEqual(sol.particular, [F(2), F(3), F(-1)])
        self.assertEqual(sol.nullspace_basis, [])

    def test_fraction_system_exact(self):
        # (1/2)x + y = 1, x - y = 1  =>  x = 4/3, y = 1/3（精确分数，非浮点）
        sol = rg.solve([[F(1, 2), 1], [1, -1]], [1, 1])
        self.assertTrue(sol.consistent)
        self.assertEqual(sol.particular, [F(4, 3), F(1, 3)])
        self.assertEqual(rg.matvec([[F(1, 2), 1], [1, -1]], sol.particular),
                         [F(1), F(1)])

    def test_string_input_accepted(self):
        sol = rg.solve([["1/2", 1], [1, -1]], [1, 1])
        self.assertEqual(sol.particular, [F(4, 3), F(1, 3)])


class TestUnderdetermined(unittest.TestCase):
    def setUp(self):
        self.A = [[1, 2, -1, 1], [2, 4, 1, 5], [3, 6, 0, 6]]
        self.b = [2, 7, 9]
        self.sol = rg.solve(self.A, self.b)

    def test_rank_and_nullity(self):
        self.assertTrue(self.sol.consistent)
        self.assertEqual(self.sol.rank, 2)
        # 零空间维数 = n - rank
        self.assertEqual(len(self.sol.nullspace_basis), 4 - 2)

    def test_particular_satisfies_system(self):
        self.assertEqual(rg.matvec(self.A, self.sol.particular),
                         [F(v) for v in self.b])

    def test_nullspace_basis_in_kernel(self):
        for vec in self.sol.nullspace_basis:
            self.assertEqual(rg.matvec(self.A, vec), [F(0)] * 3)

    def test_nullspace_basis_independent(self):
        self.assertEqual(
            rg.rank(self.sol.nullspace_basis), len(self.sol.nullspace_basis)
        )

    def test_general_solution_family(self):
        # 任取 t1, t2，通解仍满足方程组
        t1, t2 = F(7, 3), F(-2, 5)
        x = [
            self.sol.particular[j]
            + t1 * self.sol.nullspace_basis[0][j]
            + t2 * self.sol.nullspace_basis[1][j]
            for j in range(4)
        ]
        self.assertEqual(rg.matvec(self.A, x), [F(v) for v in self.b])


class TestInconsistent(unittest.TestCase):
    def test_zero_equals_one_evidence(self):
        sol = rg.solve([[1, 1], [2, 2], [1, -1]], [1, 3, 0])
        self.assertFalse(sol.consistent)
        self.assertIsNone(sol.particular)
        self.assertEqual(sol.nullspace_basis, [])
        self.assertTrue(sol.evidence, "应给出不相容证据行")
        for row in sol.evidence:
            self.assertTrue(all(v == 0 for v in row[:-1]))  # 系数全 0
            self.assertNotEqual(row[-1], 0)                 # 右端非 0，即 0 = c

    def test_fraction_inconsistency(self):
        sol = rg.solve([[F(1, 2)], [1]], [1, 1])
        self.assertFalse(sol.consistent)
        # 消元后得到证据行 [0 | -1]，即 0 = -1，同样构成矛盾
        self.assertEqual(sol.evidence[0][-1], F(-1))


class TestColumnSwap(unittest.TestCase):
    def test_variable_order_restored(self):
        # 首列全零，触发列交换；x1 为自由变量
        A = [[0, 0, 1], [0, 1, 1], [0, 1, -1]]
        b = [1, 2, 0]
        sol = rg.solve(A, b)
        self.assertTrue(sol.consistent)
        self.assertEqual(sol.rank, 2)
        # 原始变量顺序：x1 自由取 0，x2 = 1，x3 = 1
        self.assertEqual(sol.particular, [F(0), F(1), F(1)])
        self.assertEqual(sol.nullspace_basis, [[F(1), F(0), F(0)]])
        self.assertEqual(rg.matvec(A, sol.particular), [F(1), F(2), F(0)])

    def test_column_swap_in_trace(self):
        sol = rg.solve([[0, 2], [0, 1]], [4, 2])
        ops = [entry.operation for entry in sol.trace]
        self.assertIn("列交换", ops)
        # 还原后：x1 自由，x2 = 2
        self.assertEqual(sol.particular, [F(0), F(2)])
        self.assertEqual(sol.nullspace_basis, [[F(1), F(0)]])

    def test_swap_then_verify_against_original(self):
        # 随机化风格：列交换后仍须满足“原始”方程组
        A = [[0, 3, 1], [0, 1, 1], [0, 2, -1]]
        b = [7, 3, 3]
        sol = rg.solve(A, b)
        self.assertTrue(sol.consistent)
        self.assertEqual(rg.matvec(A, sol.particular), [F(v) for v in b])
        for vec in sol.nullspace_basis:
            self.assertEqual(rg.matvec(A, vec), [F(0)] * 3)


class TestTrace(unittest.TestCase):
    def test_every_step_states_domain(self):
        sol = rg.solve(
            [[2, 1, -1], [-3, -1, 2], [-2, 1, 2]],
            [8, -11, -3],
        )
        self.assertTrue(sol.trace, "应记录消元轨迹")
        for entry in sol.trace:
            self.assertTrue(entry.detail)
            self.assertTrue(entry.domain.startswith("适用域"))

    def test_operations_are_elementary(self):
        sol = rg.solve([[0, 2], [3, 1]], [4, 5])
        for entry in sol.trace:
            self.assertIn(entry.operation, {"行交换", "行倍乘", "行倍加", "列交换"})


class TestNoFloat(unittest.TestCase):
    def test_float_input_rejected(self):
        with self.assertRaises(TypeError):
            rg.solve([[0.5, 1]], [1])

    def test_float_in_b_rejected(self):
        with self.assertRaises(TypeError):
            rg.solve([[1]], [0.5])

    def test_bool_rejected(self):
        with self.assertRaises(TypeError):
            rg.to_fraction(True)


class TestValidation(unittest.TestCase):
    def test_ragged_matrix_rejected(self):
        with self.assertRaises(ValueError):
            rg.solve([[1, 2], [3]], [1, 2])

    def test_b_length_mismatch_rejected(self):
        with self.assertRaises(ValueError):
            rg.solve([[1, 2]], [1, 2])


if __name__ == "__main__":
    unittest.main(verbosity=2)
