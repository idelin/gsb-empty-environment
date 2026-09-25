"""Unit tests for the merge-aggregation engine (stdlib unittest only)."""

import math
import os
import tempfile
import unittest
from fractions import Fraction

from merge_agg import (
    COMPLETE,
    PAUSED,
    Batch,
    CursorVersionMismatch,
    ListStream,
    MergeAggregator,
    MergerError,
    Schema,
    SchemaError,
    UnsortedInputError,
)

SCHEMA = Schema([("k", "int"), ("v", "int"), ("x", "float")])
AGGS = [("sum", "v"), ("count", "v")]


def make_stream(stream_id, rows_by_batch, version=1, closed=True):
    stream = ListStream(stream_id, SCHEMA, version=version)
    for rows in rows_by_batch:
        stream.append(Batch(SCHEMA, rows))
    if closed:
        stream.close()
    return stream


def run_to_completion(streams, aggs=AGGS):
    merger = MergeAggregator(streams, "k", aggs)
    sink = []
    status = merger.run(sink)
    return status, sink, merger


class BatchAndSchemaTests(unittest.TestCase):
    def test_null_allowed_everywhere(self):
        batch = Batch(SCHEMA, [(1, None, None)])
        self.assertEqual(batch.rows[0], (1, None, None))

    def test_nan_is_not_null(self):
        nan = float("nan")
        batch = Batch(SCHEMA, [(1, 5, nan)])
        self.assertIsNone(Batch(SCHEMA, [(1, 5, None)]).rows[0][2])
        self.assertTrue(math.isnan(batch.rows[0][2]))
        self.assertNotEqual(batch.rows[0][2], batch.rows[0][2])  # NaN != NaN

    def test_int_column_rejects_float_nan_and_bool(self):
        with self.assertRaises(SchemaError):
            Batch(SCHEMA, [(1, 1.5, 0.0)])
        with self.assertRaises(SchemaError):
            Batch(SCHEMA, [(1, float("nan"), 0.0)])
        with self.assertRaises(SchemaError):
            Batch(SCHEMA, [(1, True, 0.0)])

    def test_row_width_and_unknown_type_validated(self):
        with self.assertRaises(SchemaError):
            Batch(SCHEMA, [(1, 2)])
        with self.assertRaises(SchemaError):
            Schema([("k", "decimal")])
        with self.assertRaises(SchemaError):
            Schema([("a", "int"), ("a", "int")])


class AggregationSemanticsTests(unittest.TestCase):
    def test_null_skipped_nan_counted_and_propagates(self):
        nan = float("nan")
        stream = make_stream("s", [[(1, 10, None), (1, 20, nan), (2, 5, None)]])
        _, sink, _ = run_to_completion(
            [stream], [("sum", "x"), ("count", "x"), ("min", "x"), ("max", "x")])
        (k1, r1), (k2, r2) = sink
        self.assertEqual(k1, 1)
        self.assertTrue(math.isnan(r1[0]))       # sum propagates NaN
        self.assertEqual(r1[1], 1)               # count: NaN counts, null does not
        self.assertTrue(math.isnan(r1[2]))       # min propagates NaN
        self.assertTrue(math.isnan(r1[3]))       # max propagates NaN
        self.assertEqual(k2, 2)
        self.assertIsNone(r2[0])                 # sum of zero non-nulls -> null
        self.assertEqual(r2[1], 0)

    def test_exact_integer_arithmetic(self):
        big = 2**70
        stream = make_stream("s", [[(1, big, 0.0), (1, big + 1, 0.0), (1, 3, 0.0)]])
        _, sink, _ = run_to_completion([stream], [("sum", "v"), ("avg", "v")])
        total, avg = sink[0][1]
        self.assertEqual(total, 2 * big + 4)
        self.assertIsInstance(total, int)
        self.assertEqual(avg, Fraction(2 * big + 4, 3))
        self.assertIsInstance(avg, Fraction)

    def test_min_max_avg_on_floats(self):
        stream = make_stream("s", [[(1, 0, 1.5), (1, 0, 2.5), (1, 0, None)]])
        _, sink, _ = run_to_completion(
            [stream], [("min", "x"), ("max", "x"), ("avg", "x")])
        self.assertEqual(sink[0][1], [1.5, 2.5, 2.0])


