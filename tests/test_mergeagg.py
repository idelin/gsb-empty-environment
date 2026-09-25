import math
import unittest

from mergeagg import (
    NULL,
    AggSpec,
    Batch,
    Checkpoint,
    ColumnType,
    CursorError,
    CursorVersionMismatchError,
    ListStream,
    MergeAggregator,
    is_nan,
    is_null,
)

COLS = ("k", "v", "s")
TYPES = (ColumnType.STRING, ColumnType.INT, ColumnType.FLOAT)
SPECS = [
    AggSpec("v", "sum"),
    AggSpec("s", "sum"),
    AggSpec("s", "count"),
    AggSpec("s", "min"),
]
NAN = float("nan")


def batch(rows):
    return Batch(COLS, TYPES, rows)


def make_streams(not_ready=None):
    """Three sorted streams; group 'b' spans all three of them."""
    not_ready = not_ready or {}
    data = {
        "s1": [
            batch([("a", 10, 1.5), ("b", 20, NULL)]),
            batch([("b", 30, 2.5), ("d", 40, 3.5)]),
        ],
        "s2": [
            batch([("a", 1, 0.5), ("b", 2, NAN)]),
            batch([("c", 3, 9.0)]),
        ],
        "s3": [
            batch([("b", 100, 4.5), ("b", 200, NULL)]),
            batch([("e", 300, 6.5)]),
        ],
    }
    return [
        ListStream(sid, batches, version=1, not_ready_at=not_ready.get(sid, ()))
        for sid, batches in data.items()
    ]


def run_merger(streams, **kwargs):
    merger = MergeAggregator(streams, "k", SPECS, sleep_fn=lambda _: None, **kwargs)
    merger.run()
    return merger.outputs


def norm(value):
    if is_null(value):
        return ("null",)
    if is_nan(value):
        return ("nan",)
    return ("val", value)


def norm_rows(rows):
    return [{name: norm(value) for name, value in row.items()} for row in rows]


class TestBasicMerge(unittest.TestCase):
    def test_groups_and_aggregates(self):
        outputs = run_merger(make_streams())
        self.assertEqual([row["key"] for row in outputs], ["a", "b", "c", "d", "e"])
        by_key = {row["key"]: row for row in outputs}

        self.assertEqual(by_key["a"]["sum_v"], 11)
        self.assertEqual(by_key["a"]["sum_s"], 2.0)
        self.assertEqual(by_key["a"]["count_s"], 2)
        self.assertEqual(by_key["a"]["min_s"], 0.5)

        # group b spans all three streams
        self.assertEqual(by_key["b"]["sum_v"], 20 + 30 + 2 + 100 + 200)
        self.assertTrue(is_nan(by_key["b"]["sum_s"]))  # NaN present -> NaN
        self.assertEqual(by_key["b"]["count_s"], 3)  # NULLs skipped, NaN counted
        self.assertTrue(is_nan(by_key["b"]["min_s"]))

        self.assertEqual(by_key["c"]["sum_v"], 3)
        self.assertEqual(by_key["e"]["sum_s"], 6.5)

    def test_empty_streams_produce_no_output(self):
        streams = [ListStream("s1", []), ListStream("s2", [])]
        self.assertEqual(run_merger(streams), [])

    def test_null_and_nan_are_distinct(self):
        self.assertIsNot(NULL, NAN)
        self.assertFalse(NULL == NAN)
        streams = [
            ListStream(
                "s1",
                [batch([("g", 1, NULL), ("g", 2, NAN), ("h", 3, NULL)])],
            )
        ]
        outputs = run_merger(streams)
        by_key = {row["key"]: row for row in outputs}
        self.assertTrue(is_nan(by_key["g"]["sum_s"]))
        self.assertEqual(by_key["g"]["count_s"], 1)  # only NaN counts
        self.assertTrue(is_null(by_key["h"]["sum_s"]))  # all NULL -> NULL
        self.assertEqual(by_key["h"]["count_s"], 0)

    def test_integer_sum_is_exact(self):
        big1 = 2**70 + 12345
        big2 = 2**69 + 999
        streams = [
            ListStream("s1", [batch([("g", big1, 1.0)])]),
            ListStream("s2", [batch([("g", big2, 2.0)])]),
        ]
        outputs = run_merger(streams)
        total = outputs[0]["sum_v"]
        self.assertIsInstance(total, int)
        self.assertEqual(total, big1 + big2)

    def test_batch_type_validation(self):
        with self.assertRaises(TypeError):
            batch([("a", "not-an-int", 1.0)])
        with self.assertRaises(TypeError):
            batch([("a", True, 1.0)])  # bool is not an int here
        with self.assertRaises(TypeError):
            batch([("a", 1, "not-a-float")])
        with self.assertRaises(ValueError):
            batch([("a", 1)])  # wrong arity

    def test_null_or_nan_key_rejected(self):
        streams = [ListStream("s1", [batch([(NULL, 1, 1.0)])])]
        with self.assertRaises(ValueError):
            run_merger(streams)
        streams = [ListStream("s2", [Batch(("k", "v", "s"),
                                           (ColumnType.FLOAT, ColumnType.INT, ColumnType.FLOAT),
                                           [(NAN, 1, 1.0)])])]
        with self.assertRaises(ValueError):
            run_merger(streams)


