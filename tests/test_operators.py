import unittest

from pullpipe import (
    AggDef,
    Batch,
    EmptyInjector,
    Filter,
    HashAggregate,
    HashJoin,
    Limit,
    ListTable,
    Nothing,
    Project,
    Sort,
    TableScan,
    UnionAll,
    col,
    lit,
)
from pullpipe.null import Null


def scan(rows, schema=("a", "b"), batch_size=2):
    return TableScan(ListTable(schema, rows), batch_size=batch_size)


class TestFilterProject(unittest.TestCase):
    def test_filter_three_valued(self):
        rows = [(1, 10), (2, Null), (3, 30)]
        root = Filter(scan(rows), col("b") > lit(15))
        # 10 不通过；Null 比较为 Unknown 不通过；30 通过
        self.assertEqual(Pipeline_drain(root), [(3, 30)])

    def test_filter_all_rejected_reaches_eof(self):
        rows = [(i, i) for i in range(5)]
        root = Filter(scan(rows, batch_size=2), col("a") > lit(100))
        self.assertEqual(Pipeline_drain(root), [])

    def test_predicate_error_wrapped(self):
        from pullpipe import BinOp, OperatorError
        root = Filter(
            scan([("oops",)], ("b",)),
            BinOp("+", col("b"), lit(1)),
        )
        with self.assertRaises(OperatorError):
            Pipeline_drain(root)

    def test_project(self):
        from pullpipe import BinOp
        root = Project(
            scan([(1, 2), (3, Null)]),
            [col("a"), BinOp("+", col("b"), lit(10))],
            ["a", "b10"],
        )
        got = Pipeline_drain(root)
        self.assertEqual(got, [(1, 12), (3, Null)])
        self.assertEqual(root.columns.names, ("a", "b10"))


def Pipeline_drain(root):
    from pullpipe import Pipeline
    return Pipeline(root).drain()


class TestHashJoin(unittest.TestCase):
    def test_inner_join_multiset(self):
        left = scan([(1, "l1"), (2, "l2"), (2, "l3"), (3, "l4")], ("k", "tag"), 2)
        right = scan([(2, "r1"), (2, "r2"), (3, "r3")], ("k", "info"), 2)
        root = HashJoin(left, right, col("k"), col("k"))
        got = Pipeline_drain(root)
        # 连接输出保留两侧全部列（键列各保留一份）：多重集合展开
        self.assertEqual(
            sorted(got),
            sorted([
                (2, "l2", 2, "r1"), (2, "l2", 2, "r2"),
                (2, "l3", 2, "r1"), (2, "l3", 2, "r2"),
                (3, "l4", 3, "r3"),
            ]),
        )

    def test_null_keys_never_join(self):
        left = scan([(Null, "l"), (1, "l1")], ("k", "t"))
        right = scan([(Null, "r"), (1, "r1")], ("k", "i"))
        root = HashJoin(left, right, col("k"), col("k"))
        self.assertEqual(Pipeline_drain(root), [(1, "l1", 1, "r1")])

    def test_join_empty_side(self):
        left = scan([(1, "l")], ("k", "t"))
        right = Nothing(("k", "i"))
        root = HashJoin(left, right, col("k"), col("k"))
        self.assertEqual(Pipeline_drain(root), [])
        self.assertTrue(right.closed)

    def test_join_releases_both_sides_under_limit(self):
        left = TableScan(ListTable(("k",), [(i,) for i in range(100)]), 8)
        right = TableScan(ListTable(("k",), [(i,) for i in range(100)]), 8)
        root = Limit(HashJoin(left, right, col("k"), col("k"), batch_size=4), 3)
        got = Pipeline_drain(root)
        self.assertEqual(len(got), 3)
        self.assertTrue(left.closed and right.closed)


