import math
import unittest

from interval_join import (
    IndexedInterval,
    IntervalIndex,
    TypedBatch,
    interval_join,
    point_join,
)


def interval_tuples(matches):
    return sorted(
        (match.left_id, match.right_id, match.overlap_length) for match in matches
    )


def point_tuples(matches):
    return sorted(
        (match.point_id, match.interval_id, match.overlap_length) for match in matches
    )


class IntervalJoinTest(unittest.TestCase):
    def setUp(self):
        self.schema = {"id": "STRING", "start": "INT", "end": "INT"}

    def test_half_open_point_boundary(self):
        points = TypedBatch({"id": "STRING", "point": "INT"}, [{"id": "p", "point": 5}])
        intervals = TypedBatch(
            self.schema,
            [
                {"id": "a", "start": 0, "end": 5},
                {"id": "b", "start": 5, "end": 8},
            ],
        )

        self.assertEqual(
            point_tuples(point_join(points, intervals)),
            [("p", "b", 0)],
        )

    def test_interval_overlap_length_and_touch(self):
        left = TypedBatch(self.schema, [{"id": "A", "start": 0, "end": 7}])
        right = TypedBatch(
            self.schema,
            [
                {"id": "X", "start": 3, "end": 5},
                {"id": "T", "start": 7, "end": 9},
            ],
        )

        self.assertEqual(
            interval_tuples(interval_join(left, right)),
            [("A", "X", 2)],
        )

    def test_zero_length_and_empty_intervals_have_no_match(self):
        left = TypedBatch(
            self.schema,
            [
                {"id": "A", "start": 0, "end": 5},
                {"id": "empty", "start": 4, "end": 4},
            ],
        )
        right = TypedBatch(
            self.schema,
            [
                {"id": "zero", "start": 1, "end": 1},
                {"id": "empty-table", "start": 9, "end": 2},
            ],
        )

        self.assertEqual(interval_join(left, right), [])
        self.assertEqual(
            point_join(
                TypedBatch({"id": "STRING", "point": "INT"}, [{"id": "p", "point": 4}]),
                TypedBatch(self.schema, [{"id": "zero", "start": 4, "end": 4}]),
            ),
            [],
        )

    def test_duplicate_endpoints_and_nested_intervals(self):
        left = TypedBatch(
            self.schema,
            [
                {"id": "dup1", "start": 0, "end": 7},
                {"id": "dup2", "start": 0, "end": 7},
                {"id": "outer", "start": 0, "end": 10},
                {"id": "inner", "start": 2, "end": 4},
            ],
        )
        right = TypedBatch(self.schema, [{"id": "q", "start": 3, "end": 5}])

        self.assertEqual(
            interval_tuples(interval_join(left, right)),
            [
                ("dup1", "q", 2),
                ("dup2", "q", 2),
                ("inner", "q", 1),
                ("outer", "q", 2),
            ],
        )

    def test_batches_and_exact_integer_arithmetic(self):
        batches = [
            TypedBatch(self.schema, [{"id": "A", "start": 10**40, "end": 10**40 + 3}]),
            TypedBatch(self.schema, [{"id": "B", "start": 0, "end": 2}]),
        ]
        right = TypedBatch(
            self.schema,
            [
                {"id": "X", "start": 10**40 + 1, "end": 10**40 + 4},
                {"id": "Y", "start": 1, "end": 3},
            ],
        )

        matches = interval_join(batches, right)
        self.assertEqual(
            interval_tuples(matches),
            [("A", "X", 2), ("B", "Y", 1)],
        )
        self.assertIsInstance(matches[0].overlap_length, int)

    def test_null_is_distinct_from_nan_for_ids_and_invalid_positions(self):
        point_schema = {"id": "FLOAT", "point": "FLOAT"}
        points = TypedBatch(
            point_schema,
            [
                {"id": None, "point": 4.0},
                {"id": math.nan, "point": 4.0},
                {"id": 3.0, "point": None},
                {"id": 4.0, "point": math.nan},
            ],
        )
        intervals = TypedBatch(
            {"id": "FLOAT", "start": "FLOAT", "end": "FLOAT"},
            [
                {"id": None, "start": 0.0, "end": 5.0},
                {"id": math.nan, "start": 0.0, "end": 5.0},
                {"id": 9.0, "start": None, "end": 5.0},
                {"id": 10.0, "start": math.nan, "end": 5.0},
            ],
        )

        matches = point_join(points, intervals)
        self.assertEqual(len(matches), 4)
        self.assertTrue(any(match.point_id is None for match in matches))
        self.assertTrue(
            any(
                isinstance(match.point_id, float)
                and math.isnan(match.point_id)
                for match in matches
            )
        )
        self.assertTrue(any(match.interval_id is None for match in matches))
        self.assertTrue(
            any(
                isinstance(match.interval_id, float)
                and math.isnan(match.interval_id)
                for match in matches
            )
        )

    def test_typed_batch_rules(self):
        with self.assertRaises(TypeError):
            TypedBatch({"x": "INT"}, [{"x": 1.0}])
        with self.assertRaises(TypeError):
            TypedBatch({"x": "INT"}, [{"x": True}])
        TypedBatch({"x": "FLOAT"}, [{"x": math.nan}, {"x": None}])
        with self.assertRaises(TypeError):
            TypedBatch({"x": "FLOAT"}, [{"x": 1}])

    def test_incompatible_batch_schema_and_missing_column(self):
        with self.assertRaises(TypeError):
            interval_join(
                [
                    TypedBatch(
                        {"id": "STRING", "start": "INT", "end": "INT"}, []
                    ),
                    TypedBatch(
                        {"id": "STRING", "start": "FLOAT", "end": "FLOAT"}, []
                    ),
                ],
                TypedBatch(
                    {"id": "STRING", "start": "INT", "end": "INT"}, []
                ),
            )

    def test_direct_index_queries(self):
        index = IntervalIndex(
            [
                IndexedInterval(0, 5, "a"),
                IndexedInterval(5, 8, "b"),
                IndexedInterval(3, 7, "c"),
            ]
        )

        self.assertEqual(
            sorted(item.source_id for item in index.query_point(5)),
            ["b", "c"],
        )
        self.assertEqual(
            sorted(item.source_id for item in index.query_range(7, 10)),
            ["b"],
        )
        self.assertEqual(list(index.query_range(10, 12)), [])
        with self.assertRaises(ValueError):
            point_join(
                TypedBatch({"id": "STRING"}, []),
                TypedBatch(
                    {"id": "STRING", "start": "INT", "end": "INT"}, []
                ),
            )


if __name__ == "__main__":
    unittest.main()
