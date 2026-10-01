"""基数约束 CNF 编码的 unittest 测试套件。

运行: python3.11 -m unittest test_cardenc -v
所有小规模输入均与直接计数语义逐一比对（投影等价）。
"""

import unittest
from itertools import product

from cnf import (
    VarManager,
    brute_force_extensions,
    check_assignment,
    dpll,
)
from encoders import (
    AT_LEAST,
    AT_MOST,
    EXACTLY,
    SENSES,
    METHODS,
    Encoder,
    tighten_network,
)
from verify import (
    check_projection_equivalence,
    direct_check,
    direct_count,
    extend_assignment,
    propagation_metrics,
    solve_and_verify,
)


def make_encoder(n):
    vm = VarManager()
    enc = Encoder(vm)
    lits = [vm.new_var(f"x{i}") for i in range(1, n + 1)]
    return vm, enc, lits


class TestBoundarySemantics(unittest.TestCase):
    """k<0、k>n、k=0、k=n、n=0 等边界按语义化简，且与直接计数一致。"""

    def test_constant_cases(self):
        cases = [
            (AT_MOST, 3, -1, False), (AT_MOST, 0, -1, False),
            (AT_MOST, 3, 3, True), (AT_MOST, 3, 5, True), (AT_MOST, 0, 0, True),
            (AT_LEAST, 3, 0, True), (AT_LEAST, 3, -2, True), (AT_LEAST, 0, 0, True),
            (AT_LEAST, 3, 4, False), (AT_LEAST, 0, 1, False),
            (EXACTLY, 3, -1, False), (EXACTLY, 3, 4, False),
            (EXACTLY, 0, 0, True), (EXACTLY, 0, 1, False),
        ]
        for sense, n, k, expected in cases:
            for method in METHODS:
                with self.subTest(sense=sense, n=n, k=k, method=method):
                    _, enc, lits = make_encoder(n)
                    c = enc.encode(sense, lits, k, method=method)
                    self.assertEqual(c.constant, expected)
                    if expected:
                        self.assertEqual(c.num_clauses, 0)
                    rep = check_projection_equivalence(c, lits)
                    self.assertTrue(rep["equivalent"], rep["mismatches"][:1])

    def test_unit_simplification(self):
        for method in METHODS:
            _, enc, lits = make_encoder(4)
            c = enc.encode(AT_MOST, lits, 0, method=method)
            self.assertEqual(c.num_clauses, 4)
            self.assertEqual(c.num_aux, 0)
            self.assertTrue(all(len(cl) == 1 and cl[0] < 0 for cl in c.clauses))
            c = enc.encode(AT_LEAST, lits, 4, method=method)
            self.assertEqual(c.num_clauses, 4)
            self.assertTrue(all(len(cl) == 1 and cl[0] > 0 for cl in c.clauses))

    def test_n0_exactly0_satisfiable(self):
        for method in METHODS:
            _, enc, lits = make_encoder(0)
            c = enc.encode(EXACTLY, lits, 0, method=method)
            self.assertEqual(c.num_clauses, 0)
            self.assertTrue(direct_check(EXACTLY, lits, 0, {}))
            self.assertIsNotNone(dpll(c.clauses))


class TestExhaustiveEquivalence(unittest.TestCase):
    """所有小规模输入：任一原赋值满足基数条件 <=> 存在辅助赋值使 CNF 成立。"""

    def test_projection_equivalence_grid(self):
        for n in range(0, 7):
            for k in range(-2, n + 3):
                for sense in SENSES:
                    for method in METHODS:
                        with self.subTest(n=n, k=k, sense=sense, method=method):
                            _, enc, lits = make_encoder(n)
                            c = enc.encode(sense, lits, k, method=method)
                            rep = check_projection_equivalence(c, lits)
                            self.assertTrue(
                                rep["equivalent"],
                                f"{sense} n={n} k={k} {method}: "
                                f"{rep['mismatches'][:2]}",
                            )
                            self.assertEqual(rep["ok"], 2 ** n)

    def test_negative_literal_inputs(self):
        for method in METHODS:
            with self.subTest(method=method):
                _, enc, lits = make_encoder(4)
                mixed = [lits[0], -lits[1], lits[2], -lits[3]]
                c = enc.encode(EXACTLY, mixed, 2, method=method)
                rep = check_projection_equivalence(c, lits)
                self.assertTrue(rep["equivalent"], rep["mismatches"][:1])