class MergeTests(unittest.TestCase):
    def test_three_stream_merge_groups_in_key_order(self):
        a = make_stream("a", [[(1, 1, 0.0), (3, 3, 0.0)]])
        b = make_stream("b", [[(1, 10, 0.0), (2, 20, 0.0)]])
        c = make_stream("c", [[(2, 200, 0.0), (3, 300, 0.0), (4, 400, 0.0)]])
        status, sink, merger = run_to_completion([a, b, c])
        self.assertEqual(status, COMPLETE)
        self.assertEqual(sink, [
            (1, [11, 2]),
            (2, [220, 2]),
            (3, [303, 2]),
            (4, [400, 1]),
        ])
        self.assertEqual(merger.committed_count, 4)

    def test_empty_inputs_complete_with_no_output(self):
        streams = [make_stream(s, [[]]) for s in "abc"]
        status, sink, _ = run_to_completion(streams)
        self.assertEqual(status, COMPLETE)
        self.assertEqual(sink, [])

    def test_unsorted_input_rejected(self):
        a = make_stream("a", [[(2, 1, 0.0), (1, 2, 0.0)]])
        b = make_stream("b", [[]])
        with self.assertRaises(UnsortedInputError):
            run_to_completion([a, b])

    def test_null_and_nan_keys_rejected(self):
        with self.assertRaises(MergerError):
            run_to_completion([make_stream("a", [[(None, 1, 0.0)]])])
        # NaN key via a float-keyed schema
        fschema = Schema([("k", "float"), ("v", "int")])
        s = ListStream("f", fschema, [Batch(fschema, [(float("nan"), 1)])])
        s.close()
        with self.assertRaises(MergerError):
            MergeAggregator([s], "k", [("sum", "v")]).run([])


class TemporaryEmptyTests(unittest.TestCase):
    def test_temporary_empty_stream_pauses_and_is_not_eof(self):
        a = make_stream("a", [[(1, 1, 0.0)]], closed=False)
        b = ListStream("b", SCHEMA)  # nothing appended, not closed
        merger = MergeAggregator([a, b], "k", AGGS)
        sink = []
        self.assertEqual(merger.run(sink), PAUSED)
        self.assertEqual(sink, [])  # nothing committed while b's head is unknown
        # b later receives data and closes; the same merger resumes.
        b.append(Batch(SCHEMA, [(1, 10, 0.0), (2, 20, 0.0)]))
        b.close()
        a.append(Batch(SCHEMA, [(2, 2, 0.0)]))
        a.close()
        self.assertEqual(merger.run(sink), COMPLETE)
        self.assertEqual(sink, [(1, [11, 2]), (2, [22, 2])])

    def test_closed_empty_stream_is_eof_and_group_completes(self):
        a = make_stream("a", [[(1, 1, 0.0)]])
        b = ListStream("b", SCHEMA)
        b.close()  # permanently empty
        status, sink, _ = None, [], None
        merger = MergeAggregator([a, b], "k", AGGS)
        status = merger.run(sink)
        self.assertEqual(status, COMPLETE)
        self.assertEqual(sink, [(1, [1, 1])])


