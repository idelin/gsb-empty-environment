import math
import unittest
from fractions import Fraction

from ir import FLOAT, INT, Builder
from exec_engine import Batch, Engine, render_plan, topo_order


def make_batches():
    return [
        Batch({
            "x": (INT, [1, 2, 20, 5]),
            "y": (FLOAT, [0.5, float("nan"), 2.0, None]),
        }),
        Batch({
            "x": (INT, [3, 7, 30]),
            "y": (FLOAT, [1.5, None, 0.5]),
        }),
    ]


class DedupTest(unittest.TestCase):
    def test_sum_x_computed_once_for_three_references(self):
        b = Builder()
        x = b.col("x", INT)
        s = b.agg("sum", x)
        queries = [{"a": s}, {"b": s}, {"c": s}]
        batches = make_batches()
        results, counts, order = Engine().run(queries, batches)
        agg_nodes = [n for n in order if n.kind == "agg"]
        self.assertEqual(len(agg_nodes), 1)
        self.assertEqual(counts.evals[s.uid], len(batches))
        self.assertEqual(counts.row_ops[s.uid], 7)
        for res in results:
            self.assertEqual(list(res.values()), [68])

    def test_shared_scalar_subexpression_computed_once(self):
        b = Builder()
        x = b.col("x", INT)
        inc = b.binop("add", x, b.lit(1))
        s1 = b.agg("sum", inc)
        s2 = b.agg("max", inc)
        self.assertIs(s1.children[0], s2.children[0])
        _, counts, _ = Engine().run([{"s": s1}, {"m": s2}], make_batches())
        self.assertEqual(counts.evals[inc.uid], 2)
        self.assertEqual(counts.row_ops[inc.uid], 7)

    def test_sum_x_and_sum_cast_x_not_merged(self):
        b = Builder()
        x = b.col("x", INT)
        s_int = b.agg("sum", x)
        s_float = b.agg("sum", b.cast(x, FLOAT))
        self.assertIsNot(s_int, s_float)
        results, counts, order = Engine().run(
            [{"si": s_int, "sf": s_float}], make_batches()
        )
        agg_nodes = [n for n in order if n.kind == "agg"]
        self.assertEqual(len(agg_nodes), 2)
        si, sf = results[0]["si"], results[0]["sf"]
        self.assertIsInstance(si, int)
        self.assertIsInstance(sf, float)
        self.assertEqual(si, 68)
        self.assertEqual(sf, 68.0)

    def test_int_exact_where_float_loses_precision(self):
        b = Builder()
        x = b.col("x", INT)
        big = 2**53 + 1
        batches = [Batch({"x": (INT, [big, big])})]
        s_int = b.agg("sum", x)
        s_float = b.agg("sum", b.cast(x, FLOAT))
        results, _, _ = Engine().run([{"si": s_int, "sf": s_float}], batches)
        self.assertEqual(results[0]["si"], 2**54 + 2)
        self.assertEqual(results[0]["sf"], 2.0**54)
        self.assertNotEqual(results[0]["si"], results[0]["sf"])


