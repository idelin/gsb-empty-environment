"""unittest suite for the window sort-sharing planner and local executor.

Run:  python3.11 -m unittest discover -s tests -v
"""

import unittest

from window_sharing import (
    Frame,
    FrameBound,
    OrderKey,
    SortCounter,
    WindowSpec,
    build_share_plan,
    execute_windows,
    naive_sort_count,
    partition_key_counts,
    physical_sort_count,
)


def partition_counts_for(rows, shapes):
    return {
        shape: len({tuple(row[column] for column in shape) for row in rows})
        for shape in shapes
    }


class PlannerSharingTests(unittest.TestCase):
    def test_identical_order_different_rows_frames_share_one_sort(self):
        frame_a = Frame(FrameBound("UNBOUNDED PRECEDING"), FrameBound("CURRENT ROW"))
        frame_b = Frame(FrameBound("PRECEDING", 1), FrameBound("FOLLOWING", 1))
        windows = [
            WindowSpec("w1", "sum", ("p",), (OrderKey("x"),), frame_a, "x"),
            WindowSpec("w2", "sum", ("p",), (OrderKey("x"),), frame_b, "x"),
        ]
        plan = build_share_plan(windows)
        self.assertEqual(plan.logical_sort_count, 1)
        self.assertEqual(plan.assignment["w1"], plan.assignment["w2"])
        # Frames are preserved per window, never merged.
        group = plan.groups[0]
        self.assertIn("UNBOUNDED PRECEDING", group.frames["w1"])
        self.assertIn("1 ROW PRECEDING", group.frames["w2"])

    def test_prefix_order_reuses_longer_sort(self):
        windows = [
            WindowSpec("short", "rank", ("p",), (OrderKey("a"),)),
            WindowSpec("long", "sum", ("p",),
                       (OrderKey("a"), OrderKey("b")),
                       value_column="b"),
        ]
        plan = build_share_plan(windows)
        self.assertEqual(plan.logical_sort_count, 1)
        self.assertEqual(plan.assignment["short"], plan.assignment["long"])

    def test_asc_vs_desc_cannot_share(self):
        windows = [
            WindowSpec("up", "rank", ("p",), (OrderKey("a", ascending=True),)),
            WindowSpec("down", "rank", ("p",), (OrderKey("a", ascending=False),)),
        ]
        plan = build_share_plan(windows)
        self.assertEqual(plan.logical_sort_count, 2)
        self.assertNotEqual(plan.assignment["up"], plan.assignment["down"])

    def test_null_placement_difference_blocks_sharing(self):
        windows = [
            WindowSpec("nf", "rank", ("p",),
                       (OrderKey("a", ascending=True, nulls_first=True),)),
            WindowSpec("nl", "rank", ("p",),
                       (OrderKey("a", ascending=True, nulls_first=False),)),
        ]
        plan = build_share_plan(windows)
        self.assertEqual(plan.logical_sort_count, 2)

    def test_different_partition_columns_cannot_share(self):
        windows = [
            WindowSpec("w1", "rank", ("p",), (OrderKey("x"),)),
            WindowSpec("w2", "rank", ("q",), (OrderKey("x"),)),
        ]
        plan = build_share_plan(windows)
        self.assertEqual(plan.logical_sort_count, 2)

    def test_partition_by_is_multiset_order_insensitive_duplicates_count(self):
        one = WindowSpec("w1", "rank", ("a", "b"), (OrderKey("x"),))
        two = WindowSpec("w2", "rank", ("b", "a"), (OrderKey("x"),))
        self.assertEqual(one.partition_signature(), two.partition_signature())
        plan = build_share_plan([one, two])
        self.assertEqual(plan.logical_sort_count, 1)

        dup_one = WindowSpec("w3", "rank", ("a", "a"), (OrderKey("x"),))
        self.assertNotEqual(dup_one.partition_signature(), one.partition_signature())

    def test_default_null_position_is_direction_dependent(self):
        asc = OrderKey("a", ascending=True)
        desc = OrderKey("a", ascending=False)
        self.assertFalse(asc.effective_nulls_first())  # ASC NULLS LAST
        self.assertTrue(desc.effective_nulls_first())  # DESC NULLS FIRST
        self.assertNotEqual(asc.signature(), desc.signature())

    def test_minimal_sort_set_removes_only_strictly_covered_prefix(self):
        windows = [
            WindowSpec("a_only", "rank", ("p",), (OrderKey("a"),)),
            WindowSpec("ab", "sum", ("p",), (OrderKey("a"), OrderKey("b")),
                       value_column="b"),
            WindowSpec("ac", "sum", ("p",), (OrderKey("a"), OrderKey("c")),
                       value_column="c"),
        ]
        plan = build_share_plan(windows)
        # ab and ac both cover a-only, but neither covers the other.
        self.assertEqual(plan.logical_sort_count, 2)
        self.assertIn(plan.assignment["a_only"],
                      {plan.assignment["ab"], plan.assignment["ac"]})