class TestAggregate(unittest.TestCase):
    ROWS = [
        ("a", 1), ("a", 3), ("a", Null),
        ("b", 10), ("b", 20),
        (Null, 7), (Null, 8),
        ("a", 1),
    ]

    def _agg(self):
        return HashAggregate(
            scan(self.ROWS, ("g", "v"), 3),
            [
                AggDef("COUNT_ALL", name="n"),
                AggDef("COUNT", col("v"), name="nv"),
                AggDef("SUM", col("v"), name="s"),
                AggDef("AVG", col("v"), name="av"),
                AggDef("MIN", col("v"), name="mn"),
                AggDef("MAX", col("v"), name="mx"),
            ],
            group_keys=[col("g")],
            group_names=["g"],
        )

    def test_group_semantics(self):
        got = {r[0]: r for r in Pipeline_drain(self._agg())}
        self.assertEqual(got["a"], ("a", 4, 3, 5, 5 / 3, 1, 3))
        self.assertEqual(got["b"], ("b", 2, 2, 30, 15, 10, 20))
        self.assertEqual(got[Null], (Null, 2, 2, 15, 7.5, 7, 8))

    def test_global_aggregate_empty_input_emits_one_row(self):
        root = HashAggregate(
            Nothing(("g", "v")),
            [AggDef("COUNT_ALL", name="n"), AggDef("SUM", col("v"), name="s")],
        )
        self.assertEqual(Pipeline_drain(root), [(0, Null)])

    def test_group_order_preserves_first_appearance(self):
        got = Pipeline_drain(self._agg())
        self.assertEqual([r[0] for r in got], ["a", "b", Null])

    def test_string_min_max(self):
        root = HashAggregate(
            scan([("x", "banana"), ("x", "apple")], ("g", "v"), 5),
            [AggDef("MIN", col("v"), name="mn"), AggDef("MAX", col("v"), name="mx")],
            group_keys=[col("g")],
            group_names=["g"],
        )
        self.assertEqual(Pipeline_drain(root), [("x", "apple", "banana")])


class TestSortUnion(unittest.TestCase):
    def test_sort_nulls_order(self):
        rows = [(3,), (Null,), (1,), (2,)]
        asc = Pipeline_drain(Sort(scan(rows, ("v",), 2), [col("v")]))
        self.assertEqual([r[0] for r in asc], [Null, 1, 2, 3])
        desc = Pipeline_drain(Sort(scan(rows, ("v",), 2), [col("v")], descs=[True]))
        self.assertEqual([r[0] for r in desc], [3, 2, 1, Null])

    def test_sort_stable_multiset(self):
        rows = [(1, "a"), (1, "b"), (1, "a"), (0, "z")]
        out = Pipeline_drain(Sort(scan(rows, ("k", "t"), 2), [col("k")]))
        self.assertEqual(out, [(0, "z"), (1, "a"), (1, "b"), (1, "a")])

    def test_union_all_keeps_duplicates(self):
        a = scan([(1,), (2,)], ("x",))
        b = scan([(2,), (3,)], ("x",))
        out = Pipeline_drain(UnionAll(a, b))
        self.assertEqual(out, [(1,), (2,), (2,), (3,)])

    def test_union_schema_check(self):
        with self.assertRaises(ValueError):
            UnionAll(scan([(1,)], ("x",)), scan([(1,)], ("y",)))


class TestEmptyBatchSemantics(unittest.TestCase):
    def test_injected_empty_batch_is_not_eof(self):
        root = EmptyInjector(scan([(i,) for i in range(4)], ("x",), 2))
        batches = []
        from pullpipe import Pipeline
        for b in Pipeline(root).batches():
            batches.append(len(b))
        self.assertEqual(batches[0], 0)
        self.assertEqual(sum(batches), 4)
        self.assertTrue(bool(Batch()))

    def test_nothing_is_immediate_eof(self):
        from pullpipe import Pipeline
        root = Nothing(("x",))
        self.assertEqual(Pipeline(root).drain_batches(), [])


if __name__ == "__main__":
    unittest.main()
