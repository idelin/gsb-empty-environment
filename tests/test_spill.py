"""Spilling, paged input, skewed keys, disk failures and cancellation."""

import os
import tempfile
import unittest

from minirel import (
    Cancelled, Col, CorrCond, ExistsPred, InPred, Query, Subquery, run_query,
)


def in_query():
    return Query("o", select=(Col(0),),
                 where=InPred((Col(0),), Subquery("i", select=(Col(0),))))


def exists_query():
    return Query("o", select=(Col(0),),
                 where=ExistsPred(
                     Subquery("i", corr=(CorrCond(Col(0), Col(0), True),))))


class FlakyOpen:
    """Fails on the Nth call, or returns a handle whose write fails."""

    def __init__(self, fail_on_call, mode="open"):
        self.calls = 0
        self.fail_on_call = fail_on_call
        self.mode = mode

    def __call__(self, path, *args, **kwargs):
        self.calls += 1
        if self.calls == self.fail_on_call:
            if self.mode == "open":
                raise OSError("simulated disk failure on open")
            return _FailWrite(open(path, *args, **kwargs))
        return open(path, *args, **kwargs)


class _FailWrite:
    def __init__(self, fobj):
        self._fobj = fobj

    def write(self, data):
        raise OSError("simulated disk failure on write")

    def close(self):
        self._fobj.close()

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        self.close()


class TestSpill(unittest.TestCase):
    def setUp(self):
        self.outer = [(i % 7 - 3 if i % 11 else None,) for i in range(60)]
        self.inner = [(i % 5 - 2 if i % 7 else None,) for i in range(50)]
        self.tables = {"o": self.outer, "i": self.inner}

    def test_spill_matches_interpreter_and_cleans_up(self):
        with tempfile.TemporaryDirectory() as tmp:
            spill_dir = os.path.join(tmp, "spill")
            ref = run_query(in_query(), self.tables, [self.outer],
                            mode="interpreter")
            opt = run_query(in_query(), self.tables, [self.outer],
                            mode="optimized", mem_budget=4,
                            spill_dir=spill_dir)
            self.assertGreater(opt.stats.partitions_spilled, 0)
            self.assertGreater(opt.stats.partition_loads, 0)
            self.assertEqual(ref.rows, opt.rows)
            # every temporary file is gone after the run
            self.assertEqual(os.listdir(spill_dir), [])

    def test_skewed_correlation_keys(self):
        inner = [(7,)] * 2000 + [(None,)] * 500 + [(3,)]
        outer = [(7,)] * 100 + [(None,)] * 20 + [(3,)]
        tables = {"o": outer, "i": inner}
        with tempfile.TemporaryDirectory() as tmp:
            spill_dir = os.path.join(tmp, "spill")
            ref = run_query(exists_query(), tables, [outer],
                            mode="interpreter")
            opt = run_query(exists_query(), tables, [outer],
                            mode="optimized", mem_budget=8,
                            spill_dir=spill_dir)
            self.assertGreater(opt.stats.partitions_spilled, 10)
            self.assertEqual(ref.rows, opt.rows)
            self.assertEqual(os.listdir(spill_dir), [])

    def test_paged_input(self):
        pages = [self.outer[i:i + 3] for i in range(0, len(self.outer), 3)]
        paged = run_query(in_query(), self.tables, pages, mode="optimized")
        whole = run_query(in_query(), self.tables, [self.outer],
                          mode="optimized")
        self.assertEqual(paged.rows, whole.rows)
        self.assertEqual(paged.stats.outer_rows, len(self.outer))

    def test_disk_failure_on_spill_open(self):
        with tempfile.TemporaryDirectory() as tmp:
            spill_dir = os.path.join(tmp, "spill")
            out_path = os.path.join(tmp, "out.jsonl")
            flaky = FlakyOpen(fail_on_call=3, mode="open")
            with self.assertRaises(OSError):
                run_query(in_query(), self.tables, [self.outer],
                          mode="optimized", mem_budget=4,
                          spill_dir=spill_dir, output_path=out_path,
                          open_fn=flaky)
            # partial spill partitions and outputs are all gone
            self.assertEqual(os.listdir(spill_dir), [])
            self.assertFalse(os.path.exists(out_path))
            self.assertFalse(os.path.exists(out_path + ".tmp"))

    def test_disk_failure_mid_write(self):
        with tempfile.TemporaryDirectory() as tmp:
            spill_dir = os.path.join(tmp, "spill")
            flaky = FlakyOpen(fail_on_call=2, mode="write")
            with self.assertRaises(OSError):
                run_query(in_query(), self.tables, [self.outer],
                          mode="optimized", mem_budget=4,
                          spill_dir=spill_dir, open_fn=flaky)
            self.assertEqual(os.listdir(spill_dir), [])

    def test_output_write_failure_leaves_nothing(self):
        with tempfile.TemporaryDirectory() as tmp:
            out_path = os.path.join(tmp, "out.jsonl")

            def open_fn(path, *args, **kwargs):
                if path.endswith(".tmp"):
                    raise OSError("simulated output failure")
                return open(path, *args, **kwargs)

            with self.assertRaises(OSError):
                run_query(in_query(), self.tables, [self.outer],
                          mode="optimized", output_path=out_path,
                          open_fn=open_fn)
            self.assertFalse(os.path.exists(out_path))
            self.assertFalse(os.path.exists(out_path + ".tmp"))

    def test_cancellation_cleans_up(self):
        with tempfile.TemporaryDirectory() as tmp:
            spill_dir = os.path.join(tmp, "spill")
            out_path = os.path.join(tmp, "out.jsonl")
            counter = {"n": 0}

            def cancel_check():
                counter["n"] += 1
                return counter["n"] > 25

            with self.assertRaises(Cancelled):
                run_query(in_query(), self.tables, [self.outer],
                          mode="optimized", mem_budget=4,
                          spill_dir=spill_dir, output_path=out_path,
                          cancel_check=cancel_check)
            self.assertEqual(os.listdir(spill_dir), [])
            self.assertFalse(os.path.exists(out_path))
            self.assertFalse(os.path.exists(out_path + ".tmp"))

    def test_cancellation_during_index_build(self):
        counter = {"n": 0}

        def cancel_check():
            counter["n"] += 1
            return counter["n"] > 10

        with tempfile.TemporaryDirectory() as tmp:
            spill_dir = os.path.join(tmp, "spill")
            with self.assertRaises(Cancelled):
                run_query(in_query(), self.tables, [self.outer],
                          mode="optimized", mem_budget=2,
                          spill_dir=spill_dir, cancel_check=cancel_check)
            self.assertEqual(os.listdir(spill_dir), [])

    def test_successful_output_file(self):
        import json
        with tempfile.TemporaryDirectory() as tmp:
            out_path = os.path.join(tmp, "out.jsonl")
            opt = run_query(in_query(), self.tables, [self.outer],
                            mode="optimized", output_path=out_path)
            with open(out_path) as fobj:
                lines = [json.loads(line) for line in fobj]
            self.assertEqual(lines, [list(r) for r in opt.rows])
            self.assertFalse(os.path.exists(out_path + ".tmp"))


if __name__ == "__main__":
    unittest.main()