class TestCheckpointResume(unittest.TestCase):
    def test_pause_resume_at_every_row_boundary(self):
        """Pausing anywhere -- including mid-group across 3 streams -- and
        resuming from a JSON-round-tripped checkpoint must match the
        uninterrupted run exactly."""
        reference = norm_rows(run_merger(make_streams()))
        total_rows = 10  # rows across all three streams
        for pause_at in range(1, total_rows + 2):
            with self.subTest(pause_after_rows=pause_at):
                merger = MergeAggregator(
                    make_streams(), "k", SPECS, sleep_fn=lambda _: None
                )
                merger.run(pause_after_rows=pause_at)
                payload = merger.checkpoint().to_json()

                restored = MergeAggregator.restore(
                    Checkpoint.from_json(payload),
                    make_streams(),
                    "k",
                    SPECS,
                    sleep_fn=lambda _: None,
                )
                restored.run()
                self.assertEqual(norm_rows(merger.outputs + restored.outputs), reference)

    def test_committed_groups_are_not_reemitted(self):
        merger = MergeAggregator(make_streams(), "k", SPECS, sleep_fn=lambda _: None)
        merger.run(pause_after_rows=3)  # group 'a' committed, 'b' in flight
        checkpoint = merger.checkpoint()
        self.assertTrue(checkpoint.has_committed)
        committed_upto = checkpoint.committed_upto
        committed_keys = [row["key"] for row in merger.outputs]
        self.assertIn("a", committed_keys)
        self.assertIsNotNone(checkpoint.pending)  # 'b' is mid-flight

        restored = MergeAggregator.restore(
            Checkpoint.from_json(checkpoint.to_json()),
            make_streams(),
            "k",
            SPECS,
            sleep_fn=lambda _: None,
        )
        restored.run()
        for row in restored.outputs:
            self.assertGreater(row["key"], committed_upto)
            self.assertNotIn(row["key"], committed_keys)

    def test_pending_group_state_survives_json(self):
        merger = MergeAggregator(make_streams(), "k", SPECS, sleep_fn=lambda _: None)
        merger.run(pause_after_rows=4)  # inside group 'b' (NaN/NULL in state)
        checkpoint = merger.checkpoint()
        clone = Checkpoint.from_json(checkpoint.to_json())
        self.assertEqual(clone.pending.key, checkpoint.pending.key)
        self.assertEqual(clone.pending.drained, checkpoint.pending.drained)
        # NaN inside the pending sum state must survive as NaN, not null.
        restored = MergeAggregator.restore(
            clone, make_streams(), "k", SPECS, sleep_fn=lambda _: None
        )
        restored.run()
        combined = merger.outputs + restored.outputs
        by_key = {row["key"]: row for row in combined}
        self.assertTrue(is_nan(by_key["b"]["sum_s"]))
        self.assertEqual(by_key["b"]["sum_v"], 352)

    def test_cursor_version_mismatch_raises(self):
        merger = MergeAggregator(make_streams(), "k", SPECS, sleep_fn=lambda _: None)
        merger.run(pause_after_rows=2)
        checkpoint = merger.checkpoint()

        stale = [
            ListStream("s1", [], version=2),  # bumped version
            ListStream("s2", [], version=1),
            ListStream("s3", [], version=1),
        ]
        with self.assertRaises(CursorVersionMismatchError) as ctx:
            MergeAggregator.restore(checkpoint, stale, "k", SPECS)
        self.assertEqual(ctx.exception.stream_id, "s1")
        self.assertEqual(ctx.exception.expected, 1)
        self.assertEqual(ctx.exception.actual, 2)

    def test_stream_set_mismatch_raises(self):
        merger = MergeAggregator(make_streams(), "k", SPECS, sleep_fn=lambda _: None)
        merger.run(pause_after_rows=2)
        checkpoint = merger.checkpoint()
        with self.assertRaises(CursorError):
            MergeAggregator.restore(
                checkpoint, [ListStream("s1", []), ListStream("s2", [])], "k", SPECS
            )
        with self.assertRaises(CursorError):
            MergeAggregator.restore(
                checkpoint,
                make_streams() + [ListStream("s4", [])],
                "k",
                SPECS,
            )


class TestTemporaryEmptyStream(unittest.TestCase):
    def test_not_ready_is_not_treated_as_eof(self):
        # s2 reports NOT_READY twice in the middle of its lifetime; the rows
        # behind that gap must still be merged.
        streams = make_streams(not_ready={"s2": (1, 2)})
        outputs = run_merger(streams)
        self.assertEqual(norm_rows(outputs), norm_rows(run_merger(make_streams())))
        by_key = {row["key"]: row for row in outputs}
        self.assertEqual(by_key["c"]["sum_v"], 3)  # row after the NOT_READY gap

    def test_not_ready_budget_exceeded_raises(self):
        from mergeagg import StreamNotReadyError

        class NeverReady(ListStream):
            def poll(self):
                from mergeagg import NOT_READY

                return NOT_READY

        streams = [
            ListStream("s1", [batch([("a", 1, 1.0)])]),
            NeverReady("s2", []),
        ]
        merger = MergeAggregator(
            streams, "k", SPECS, max_not_ready=3, sleep_fn=lambda _: None
        )
        with self.assertRaises(StreamNotReadyError):
            merger.run()


if __name__ == "__main__":
    unittest.main()