class NullNanTest(unittest.TestCase):
    def test_null_and_nan_are_distinct(self):
        b = Builder()
        y = b.col("y", FLOAT)
        cnt = b.agg("count", y)
        s = b.agg("sum", y)
        batches = [Batch({"y": (FLOAT, [float("nan"), None, 1.0])})]
        results, _, _ = Engine().run([{"c": cnt, "s": s}], batches)
        self.assertEqual(results[0]["c"], 2)
        self.assertTrue(math.isnan(results[0]["s"]))

    def test_is_null_vs_is_nan(self):
        b = Builder()
        y = b.col("y", FLOAT)
        nulls = b.agg("sum", b.lit(1), filter=b.is_null(y))
        nans = b.agg("sum", b.lit(1), filter=b.is_nan(y))
        batches = [Batch({"y": (FLOAT, [float("nan"), None, 1.0])})]
        results, _, _ = Engine().run([{"n": nulls, "nans": nans}], batches)
        self.assertEqual(results[0]["n"], 1)
        self.assertEqual(results[0]["nans"], 1)

    def test_filter_null_differences_preserved(self):
        b = Builder()
        x = b.col("x", INT)
        y = b.col("y", FLOAT)
        f_strict = b.binop("gt", y, b.lit(1.0))
        f_keep_null = b.binop("or", f_strict, b.is_null(y))
        s_strict = b.agg("sum", x, filter=f_strict)
        s_keep = b.agg("sum", x, filter=f_keep_null)
        self.assertIsNot(s_strict, s_keep)
        results, _, order = Engine().run(
            [{"a": s_strict, "b": s_keep}], make_batches()
        )
        agg_nodes = [n for n in order if n.kind == "agg"]
        self.assertEqual(len(agg_nodes), 2)
        self.assertEqual(results[0]["a"], 23)
        self.assertEqual(results[0]["b"], 35)

    def test_nan_row_excluded_by_null_tolerant_filter(self):
        b = Builder()
        x = b.col("x", INT)
        y = b.col("y", FLOAT)
        f = b.binop("or", b.binop("gt", y, b.lit(1.0)), b.is_null(y))
        s = b.agg("sum", x, filter=f)
        batches = [Batch({
            "x": (INT, [100, 1]),
            "y": (FLOAT, [float("nan"), None]),
        })]
        results, _, _ = Engine().run([{"s": s}], batches)
        self.assertEqual(results[0]["s"], 1)

    def test_aggregates_over_empty_and_all_null(self):
        b = Builder()
        x = b.col("x", INT)
        queries = [{
            "s": b.agg("sum", x),
            "c": b.agg("count", x),
            "cs": b.agg("count"),
            "a": b.agg("avg", x),
            "mn": b.agg("min", x),
            "mx": b.agg("max", x),
        }]
        batches = [Batch({"x": (INT, [None, None])}), Batch({"x": (INT, [])})]
        results, _, _ = Engine().run(queries, batches)
        r = results[0]
        self.assertIsNone(r["s"])
        self.assertEqual(r["c"], 0)
        self.assertEqual(r["cs"], 2)
        self.assertIsNone(r["a"])
        self.assertIsNone(r["mn"])
        self.assertIsNone(r["mx"])


class SideEffectTest(unittest.TestCase):
    def test_side_effect_func_not_deduped(self):
        b = Builder()
        x = b.col("x", INT)
        calls = []

        def trace(v):
            calls.append(v)
            return v

        n1 = b.func("trace", [x], INT, pure=False)
        n2 = b.func("trace", [x], INT, pure=False)
        self.assertIsNot(n1, n2)
        s1 = b.agg("sum", n1)
        s2 = b.agg("sum", n2)
        self.assertIsNot(s1, s2)
        results, counts, _ = Engine(funcs={"trace": trace}).run(
            [{"a": s1, "b": s2}], make_batches()
        )
        self.assertEqual(len(calls), 14)
        self.assertEqual(counts.row_ops[n1.uid], 7)
        self.assertEqual(counts.row_ops[n2.uid], 7)
        self.assertEqual(results[0], {"a": 68, "b": 68})

    def test_pure_func_deduped(self):
        b = Builder()
        x = b.col("x", INT)
        calls = []

        def double(v):
            calls.append(v)
            return None if v is None else 2 * v

        n1 = b.func("double", [x], INT, pure=True)
        n2 = b.func("double", [x], INT, pure=True)
        self.assertIs(n1, n2)
        s1 = b.agg("sum", n1)
        s2 = b.agg("sum", n2)
        self.assertIs(s1, s2)
        Engine(funcs={"double": double}).run([{"a": s1}, {"b": s2}], make_batches())
        self.assertEqual(len(calls), 7)