class FrameTests(unittest.TestCase):
    def test_rows_frame_offsets_are_clamped_and_physical(self):
        from window_sharing.engine import frame_slice
        frame = Frame(FrameBound("PRECEDING", 1), FrameBound("FOLLOWING", 1))
        self.assertEqual(frame_slice(frame, 0, 5), (0, 2))
        self.assertEqual(frame_slice(frame, 4, 5), (3, 5))
        self.assertEqual(frame_slice(frame, 2, 5), (1, 4))

    def test_invalid_offsets_rejected(self):
        with self.assertRaises(ValueError):
            FrameBound("PRECEDING", 0)
        with self.assertRaises(ValueError):
            FrameBound("CURRENT ROW", 3)


class ExecutionTests(unittest.TestCase):
    ROWS = [
        {"p": "x", "v": 2},
        {"p": "x", "v": 1},
        {"p": "x", "v": 1},  # tie: multiset semantics
        {"p": "y", "v": 5},
    ]

    def test_row_number_stable_on_ties_and_input_aligned_output(self):
        windows = [WindowSpec("rn", "row_number", ("p",), (OrderKey("v"),))]
        results, _ = execute_windows(self.ROWS, windows)
        values = results[0].values
        # Original order x:2, x:1, x:1 -> ordered positions 3, 1, 2 (ties stable).
        self.assertEqual(values, [3, 1, 2, 1])

    def test_rank_gives_peer_rank_dense_rank_too(self):
        windows = [
            WindowSpec("rk", "rank", ("p",), (OrderKey("v"),)),
            WindowSpec("dr", "dense_rank", ("p",), (OrderKey("v"),)),
        ]
        results, plan = execute_windows(self.ROWS, windows)
        by_name = {result.name: result.values for result in results}
        # x:2, x:1, x:1 -> ranks 3,1,1 ; dense ranks 2,1,1
        self.assertEqual(by_name["rk"], [3, 1, 1, 1])
        self.assertEqual(by_name["dr"], [2, 1, 1, 1])
        # Same sort: shared.
        self.assertEqual(plan.assignment["rk"], plan.assignment["dr"])

    def test_null_ordering_first_vs_last(self):
        rows = [{"p": "x", "v": 1}, {"p": "x", "v": None}, {"p": "x", "v": 2}]
        first = [WindowSpec("f", "row_number", ("p",),
                            (OrderKey("v", ascending=True, nulls_first=True),))]
        last = [WindowSpec("l", "row_number", ("p",),
                           (OrderKey("v", ascending=True, nulls_first=False),))]
        first_values = execute_windows(rows, first)[0][0].values
        last_values = execute_windows(rows, last)[0][0].values
        # Input order 1, NULL, 2. NULL first -> positions 2,1,3. NULL last -> 1,3,2.
        self.assertEqual(first_values, [2, 1, 3])
        self.assertEqual(last_values, [1, 3, 2])

    def test_sum_ignores_null_values_but_rows_count_all(self):
        rows = [
            {"p": "x", "v": 10},
            {"p": "x", "v": None},
            {"p": "x", "v": 5},
        ]
        total = WindowSpec(
            "total", "sum", ("p",), (OrderKey("v", nulls_first=False),),
            value_column="v",
        )
        counted = WindowSpec(
            "n", "count", ("p",), (OrderKey("v", nulls_first=False),),
            value_column="v",
        )
        results, _ = execute_windows(rows, [total, counted])
        by_name = {result.name: result.values for result in results}
        # Ordered with NULL last: 5(NULL at pos 2), 10, NULL. running sums: 5,15,15.
        self.assertEqual(by_name["total"], [15, 15, 5])
        # COUNT(v) ignores NULL: 1,2,2; aligned input: row 10 -> 2, NULL -> 2, 5 -> 1.
        self.assertEqual(by_name["n"], [2, 2, 1])

    def test_shared_sort_independent_results_for_distinct_frames(self):
        frame_a = Frame(FrameBound("UNBOUNDED PRECEDING"), FrameBound("CURRENT ROW"))
        frame_b = Frame(FrameBound("PRECEDING", 1), FrameBound("FOLLOWING", 1))
        windows = [
            WindowSpec("run", "sum", ("p",), (OrderKey("v"),), frame_a, "v"),
            WindowSpec("win", "sum", ("p",), (OrderKey("v"),), frame_b, "v"),
        ]
        results, _ = execute_windows(self.ROWS, windows)
        by_name = {result.name: result.values for result in results}
        # x ordered v=1,1,2: running = 1,2,4 (input 2,1,1 -> 4,1,2)
        self.assertEqual(by_name["run"], [4, 1, 2, 5])
        # centered 1 PRECEDING..1 FOLLOWING for 1,1,2 -> 2,4,3
        # input order x:2,x:1,x:1 -> 3,2,4 ; y:5 -> 5
        self.assertEqual(by_name["win"], [3, 2, 4, 5])


