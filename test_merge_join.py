import os
import threading
import unittest

from merge_join import JoinCancelledError, UnsortedStreamError, merge_join


class MergeJoinTest(unittest.TestCase):
    def test_two_by_three_duplicate_group_outputs_six_rows(self):
        left = [(1, "L1"), (1, "L2"), (3, "L3")]
        right = [(1, "R1"), (1, "R2"), (1, "R3"), (3, "R4")]

        result = list(merge_join(left, right, 0))

        self.assertEqual(len(result), 7)
        self.assertEqual(
            result[:6],
            [
                ((1, "L1"), (1, "R1")),
                ((1, "L1"), (1, "R2")),
                ((1, "L1"), (1, "R3")),
                ((1, "L2"), (1, "R1")),
                ((1, "L2"), (1, "R2")),
                ((1, "L2"), (1, "R3")),
            ],
        )
        self.assertEqual(result[6], ((3, "L3"), (3, "R4")))

    def test_left_outer_null_and_unmatched_semantics(self):
        left = [(None, "null"), (1, "one"), (2, "two")]
        right = [(None, "ignored-null"), (1, "matched")]

        result = list(merge_join(left, right, 0, how="left"))

        self.assertEqual(
            result,
            [
                ((None, "null"), None),
                ((1, "one"), (1, "matched")),
                ((2, "two"), None),
            ],
        )

    def test_multiple_null_rows_are_all_preserved_on_left(self):
        left = [(None, "null-1"), (None, "null-2"), (1, "one")]
        right = [(1, "matched")]

        result = list(merge_join(left, right, 0, how="left"))

        self.assertEqual(
            result,
            [
                ((None, "null-1"), None),
                ((None, "null-2"), None),
                ((1, "one"), (1, "matched")),
            ],
        )

    def test_duplicates_have_multiset_semantics(self):
        left = [("k", index) for index in range(7)]
        right = [("k", index) for index in range(11)]

        result = list(merge_join(left, right, lambda row: row[0]))

        self.assertEqual(len(result), 77)
        self.assertEqual(
            sorted((left_row[1], right_row[1]) for left_row, right_row in result),
            sorted(
                (left_row[1], right_row[1])
                for left_row in left
                for right_row in right
            ),
        )

    def test_group_larger_than_memory_bound_spills_and_completes(self):
        left = [(42, f"L{index}") for index in range(300)]
        right = [(42, f"R{index}") for index in range(4)]
        observed = []
        spill_paths = []

        def observe(stats):
            observed.append(stats)
            if stats["left_spill_path"] is not None:
                spill_paths.append(stats["left_spill_path"])
                self.assertTrue(os.path.exists(stats["left_spill_path"]))

        result = list(
            merge_join(
                left,
                right,
                0,
                max_in_memory=100,
                group_callback=observe,
            )
        )

        self.assertEqual(len(result), 1200)
        self.assertEqual(observed[0]["left_count"], 300)
        self.assertEqual(observed[0]["right_count"], 4)
        self.assertTrue(observed[0]["left_spilled"])
        self.assertFalse(observed[0]["right_spilled"])
        self.assertTrue(spill_paths)
        self.assertFalse(any(os.path.exists(path) for path in spill_paths))

    def test_spill_files_are_removed_after_iteration(self):
        left = [(9, f"L{index}") for index in range(20)]
        right = [(9, f"R{index}") for index in range(20)]
        paths = []

        result = list(
            merge_join(
                left,
                right,
                0,
                max_in_memory=5,
                group_callback=lambda stats: paths.extend(
                    path
                    for path in (stats["left_spill_path"], stats["right_spill_path"])
                    if path is not None
                ),
            )
        )

        self.assertEqual(len(result), 400)
        self.assertTrue(paths)
        self.assertFalse(any(os.path.exists(path) for path in paths))

    def test_unsorted_left_raises_instead_of_emitting_wrong_result(self):
        left = iter([(2, "first"), (1, "out of order")])
        right = [(2, "right")]
        emitted = []

        with self.assertRaises(UnsortedStreamError):
            for pair in merge_join(left, right, 0):
                emitted.append(pair)

        self.assertEqual(emitted, [])

    def test_unsorted_unmatched_left_group_raises_before_output(self):
        left = iter([(2, "first"), (1, "out of order")])
        right = [(3, "ahead")]
        emitted = []

        with self.assertRaises(UnsortedStreamError):
            for pair in merge_join(left, right, 0, how="left"):
                emitted.append(pair)

        self.assertEqual(emitted, [])

    def test_unsorted_null_group_raises_before_left_outer_output(self):
        left = iter([(1, "non-null"), (None, "null after non-null")])
        emitted = []

        with self.assertRaises(UnsortedStreamError):
            for pair in merge_join(left, [], 0, how="left"):
                emitted.append(pair)

        self.assertEqual(emitted, [])

    def test_unsorted_right_raises_within_duplicate_group(self):
        left = [(2, "left")]
        right = iter([(2, "first"), (1, "out of order"), (2, "second")])

        with self.assertRaises(UnsortedStreamError):
            list(merge_join(left, right, 0))

    def test_cancellation_is_checked_between_output_rows(self):
        left = [(1, "left")]
        right = [(1, f"right-{index}") for index in range(5)]
        event = threading.Event()
        seen = 0

        with self.assertRaises(JoinCancelledError):
            for left_row, right_row in merge_join(left, right, 0, cancel_event=event):
                seen += 1
                if right_row[1] == "right-2":
                    event.set()

        self.assertEqual(seen, 3)

    def test_composite_null_never_matches_but_is_preserved_on_left(self):
        left = [(1, None, "left")]
        right = [(1, None, "right")]

        result = list(
            merge_join(left, right, lambda row: (row[0], row[1]), how="left")
        )

        self.assertEqual(result, [((1, None, "left"), None)])


if __name__ == "__main__":
    unittest.main()
