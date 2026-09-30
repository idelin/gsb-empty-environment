import random
import unittest

from lexprop import (LexConstraint, MSG_NO_FIXPOINT, Solver, Var,
                     check_assignment, enumerate_supported_values)


def propagate_only(domains, constraints, budget=100_000):
    """只传播不搜索，返回 (Solver, 是否OK)。"""
    s = Solver(domains, constraints, budget_steps=budget)
    try:
        s.propagate()
        return s, True
    except Exception:
        return s, False


class TestBasicPropagation(unittest.TestCase):
    def test_ground_strict_vs_nonstrict(self):
        x, y = Var("x"), Var("y")
        s, ok = propagate_only({x: {0}, y: {0}}, [LexConstraint([x], [y])])
        self.assertTrue(ok)                       # 相同向量满足非严格
        s, ok = propagate_only({x: {0}, y: {0}}, [LexConstraint([x], [y], strict=True)])
        self.assertFalse(ok)                      # 相同向量不满足严格

    def test_prefix_equality_and_first_difference(self):
        # 前缀被强制相等后，首差位置决定后续删值
        x0, x1, y0, y1 = Var("x0"), Var("x1"), Var("y0"), Var("y1")
        doms = {x0: {2, 3}, y0: {2, 3}, x1: {1}, y1: {0}}
        c = LexConstraint([x0, x1], [y0, y1], strict=True)
        s, ok = propagate_only(doms, [c])
        self.assertTrue(ok)
        self.assertEqual(s.domains[x0], {2})
        self.assertEqual(s.domains[y0], {3})
        self.assertEqual(s.domains[x1], {1})
        self.assertEqual(s.domains[y1], {0})
        alpha, beta = c.prefix_stats(s.domains)
        self.assertEqual((alpha, beta), (0, 0))

    def test_earlier_smaller_frees_later(self):
        # 前位已小，则后位不受任何额外约束
        x0, x1, y0, y1 = Var("x0"), Var("x1"), Var("y0"), Var("y1")
        doms = {x0: {0}, y0: {1, 2}, x1: set(range(6)), y1: set(range(6))}
        s, ok = propagate_only(doms, [LexConstraint([x0, x1], [y0, y1], strict=True)])
        self.assertTrue(ok)
        self.assertEqual(s.domains[x1], set(range(6)))
        self.assertEqual(s.domains[y1], set(range(6)))
        self.assertEqual(s.proofs, [])

    def test_unsat_with_proofs(self):
        x0, x1, y0, y1 = Var("x0"), Var("x1"), Var("y0"), Var("y1")
        doms = {x0: {1, 2}, y0: {1}, x1: {2}, y1: {0, 1}}
        c = LexConstraint([x0, x1], [y0, y1])
        s, ok = propagate_only(doms, [c])
        self.assertFalse(ok)
        self.assertTrue(s.proofs)
        for p in s.proofs:
            self.assertTrue(p.verify(c))
            self.assertIn("lex_leq([x0,x1], [y0,y1])", p.render())


class TestLengthRules(unittest.TestCase):
    def test_empty_vectors(self):
        s, ok = propagate_only({}, [LexConstraint([], [], strict=False)])
        self.assertTrue(ok)                       # [] <= []
        s, ok = propagate_only({}, [LexConstraint([], [], strict=True)])
        self.assertFalse(ok)                      # 不是 [] < []

    def test_empty_vs_nonempty(self):
        a = Var("a")
        s, ok = propagate_only({a: {5}}, [LexConstraint([], [a], strict=True)])
        self.assertTrue(ok)                       # [] 是真前缀 => [] < [a]
        self.assertEqual(s.domains[a], {5})       # a 不受约束
        s, ok = propagate_only({a: {5}}, [LexConstraint([a], [], strict=False)])
        self.assertFalse(ok)                      # [a] <= [] 不成立

    def test_different_lengths(self):
        x0, y0, y1 = Var("x0"), Var("y0"), Var("y1")
        # [5] < [5, ...]：严格与非严格都成立，且不删值
        for strict in (False, True):
            doms = {x0: {5}, y0: {5}, y1: {0, 9}}
            s, ok = propagate_only(doms, [LexConstraint([x0], [y0, y1], strict=strict)])
            self.assertTrue(ok)
            self.assertEqual(s.domains[y1], {0, 9})
        # 反向：[5, x1] <= [5] 不成立（长的不小于等于短的）
        x1 = Var("x1")
        doms = {x0: {5}, x1: {0, 1}, y0: {5}}
        s, ok = propagate_only(doms, [LexConstraint([x0, x1], [y0])])
        self.assertFalse(ok)

    def test_identical_vectors_only_nonstrict(self):
        a, b = Var("a"), Var("b")
        xs = [a, b]
        s, ok = propagate_only({a: {0, 1}, b: {0, 1}},
                               [LexConstraint(xs, xs, strict=True)])
        self.assertFalse(ok)                      # 相同向量不满足严格
        s, ok = propagate_only({a: {0, 1}, b: {0, 1}},
                               [LexConstraint(xs, xs, strict=False)])
        self.assertTrue(ok)                       # 非严格恒成立，不删值
        self.assertEqual(s.domains[a], {0, 1})
        self.assertEqual(s.domains[b], {0, 1})