class CountingTests(unittest.TestCase):
    def test_physical_sort_calls_match_counter_and_formula(self):
        windows = [
            WindowSpec("w1", "rank", ("p",), (OrderKey("a"),)),
            WindowSpec("w2", "sum", ("p",), (OrderKey("a"), OrderKey("b")),
                       value_column="b"),
            WindowSpec("w3", "rank", ("p",), (OrderKey("a", ascending=False),)),
            WindowSpec("g", "count"),
        ]
        rows = [
            {"p": "x", "a": 1, "b": 1},
            {"p": "y", "a": 1, "b": 1},
            {"p": "z", "a": 1, "b": 1},
        ]
        counter = SortCounter()
        _, plan = execute_windows(rows, windows, counter=counter)
        counts = partition_key_counts(rows, plan)
        # region-partition sorts: prefix merge leaves 2 shapes * 3 partitions,
        # plus 1 global no-partition sort = 7.
        self.assertEqual(physical_sort_count(plan, counts), 7)
        self.assertEqual(counter.count, 7)
        # Naive: 3 windows partitioned by p (3 each) + 1 global = 10.
        self.assertEqual(naive_sort_count(windows, counts), 10)
        self.assertEqual(plan.logical_sort_count, 3)

    def test_empty_table_issues_no_sorts(self):
        windows = [WindowSpec("w1", "rank", ("p",), (OrderKey("a"),))]
        counter = SortCounter()
        results, plan = execute_windows([], windows, counter=counter)
        self.assertEqual(counter.count, 0)
        self.assertEqual(results[0].values, [])
        counts = partition_key_counts([], plan)
        self.assertEqual(physical_sort_count(plan, counts), 0)


if __name__ == "__main__":
    unittest.main()