class TestExtensionConstruction(unittest.TestCase):
    """原赋值 -> 辅助赋值的构造性扩展必须通过独立检查器。"""

    def test_extension_satisfies_cnf(self):
        for n in range(1, 6):
            for k in range(0, n + 1):
                for sense in SENSES:
                    for method in METHODS:
                        _, enc, lits = make_encoder(n)
                        c = enc.encode(sense, lits, k, method=method)
                        for values in product((False, True), repeat=n):
                            base = dict(zip(lits, values))
                            if not direct_check(sense, lits, k, base):
                                continue
                            full = extend_assignment(c, base)
                            with self.subTest(n=n, k=k, sense=sense, method=method):
                                self.assertTrue(
                                    check_assignment(c.clauses, full),
                                    f"扩展赋值未通过独立检查: {base}",
                                )

    def test_brute_force_agrees_with_dpll(self):
        for method in METHODS:
            _, enc, lits = make_encoder(4)
            c = enc.encode(EXACTLY, lits, 2, method=method)
            for values in product((False, True), repeat=4):
                base = dict(zip(lits, values))
                bf = brute_force_extensions(c.clauses, base, c.aux_vars) > 0
                dp = dpll(c.clauses, base) is not None
                self.assertEqual(bf, dp, f"{method} {base}")
                self.assertEqual(bf, direct_check(EXACTLY, lits, 2, base))


class TestIncremental(unittest.TestCase):
    """增量收紧界与假设机制撤回。"""

    def test_network_native_tightening(self):
        for k_old, k_new in ((3, 1), (3, 0), (4, 2), (2, 0)):
            with self.subTest(k_old=k_old, k_new=k_new):
                vm, enc, lits = make_encoder(6)
                act = vm.new_var("act")
                c = enc.encode(AT_MOST, lits, k_old, method="net", activation=act)
                clauses_before = c.num_clauses
                tighten_network(c, k_new)
                self.assertEqual(c.num_clauses, clauses_before + 1)
                self.assertEqual(c.k, k_new)
                rep = check_projection_equivalence(c, lits)
                self.assertTrue(rep["equivalent"], rep["mismatches"][:1])

    def test_tighten_rejects_invalid(self):
        _, enc, lits = make_encoder(4)
        c = enc.encode(AT_MOST, lits, 2, method="seq")
        with self.assertRaises(ValueError):
            tighten_network(c, 1)
        c = enc.encode(AT_MOST, lits, 2, method="net")
        with self.assertRaises(ValueError):
            tighten_network(c, 2)
        with self.assertRaises(ValueError):
            tighten_network(c, -1)

    def test_assumption_tighten_and_retract(self):
        vm, enc, lits = make_encoder(5)
        a_lo = vm.new_var("a_lo")
        a_hi = vm.new_var("a_hi")
        c_lo = enc.encode(AT_MOST, lits, 2, method="seq", activation=a_lo)
        c_hi = enc.encode(AT_MOST, lits, 1, method="seq", activation=a_hi)
        clauses = c_lo.clauses + c_hi.clauses
        for values in product((False, True), repeat=5):
            base = dict(zip(lits, values))
            count = direct_count(lits, base)
            sat_tight = dpll(clauses, {**base, a_lo: False, a_hi: True}) is not None
            self.assertEqual(sat_tight, count <= 1, f"收紧 {base}")
            sat_back = dpll(clauses, {**base, a_lo: True, a_hi: False}) is not None
            self.assertEqual(sat_back, count <= 2, f"撤回 {base}")
            self.assertIsNotNone(dpll(clauses, {**base, a_lo: False, a_hi: False}))

    def test_activation_isolates_constraint(self):
        vm, enc, lits = make_encoder(4)
        act = vm.new_var("act")
        c = enc.encode(EXACTLY, lits, 1, method="net", activation=act)
        for values in product((False, True), repeat=4):
            base = dict(zip(lits, values))
            self.assertIsNotNone(dpll(c.clauses, {**base, act: False}))