class CheckpointRestoreTests(unittest.TestCase):
    """Pause/resume with a group spanning three streams."""

    def full_data(self):
        return {
            "a": [[(1, 100, 0.0), (2, 1, 0.0), (3, 5, 0.0)]],
            "b": [[(1, 200, 0.0), (2, 10, 0.0), (2, 20, 0.0)]],
            "c": [[(1, 300, 0.0), (2, 100, 0.0), (4, 7, 0.0)]],
        }

    def baseline(self):
        data = self.full_data()
        streams = [make_stream(s, data[s]) for s in "abc"]
        status, sink, _ = run_to_completion(streams)
        self.assertEqual(status, COMPLETE)
        return sink

    def phase1_streams(self):
        # Only the first part of each stream is available; nothing closed.
        return [
            make_stream("a", [[(1, 100, 0.0), (2, 1, 0.0), (3, 5, 0.0)]],
                        closed=False),
            make_stream("b", [[(1, 200, 0.0), (2, 10, 0.0)]], closed=False),
            make_stream("c", [[(1, 300, 0.0), (2, 100, 0.0), (4, 7, 0.0)]],
                        closed=False),
        ]

    def test_pause_resume_mid_group_matches_uninterrupted_run(self):
        expected = self.baseline()
        self.assertEqual(expected, [
            (1, [600, 3]),
            (2, [131, 4]),   # group 2 spans all three streams
            (3, [5, 1]),
            (4, [7, 1]),
        ])

        # Phase 1: run until a stream dries up mid-group, then checkpoint.
        merger = MergeAggregator(self.phase1_streams(), "k", AGGS)
        sink = []
        self.assertEqual(merger.run(sink), PAUSED)
        self.assertEqual(sink, [(1, [600, 3])])  # group 1 committed
        state = merger.checkpoint()
        self.assertTrue(state["has_group"])      # group 2 is incomplete
        self.assertEqual(state["current_key"], 2)
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "checkpoint.pkl")
            merger.save_checkpoint(path)

            # Phase 2: rebuild equivalent streams with the full data and
            # restore; the incomplete group must be completed exactly once.
            data = self.full_data()
            streams = [make_stream(s, data[s]) for s in "abc"]
            restored = MergeAggregator.restore(path, streams)
            self.assertEqual(restored.committed_count, 1)
            self.assertEqual(restored.last_committed_key, 1)
            self.assertEqual(restored.run(sink), COMPLETE)

        # No committed group is emitted twice; combined output == baseline.
        self.assertEqual(sink, expected)
        self.assertEqual(restored.committed_count, 4)

    def test_restore_does_not_reoutput_committed_groups(self):
        merger = MergeAggregator(self.phase1_streams(), "k", AGGS)
        sink1, sink2 = [], []
        self.assertEqual(merger.run(sink1), PAUSED)
        self.assertEqual(len(sink1), 1)
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "checkpoint.pkl")
            merger.save_checkpoint(path)
            data = self.full_data()
            restored = MergeAggregator.restore(
                path, [make_stream(s, data[s]) for s in "abc"])
            self.assertEqual(restored.run(sink2), COMPLETE)
        keys1 = [k for k, _ in sink1]
        keys2 = [k for k, _ in sink2]
        self.assertEqual(keys1, [1])
        self.assertEqual(keys2, [2, 3, 4])  # group 1 not re-emitted
        self.assertFalse(set(keys1) & set(keys2))

    def test_cursor_version_mismatch_raises(self):
        merger = MergeAggregator(self.phase1_streams(), "k", AGGS)
        self.assertEqual(merger.run([]), PAUSED)
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "checkpoint.pkl")
            merger.save_checkpoint(path)
            data = self.full_data()
            # Same ids and rows, but the data generation (version) changed.
            stale = [make_stream(s, data[s], version=2) for s in "abc"]
            with self.assertRaises(CursorVersionMismatch):
                MergeAggregator.restore(path, stale)
            # Matching version restores fine.
            ok = [make_stream(s, data[s], version=1) for s in "abc"]
            restored = MergeAggregator.restore(path, ok)
            sink = []
            self.assertEqual(restored.run(sink), COMPLETE)
            self.assertEqual(sink, [(2, [131, 4]), (3, [5, 1]), (4, [7, 1])])

    def test_checkpoint_roundtrip_preserves_partial_aggregates(self):
        merger = MergeAggregator(self.phase1_streams(), "k", AGGS)
        merger.run([])
        state = merger.checkpoint()
        acc_sum, acc_count = state["current_accs"]
        # Paused after folding a's (2,1) and b's (2,10) into group 2,
        # before b's (2,20) and c's (2,100) arrived/were consumed.
        self.assertEqual(acc_sum.result(), 11)
        self.assertEqual(acc_count.result(), 2)
        cursors = {c.stream_id: c for c in state["cursors"]}
        self.assertEqual(cursors["a"].rows_consumed, 2)
        self.assertEqual(cursors["b"].rows_consumed, 2)
        self.assertEqual(cursors["c"].rows_consumed, 1)

    def test_restore_with_wrong_stream_id_raises(self):
        merger = MergeAggregator(self.phase1_streams(), "k", AGGS)
        merger.run([])
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "checkpoint.pkl")
            merger.save_checkpoint(path)
            data = self.full_data()
            bad = [make_stream("x", data["a"]),
                   make_stream("b", data["b"]),
                   make_stream("c", data["c"])]
            with self.assertRaises(MergerError):
                MergeAggregator.restore(path, bad)


if __name__ == "__main__":
    unittest.main()