class TestSharedVariables(unittest.TestCase):
    def test_shared_cross(self):
        # [a,b] < [b,a] 当且仅当 a < b
        a, b = Var("a"), Var("b")
        c = LexConstraint([a, b], [b, a], strict=True)
        s, ok = propagate_only({a: {0, 1}, b: {0, 1}}, [c])
        self.assertTrue(ok)
        self.assertEqual(s.domains[a], {0})
        self.assertEqual(s.domains[b], {1})

    def test_shared_same_position(self):
        a = Var("a")
        s, ok = propagate_only({a: {0, 1}}, [LexConstraint([a], [a], strict=True)])
        self.assertFalse(ok)                      # a < a 不可能


class TestPushPop(unittest.TestCase):
    def test_pop_restores_domains_and_prefix_cache(self):
        x0, x1, y0, y1 = Var("x0"), Var("x1"), Var("y0"), Var("y1")
        c = LexConstraint([x0, x1], [y0, y1], strict=True)
        s = Solver({x0: {1, 2}, y0: {1, 2}, x1: {0, 1}, y1: {0, 1}}, [c])
        s.propagate()
        s.push()
        snap_domains = {v: set(d) for v, d in s.domains.items()}
        snap_cache = dict(c._cache)
        self.assertTrue(snap_cache)               # 前缀支持缓存已被填充
        # 深入一层：赋值并继续传播，缓存与域都会变化
        s.domains[x0] = {2}
        s.propagate()
        self.assertNotEqual({v: set(d) for v, d in s.domains.items()}, snap_domains)
        s.pop()
        self.assertEqual(s.domains, snap_domains)     # 域恢复
        self.assertEqual(c._cache, snap_cache)        # 前缀支持缓存恢复


class TestBudget(unittest.TestCase):
    def test_budget_exhausted_reports_no_fixpoint(self):
        x, y = Var("x"), Var("y")
        c = LexConstraint([x], [y])
        r = Solver({x: {0, 1, 2}, y: {0, 1, 2}}, [c], budget_steps=0).solve()
        self.assertEqual(r.status, "UNKNOWN")
        self.assertFalse(r.fixpoint_reached)
        self.assertEqual(r.message, MSG_NO_FIXPOINT)
        self.assertIn("固定点", r.message)


class TestSolveAndIndependentCheck(unittest.TestCase):
    def test_sat_solution_verified(self):
        x0, x1, y0 = Var("x0"), Var("x1"), Var("y0")
        c = LexConstraint([x0, x1], [y0], strict=True)
        r = Solver({x0: {0, 1}, x1: {0, 1}, y0: {1}}, [c]).solve()
        self.assertEqual(r.status, "SAT")
        self.assertTrue(check_assignment(c.xs, c.ys, c.strict, r.solution))
        bad = dict(r.solution)
        bad[x0] = 1
        bad[x1] = 1
        self.assertFalse(check_assignment(c.xs, c.ys, c.strict, bad))

    def test_unsat(self):
        x, y = Var("x"), Var("y")
        r = Solver({x: {3}, y: {2}}, [LexConstraint([x], [y])]).solve()
        self.assertEqual(r.status, "UNSAT")


class TestExactDomainsAgainstEnumeration(unittest.TestCase):
    """保留域恰好包含所有支持值：与独立全赋值枚举逐一对比。"""

    def check_instance(self, xs, ys, strict, domains):
        c = LexConstraint(xs, ys, strict=strict)
        s = Solver(domains, [c])
        try:
            s.propagate()
            wiped = False
        except Exception:
            wiped = True
        supported, count = enumerate_supported_values([c], domains)
        if count == 0:
            self.assertTrue(wiped, f"枚举无解但传播未失败: {c.expr()}")
            return
        self.assertFalse(wiped, f"枚举有解但传播失败: {c.expr()}")
        for v in c.vars:
            self.assertEqual(s.domains[v], supported[v],
                             f"{c.expr()} 变量 {v.name} 保留域不精确")
        for p in s.proofs:                        # 每条删值证明都可独立复核
            self.assertTrue(p.verify(c))

    def test_handpicked(self):
        a, b, c_, d = Var("a"), Var("b"), Var("c"), Var("d")
        cases = [
            ([a, b], [c_, d], False, {a: {0, 1}, b: {1, 2}, c_: {1}, d: {0, 2}}),
            ([a, b], [c_, d], True, {a: {2}, b: {0, 3}, c_: {2}, d: {0, 3}}),
            ([a], [c_, d], True, {a: {1, 2}, c_: {1, 2}, d: {0}}),
            ([a, b], [b, a], True, {a: {0, 1, 2}, b: {0, 1, 2}}),
            ([a, a], [b, c_], False, {a: {0, 1}, b: {0, 1}, c_: {0, 1}}),
        ]
        for xs, ys, strict, doms in cases:
            with self.subTest(strict=strict):
                self.check_instance(xs, ys, strict, doms)

    def test_randomized(self):
        rng = random.Random(20261001)
        for trial in range(300):
            pool = [Var(f"v{i}") for i in range(rng.randint(1, 4))]
            n, m = rng.randint(0, 3), rng.randint(0, 3)
            xs = [rng.choice(pool) for _ in range(n)]   # 允许共享变量
            ys = [rng.choice(pool) for _ in range(m)]
            strict = rng.random() < 0.5
            domains = {}
            for v in pool:
                domains[v] = set(rng.sample([0, 1, 2], rng.randint(1, 3)))
            with self.subTest(trial=trial, n=n, m=m, strict=strict):
                self.check_instance(xs, ys, strict, domains)


if __name__ == "__main__":
    unittest.main()