class TestVarNumbering(unittest.TestCase):
    """辅助变量编号与命名不冲突。"""

    def test_aux_vars_disjoint_and_fresh(self):
        vm, enc, lits = make_encoder(5)
        c1 = enc.encode(AT_MOST, lits, 2, method="seq")
        c2 = enc.encode(AT_MOST, lits, 2, method="net")
        c3 = enc.encode(EXACTLY, lits, 3, method="seq")
        original = set(lits)
        for c in (c1, c2, c3):
            self.assertTrue(original.isdisjoint(c.aux_vars))
            self.assertTrue(all(v > max(original) for v in c.aux_vars))
        all_aux = c1.aux_vars + c2.aux_vars + c3.aux_vars
        self.assertEqual(len(all_aux), len(set(all_aux)), "辅助变量编号冲突")

    def test_names_unique(self):
        vm, enc, lits = make_encoder(5)
        enc.encode(AT_MOST, lits, 2, method="seq")
        enc.encode(AT_MOST, lits, 2, method="net")
        names = [vm.name(v) for v in range(1, vm.num_vars + 1)]
        self.assertEqual(len(names), len(set(names)), "变量命名冲突")

    def test_var_manager_start_offset(self):
        vm = VarManager(start=100)
        enc = Encoder(vm)
        lits = [vm.new_var() for _ in range(3)]
        c = enc.encode(AT_MOST, lits, 1, method="net")
        self.assertTrue(all(v >= 100 for v in lits + c.aux_vars))
        vm.ensure(500)
        self.assertGreaterEqual(vm.new_var(), 501)


class TestClauseCounts(unittest.TestCase):
    """锁定若干已知小规模编码的子句数（回归保护）。"""

    def test_known_counts(self):
        _, enc, lits = make_encoder(3)
        c = enc.encode(AT_MOST, lits, 1, method="seq")
        self.assertEqual(c.num_clauses, 5)
        self.assertEqual(c.num_aux, 2)
        _, enc, lits = make_encoder(2)
        c = enc.encode(AT_MOST, lits, 1, method="net")
        self.assertEqual(c.num_clauses, 7)
        self.assertEqual(c.num_aux, 2)

    def test_seq_clause_formula(self):
        # Sinz 顺序计数器（0<k<n）：子句 2k(n-2)+n+k-1，寄存器 (n-1)k
        for n, k in ((3, 1), (8, 3), (6, 2)):
            _, enc, lits = make_encoder(n)
            c = enc.encode(AT_MOST, lits, k, method="seq")
            self.assertEqual(c.num_clauses, 2 * k * (n - 2) + n + k - 1)
            self.assertEqual(c.num_aux, (n - 1) * k)

    def test_network_gate_structure(self):
        # 全比较器：每个 OR/AND 门恰好 3 条子句，外加 1 条输出单位子句
        for n, k in ((2, 1), (8, 3), (6, 3), (8, 1)):
            _, enc, lits = make_encoder(n)
            c = enc.encode(AT_MOST, lits, k, method="net")
            gates = sum(1 for d in c.definitions if d[1] in ("or", "and"))
            self.assertEqual(gates, c.num_aux)
            self.assertEqual(c.num_clauses, 3 * gates + 1)


class TestPropagation(unittest.TestCase):
    """传播能力：UP 冲突检出与强制值检出。"""

    def test_at_most_conflict_detection_complete(self):
        for method in METHODS:
            for k in (1, 2):
                with self.subTest(method=method, k=k):
                    _, enc, lits = make_encoder(4)
                    c = enc.encode(AT_MOST, lits, k, method=method)
                    m = propagation_metrics(c, lits)
                    self.assertGreater(m["inconsistent"], 0)
                    self.assertEqual(
                        m["up_conflict"], m["inconsistent"],
                        f"{method} k={k}: UP 未能检出全部冲突 {m}",
                    )

    def test_metrics_ranges(self):
        results = {}
        for method in METHODS:
            _, enc, lits = make_encoder(4)
            c = enc.encode(EXACTLY, lits, 2, method=method)
            m = propagation_metrics(c, lits)
            self.assertEqual(m["partial"], 3 ** 4)
            self.assertLessEqual(m["up_conflict"], m["inconsistent"])
            self.assertLessEqual(m["up_forced"], m["forced"])
            results[method] = m
        for method, m in results.items():
            self.assertGreater(m["up_forced"], 0, f"{method}: {m}")


class TestSolveAndVerify(unittest.TestCase):
    """声称有解时必须用独立检查器验证原约束。"""

    def test_solve_and_independent_verify(self):
        for sense in SENSES:
            for method in METHODS:
                with self.subTest(sense=sense, method=method):
                    _, enc, lits = make_encoder(6)
                    c = enc.encode(sense, lits, 3, method=method)
                    projection, verified = solve_and_verify(c, lits)
                    self.assertIsNotNone(projection)
                    self.assertTrue(verified)

    def test_unsat_claim(self):
        for method in METHODS:
            _, enc, lits = make_encoder(3)
            c = enc.encode(AT_LEAST, lits, 4, method=method)
            projection, _ = solve_and_verify(c, lits)
            self.assertIsNone(projection)


if __name__ == "__main__":
    unittest.main()
