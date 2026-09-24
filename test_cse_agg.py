import math
import unittest
from fractions import Fraction

from cse_agg import INT, FLOAT, BOOL, Col, Lit, Cast, Func, Agg, Engine

NAN = float("nan")
SCHEMA = [("x", INT), ("y", FLOAT)]


def gt(a, b):
    return a > b


def make_batches():
    return [
        (SCHEMA, [(1, 1.5), (2, None), (None, NAN), (2 ** 70, 2.0)]),
        (SCHEMA, [(3, 3.5), (None, None), (5, NAN)]),
    ]


class CseTests(unittest.TestCase):
    def test_sum_referenced_3_times_computed_once(self):
        x = Col("x", INT)
        s = Agg("sum", x, out_type=INT)
        eng = Engine([[s], [s], [s]])
        results, counts = eng.run(make_batches())
        node = eng.plan.memo[s.key()]
        self.assertEqual(counts[node], 2)  # 2 个批次各一次, 而非 3 处引用 x 2 批
        self.assertEqual(len([n for n in eng.plan.nodes
                              if isinstance(n.expr, Agg)]), 1)
        self.assertEqual(results, [[2 ** 70 + 11]] * 3)

    def test_sum_and_sum_cast_not_merged(self):
        x = Col("x", INT)
        s_int = Agg("sum", x, out_type=INT)
        s_flt = Agg("sum", Cast(x, FLOAT), out_type=FLOAT)
        eng = Engine([[s_int, s_flt]])
        results, counts = eng.run(make_batches())
        aggs = [n for n in eng.plan.nodes if isinstance(n.expr, Agg)]
        self.assertEqual(len(aggs), 2)  # 类型语义不同, 禁止合并
        ri, rf = results[0]
        self.assertIsInstance(ri, int)
        self.assertIsInstance(rf, float)
        self.assertEqual(ri, 2 ** 70 + 11)               # 整数精确
        self.assertEqual(rf, float(2 ** 70 + 11))        # 浮点语义

    def test_volatile_func_never_deduplicated(self):
        calls = []

        def side_effect():
            calls.append(1)
            return len(calls)

        f1 = Func("se", side_effect, [], INT, volatile=True)
        f2 = Func("se", side_effect, [], INT, volatile=True)
        eng = Engine([[f1, f2]])
        results, _ = eng.run([(SCHEMA, [(1, 1.0), (2, 2.0)])])
        vols = [n for n in eng.plan.nodes if isinstance(n.expr, Func)]
        self.assertEqual(len(vols), 2)  # 两个节点, 不复用
        self.assertEqual(results[0][0], [[1, 2]])
        self.assertEqual(results[0][1], [[3, 4]])
        self.assertEqual(len(calls), 4)

    def test_pure_func_deduplicated(self):
        x = Col("x", INT)
        f = lambda: Func("dbl", lambda v: v * 2, [x], INT)
        eng = Engine([[f(), f()]])
        funcs = [n for n in eng.plan.nodes if isinstance(n.expr, Func)]
        self.assertEqual(len(funcs), 1)

    def test_null_and_nan_are_distinct(self):
        y = Col("y", FLOAT)
        eng = Engine([[Agg("count", y, out_type=INT),
                       Agg("sum", y, out_type=FLOAT)]])
        results, _ = eng.run(make_batches())
        # y 值: 1.5, None, NaN, 2.0 | 3.5, None, NaN
        self.assertEqual(results[0][0], 5)          # null 不计, NaN 计
        self.assertTrue(math.isnan(results[0][1]))  # NaN 参与求和 -> NaN

    def test_sum_skips_null_int_column(self):
        x = Col("x", INT)
        eng = Engine([[Agg("count", x, out_type=INT)]])
        results, _ = eng.run(make_batches())
        self.assertEqual(results[0][0], 5)  # x 有 2 个 null

    def test_filter_differences_preserved(self):
        x = Col("x", INT)
        plain = Agg("sum", x, out_type=INT)
        f1 = Agg("sum", x, out_type=INT,
                 filt=Func("gt", gt, [x, Lit(1, INT)], BOOL))
        f2 = Agg("sum", x, out_type=INT,
                 filt=Func("gt", gt, [x, Lit(100, INT)], BOOL))
        eng = Engine([[plain, f1, f2]])
        results, _ = eng.run(make_batches())
        aggs = [n for n in eng.plan.nodes if isinstance(n.expr, Agg)]
        self.assertEqual(len(aggs), 3)  # 过滤条件不同, 不得合并
        total = 2 ** 70 + 11
        self.assertEqual(results[0], [total, total - 1, total - 1 - 2 - 3 - 5])

    def test_filter_null_treated_as_false(self):
        x = Col("x", INT)
        f = Agg("sum", x, out_type=INT,
                filt=Func("gt", gt, [x, Lit(0, INT)], BOOL))
        eng = Engine([[f]])
        results, _ = eng.run(make_batches())
        # x 的 null 行使 filter 为 null -> 排除, 结果不含 null 行
        self.assertEqual(results[0][0], 2 ** 70 + 11)

    def test_integer_exactness_beyond_float53(self):
        x = Col("x", INT)
        eng = Engine([[Agg("sum", x, out_type=INT)]])
        big = [(SCHEMA, [(2 ** 70, 1.0), (2 ** 70, 1.0), (1, 1.0)])]
        results, _ = eng.run(big)
        self.assertEqual(results[0][0], 2 ** 71 + 1)
        self.assertIsInstance(results[0][0], int)

    def test_avg_int_is_exact_fraction(self):
        x = Col("x", INT)
        eng = Engine([[Agg("avg", x, out_type=INT)]])
        batches = [(SCHEMA, [(1, 1.0), (2, 1.0), (None, 1.0)])]
        results, _ = eng.run(batches)
        self.assertEqual(results[0][0], Fraction(3, 2))

    def test_batches_match_single_batch_and_counts_scale(self):
        x = Col("x", INT)
        q = [[Agg("sum", x, out_type=INT)]]
        eng1 = Engine(q)
        r1, c1 = eng1.run(make_batches())
        eng2 = Engine(q)
        flat = [(SCHEMA, [row for _, rows in make_batches() for row in rows])]
        r2, c2 = eng2.run(flat)
        self.assertEqual(r1, r2)
        node = eng1.plan.memo[q[0][0].key()]
        self.assertEqual(c1[node], 2)
        self.assertEqual(c2[node], 1)

    def test_schema_type_mismatch_raises(self):
        x = Col("x", INT)
        eng = Engine([[Agg("sum", x, out_type=INT)]])
        bad = [([("x", FLOAT), ("y", FLOAT)], [(1.0, 1.0)])]
        with self.assertRaises(TypeError):
            eng.run(bad)

    def test_missing_column_raises(self):
        z = Col("z", INT)
        eng = Engine([[Agg("sum", z, out_type=INT)]])
        with self.assertRaises(KeyError):
            eng.run(make_batches())

    def test_count_star_and_empty_sum(self):
        x = Col("x", INT)
        eng = Engine([[Agg("count", None, out_type=INT),
                       Agg("sum", x, out_type=INT)]])
        results, _ = eng.run([(SCHEMA, [(None, 1.0), (None, 2.0)])])
        self.assertEqual(results[0][0], 2)     # count(*) 计所有行
        self.assertIsNone(results[0][1])       # 全 null -> sum 为 null

    def test_float_sum_precision(self):
        y = Col("y", FLOAT)
        eng = Engine([[Agg("sum", y, out_type=FLOAT)]])
        vals = [(1, 1e16), (2, 1.0), (3, -1e16)]
        results, _ = eng.run([(SCHEMA, vals)])
        self.assertEqual(results[0][0], 1.0)  # fsum 精确, 朴素累加会得 0.0


if __name__ == "__main__":
    unittest.main()
