"""Tests for the window sort-sharing planner and executor."""

import unittest

from window_sort_sharing import (
    CURRENT_ROW,
    RELATION,
    UNBOUNDED_FOLLOWING,
    UNBOUNDED_PRECEDING,
    Frame,
    OrderKey,
    WindowFunction,
    WindowSpec,
    execute,
    following,
    plan_windows,
    preceding,
)


def spec(name, kind="sum", column="salary", partition_by=("dept",),
         order_by=(OrderKey("salary"),),
         frame=Frame(UNBOUNDED_PRECEDING, CURRENT_ROW)):
    return WindowSpec(
        name=name,
        function=WindowFunction(kind, column),
        partition_by=partition_by,
        order_by=order_by,
        frame=frame,
    )


class PlanGroupingTests(unittest.TestCase):
    def test_same_sort_different_rows_frames_share_one_sort(self):
        w1 = spec("w1", frame=Frame(UNBOUNDED_PRECEDING, CURRENT_ROW))
        w2 = spec("w2", frame=Frame(preceding(1), following(1)))
        plan = plan_windows([w1, w2])
        self.assertEqual(plan.sort_count, 1)
        self.assertEqual(plan.naive_sort_count, 2)
        self.assertEqual([s.name for s in plan.groups[0].windows], ["w1", "w2"])

    def test_desc_cannot_be_reused(self):
        asc = spec("asc")
        desc = spec("desc", order_by=(OrderKey("salary", descending=True),))
        plan = plan_windows([asc, desc])
        self.assertEqual(plan.sort_count, 2)

    def test_null_ordering_difference_prevents_sharing(self):
        default_asc = spec("default")  # ASC -> NULLS LAST
        nulls_first = spec("nf", order_by=(OrderKey("salary", nulls_first=True),))
        plan = plan_windows([default_asc, nulls_first])
        self.assertEqual(plan.sort_count, 2)

    def test_explicit_nulls_last_matches_asc_default(self):
        default_asc = spec("default")
        explicit = spec("explicit",
                        order_by=(OrderKey("salary", nulls_first=False),))
        plan = plan_windows([default_asc, explicit])
        self.assertEqual(plan.sort_count, 1)

    def test_desc_default_nulls_first_differs_from_asc_nulls_first(self):
        desc = spec("desc", order_by=(OrderKey("salary", descending=True),))
        asc_nf = spec("asc_nf", order_by=(OrderKey("salary", nulls_first=True),))
        plan = plan_windows([desc, asc_nf])
        self.assertEqual(plan.sort_count, 2)

    def test_partition_mismatch_prevents_sharing(self):
        by_dept = spec("a", partition_by=("dept",))
        whole = spec("b", partition_by=())
        plan = plan_windows([by_dept, whole])
        self.assertEqual(plan.sort_count, 2)


class ExecutionTests(unittest.TestCase):
    def setUp(self):
        self.w1 = spec("w1", frame=Frame(UNBOUNDED_PRECEDING, CURRENT_ROW))
        self.w2 = spec("w2", frame=Frame(preceding(1), following(1)))
        self.w3 = spec("w3", order_by=(OrderKey("salary", descending=True),))
        self.w4 = spec("w4", kind="row_number", column=None,
                       order_by=(OrderKey("salary", nulls_first=True),),
                       frame=Frame(UNBOUNDED_PRECEDING, UNBOUNDED_FOLLOWING))
        self.w5 = spec("w5", kind="rank", column=None,
                       frame=Frame(UNBOUNDED_PRECEDING, UNBOUNDED_FOLLOWING))
        self.plan = plan_windows([self.w1, self.w2, self.w3, self.w4, self.w5])
        self.result = execute(self.plan, RELATION)

    def test_actual_sort_count_matches_shared_groups(self):
        self.assertEqual(self.plan.sort_count, 3)
        self.assertEqual(self.result.sorts_performed, 3)
        self.assertEqual(self.plan.naive_sort_count, 5)

    def test_shared_sort_but_different_frame_results(self):
        # w1 and w2 share one sort, yet produce different values per row.
        self.assertEqual(self.result.results["w1"],
                         [100, 200, 320, 80, 160, 90, 185, 320])
        self.assertEqual(self.result.results["w2"],
                         [200, 320, 120, 160, 160, 185, 185, 220])

    def test_null_partition_rows_group_together(self):
        # Rows 3 and 4 both have dept=None: one partition, running sum 80,160.
        self.assertEqual(self.result.results["w1"][3], 80)
        self.assertEqual(self.result.results["w1"][4], 160)

    def test_multiset_duplicates_counted_individually(self):
        # Rows 3 and 4 are identical duplicates; both appear in the frame.
        self.assertEqual(self.result.results["w2"][3], 160)  # 80 + 80
        self.assertEqual(self.result.results["w1"][4], 160)  # running sum: 80 + 80

    def test_sum_ignores_nulls(self):
        # Row 2 has salary=None; running sum stays at 320 within eng.
        self.assertEqual(self.result.results["w1"][2], 320)
        # DESC default puts NULLS first: row 2's frame is all-NULL -> None.
        self.assertIsNone(self.result.results["w3"][2])

    def test_rank_ties_and_null_peer_group(self):
        # eng order: 100,100,120,NULL -> ranks 1,1,3,4 (NULL is its own peer).
        self.assertEqual(self.result.results["w5"][0], 1)
        self.assertEqual(self.result.results["w5"][1], 1)
        self.assertEqual(self.result.results["w5"][7], 3)
        self.assertEqual(self.result.results["w5"][2], 4)

    def test_nulls_first_changes_row_number(self):
        # ASC NULLS FIRST: NULL salary row leads the eng partition.
        self.assertEqual(self.result.results["w4"][2], 1)
        self.assertEqual(self.result.results["w4"][7], 4)

    def test_empty_frame_sum_is_null_and_count_is_zero(self):
        w = spec("empty", frame=Frame(preceding(2), preceding(1)))
        result = execute(plan_windows([w]), [{"dept": "x", "salary": 5}])
        self.assertIsNone(result.results["empty"][0])
        w_count = spec("empty_count", kind="count_star", column=None,
                       frame=Frame(preceding(2), preceding(1)))
        result = execute(plan_windows([w_count]), [{"dept": "x", "salary": 5}])
        self.assertEqual(result.results["empty_count"][0], 0)

    def test_results_aligned_with_input_row_order(self):
        for name, values in self.result.results.items():
            self.assertEqual(len(values), len(RELATION), name)


if __name__ == "__main__":
    unittest.main()