class SemanticsTest(unittest.TestCase):
    def test_batched_equals_single_batch(self):
        def build():
            b = Builder()
            x = b.col("x", INT)
            y = b.col("y", FLOAT)
            return [{
                "s": b.agg("sum", x),
                "a": b.agg("avg", x),
                "sy": b.agg("sum", y),
                "c": b.agg("count"),
            }]

        r1, _, _ = Engine().run(build(), make_batches())
        merged = [Batch({
            "x": (INT, [1, 2, 20, 5, 3, 7, 30]),
            "y": (FLOAT, [0.5, float("nan"), 2.0, None, 1.5, None, 0.5]),
        })]
        r2, _, _ = Engine().run(build(), merged)
        self.assertEqual(r1[0]["s"], r2[0]["s"])
        self.assertEqual(r1[0]["a"], r2[0]["a"])
        self.assertEqual(r1[0]["c"], r2[0]["c"])
        self.assertTrue(math.isnan(r1[0]["sy"]) and math.isnan(r2[0]["sy"]))

    def test_avg_int_is_exact_fraction(self):
        b = Builder()
        x = b.col("x", INT)
        a = b.agg("avg", x)
        batches = [Batch({"x": (INT, [1, 2])}), Batch({"x": (INT, [4, None])})]
        results, _, _ = Engine().run([{"a": a}], batches)
        self.assertEqual(results[0]["a"], Fraction(7, 3))
        self.assertIsInstance(results[0]["a"], Fraction)

    def test_min_max_skip_null(self):
        b = Builder()
        x = b.col("x", INT)
        queries = [{"mn": b.agg("min", x), "mx": b.agg("max", x)}]
        results, _, _ = Engine().run(queries, make_batches())
        self.assertEqual(results[0]["mn"], 1)
        self.assertEqual(results[0]["mx"], 30)

    def test_three_valued_logic(self):
        b = Builder()
        x = b.col("x", INT)
        f = b.binop("and", b.binop("gt", x, b.lit(2)), b.binop("lt", x, b.lit(10)))
        s = b.agg("sum", x, filter=f)
        batches = [Batch({"x": (INT, [1, 5, None, 20])})]
        results, _, _ = Engine().run([{"s": s}], batches)
        self.assertEqual(results[0]["s"], 5)

    def test_distinct_counts_nan_once(self):
        b = Builder()
        y = b.col("y", FLOAT)
        c = b.agg("count", y, distinct=True)
        batches = [Batch({
            "y": (FLOAT, [float("nan"), float("nan"), 1.0, 1.0, None]),
        })]
        results, _, _ = Engine().run([{"c": c}], batches)
        self.assertEqual(results[0]["c"], 2)

    def test_count_star_with_filter(self):
        b = Builder()
        x = b.col("x", INT)
        c = b.agg("count", filter=b.binop("gt", x, b.lit(2)))
        batches = [Batch({"x": (INT, [1, 5, None, 20])})]
        results, _, _ = Engine().run([{"c": c}], batches)
        self.assertEqual(results[0]["c"], 2)

    def test_cast_nan_to_int_raises(self):
        b = Builder()
        y = b.col("y", FLOAT)
        s = b.agg("sum", b.cast(y, INT))
        batches = [Batch({"y": (FLOAT, [float("nan")])})]
        with self.assertRaises(ValueError):
            Engine().run([{"s": s}], batches)


class ValidationTest(unittest.TestCase):
    def test_batch_rejects_wrong_value_type(self):
        with self.assertRaises(TypeError):
            Batch({"x": (INT, [1, "oops"])})

    def test_batch_rejects_bool_in_int_column(self):
        with self.assertRaises(TypeError):
            Batch({"x": (INT, [True])})

    def test_batch_rejects_ragged_columns(self):
        with self.assertRaises(ValueError):
            Batch({"x": (INT, [1]), "y": (FLOAT, [1.0, 2.0])})

    def test_column_type_mismatch_between_expr_and_batch(self):
        b = Builder()
        x = b.col("x", INT)
        s = b.agg("sum", x)
        with self.assertRaises(TypeError):
            Engine().run([{"s": s}], [Batch({"x": (FLOAT, [1.0])})])

    def test_non_agg_output_rejected(self):
        b = Builder()
        x = b.col("x", INT)
        with self.assertRaises(ValueError):
            Engine().run([{"bad": x}], [Batch({"x": (INT, [1])})])


class PlanRenderTest(unittest.TestCase):
    def test_render_plan_shows_graph_and_counts(self):
        b = Builder()
        x = b.col("x", INT)
        s = b.agg("sum", x)
        sf = b.agg("sum", b.cast(x, FLOAT))
        queries = [{"a": s}, {"b": s}, {"c": sf}]
        _, counts, order = Engine().run(queries, make_batches())
        text = render_plan(order, queries, counts)
        self.assertIn("agg(sum)", text)
        self.assertIn("cast->float", text)
        self.assertIn("->", text)
        self.assertIn("evals=2", text)
        self.assertEqual(text.count("agg(sum)"), 2)
        self.assertIn("Q1.a", text)
        self.assertIn("Q2.b", text)

    def test_topo_order_children_before_parents(self):
        b = Builder()
        x = b.col("x", INT)
        s = b.agg("sum", b.binop("add", x, b.lit(1)))
        order = topo_order([s])
        self.assertIs(order[-1], s)
        self.assertIs(order[0], x)


if __name__ == "__main__":
    unittest.main()
