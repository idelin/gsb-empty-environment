import json
import os
import random
import subprocess
import sys
import tempfile
import unittest

import extsort
from extsort import (
    CorruptSegmentError,
    ExternalSorter,
    InputFormatError,
    ManifestError,
)

EXTSORT_PY = os.path.join(os.path.dirname(os.path.abspath(__file__)), "extsort.py")


class SimulatedCrash(Exception):
    """Raised by CrashHook to simulate an interruption."""


class CrashHook:
    """Checkpoint hook that 'crashes' on the Nth durable manifest commit."""

    def __init__(self, fail_on_call):
        self.fail_on_call = fail_on_call
        self.calls = 0

    def __call__(self, event):
        self.calls += 1
        if self.calls == self.fail_on_call:
            raise SimulatedCrash(f"simulated crash at commit {self.calls}: {event}")


def write_input(path, records):
    """records: iterable of (key, value); all data in these tests is synthetic."""
    with open(path, "w", encoding="utf-8") as fh:
        for key, value in records:
            fh.write(json.dumps({"key": key, "value": value}) + "\n")


def read_output(path):
    with open(path, encoding="utf-8") as fh:
        return [json.loads(line) for line in fh if line.strip()]


def reference_sort(records):
    """Python's stable sort as ground truth: by key, ties keep input order."""
    return sorted(enumerate(records), key=lambda t: (t[1][0], t[0]))


class ExtsortTestBase(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.dir = self.tmp.name
        self.input_path = os.path.join(self.dir, "input.jsonl")
        self.output_path = os.path.join(self.dir, "output.jsonl")
        self.work_dir = os.path.join(self.dir, "work")

    def make_sorter(self, memory_records=4, fan_in=3, hook=None, keep_work=True):
        return ExternalSorter(
            self.input_path,
            self.output_path,
            memory_records=memory_records,
            fan_in=fan_in,
            work_dir=self.work_dir,
            keep_work=keep_work,
            checkpoint_hook=hook,
        )


class TestStability(ExtsortTestBase):
    def test_three_same_key_records_keep_input_order(self):
        # Acceptance boundary: same-key records entered as 1,2,3 must come
        # out as 1,2,3 no matter how the runs are split and merged.
        records = [
            ("b", "other-1"),
            ("a", "rec1"),
            ("c", "other-2"),
            ("a", "rec2"),
            ("a", "rec3"),
            ("b", "other-3"),
        ]
        write_input(self.input_path, records)
        # memory_records=1 forces one segment per record; fan_in=2 forces
        # many merge rounds.
        self.make_sorter(memory_records=1, fan_in=2).sort()
        out = read_output(self.output_path)
        a_values = [r["value"] for r in out if r["key"] == "a"]
        self.assertEqual(a_values, ["rec1", "rec2", "rec3"])
        a_seqs = [r["seq"] for r in out if r["key"] == "a"]
        self.assertEqual(a_seqs, [1, 3, 4])

    def test_stability_across_many_segments_and_rounds(self):
        rng = random.Random(20261001)
        records = [(rng.choice(["x", "y", "z"]), f"v{i}") for i in range(60)]
        write_input(self.input_path, records)
        self.make_sorter(memory_records=3, fan_in=2).sort()
        out = read_output(self.output_path)
        expected = reference_sort(records)
        self.assertEqual([r["value"] for r in out], [records[i][1] for i, _ in expected])
        self.assertEqual([r["seq"] for r in out], [i for i, _ in expected])

    def test_matches_builtin_sort_random_numeric_keys(self):
        rng = random.Random(7)
        records = [(rng.randint(0, 20), rng.randint(0, 10**6)) for _ in range(500)]
        write_input(self.input_path, records)
        self.make_sorter(memory_records=17, fan_in=3).sort()
        out = read_output(self.output_path)
        expected = reference_sort(records)
        self.assertEqual([r["key"] for r in out], [records[i][0] for i, _ in expected])
        self.assertEqual([r["seq"] for r in out], [i for i, _ in expected])

    def test_single_record(self):
        write_input(self.input_path, [("only", 1)])
        self.make_sorter().sort()
        self.assertEqual(
            read_output(self.output_path), [{"key": "only", "seq": 0, "value": 1}]
        )

    def test_empty_input(self):
        write_input(self.input_path, [])
        self.make_sorter().sort()
        self.assertEqual(read_output(self.output_path), [])


class TestRecovery(ExtsortTestBase):
    def _records(self, n=16):
        rng = random.Random(99)
        return [(rng.randint(0, 5), f"v{i}") for i in range(n)]

    def test_resume_after_crash_during_run_generation(self):
        records = self._records()
        write_input(self.input_path, records)
        hook = CrashHook(fail_on_call=3)  # crash after 3rd segment commit
        with self.assertRaises(SimulatedCrash):
            self.make_sorter(memory_records=2, fan_in=2, hook=hook).sort()
        self.assertFalse(os.path.exists(self.output_path))
        # Resume with a fresh sorter instance; no hook this time.
        self.make_sorter(memory_records=2, fan_in=2).sort()
        out = read_output(self.output_path)
        expected = reference_sort(records)
        self.assertEqual([r["seq"] for r in out], [i for i, _ in expected])
        self.assertEqual(len(out), len(records))

    def test_resume_after_crash_during_merge_rounds(self):
        records = self._records()
        write_input(self.input_path, records)
        # memory=2 -> 8 segments -> commits 1..8 are runs, 9 is input_complete,
        # 10+ are merge rounds (fan_in=2 needs several rounds).
        hook = CrashHook(fail_on_call=12)
        with self.assertRaises(SimulatedCrash):
            self.make_sorter(memory_records=2, fan_in=2, hook=hook).sort()
        self.make_sorter(memory_records=2, fan_in=2).sort()
        out = read_output(self.output_path)
        expected = reference_sort(records)
        self.assertEqual([r["seq"] for r in out], [i for i, _ in expected])

    def test_record_count_not_duplicated_after_resume(self):
        records = self._records(n=25)
        write_input(self.input_path, records)
        hook = CrashHook(fail_on_call=5)
        with self.assertRaises(SimulatedCrash):
            self.make_sorter(memory_records=2, fan_in=2, hook=hook).sort()
        self.make_sorter(memory_records=2, fan_in=2).sort()
        out = read_output(self.output_path)
        seqs = sorted(r["seq"] for r in out)
        self.assertEqual(seqs, list(range(len(records))))
        self.assertEqual(len(out), len(records))

    def test_resume_is_idempotent_when_run_again(self):
        records = self._records()
        write_input(self.input_path, records)
        self.make_sorter(memory_records=2, fan_in=2).sort()
        first = read_output(self.output_path)
        # Running again from scratch state must reproduce identical output.
        self.make_sorter(memory_records=2, fan_in=2).sort()
        self.assertEqual(read_output(self.output_path), first)


class TestCorruption(ExtsortTestBase):
    def _crash_midway(self):
        records = [(i % 3, f"v{i}") for i in range(12)]
        write_input(self.input_path, records)
        hook = CrashHook(fail_on_call=2)
        with self.assertRaises(SimulatedCrash):
            self.make_sorter(memory_records=2, fan_in=2, hook=hook).sort()
        return records

    def _segment_files(self):
        return sorted(
            f for f in os.listdir(self.work_dir)
            if f.startswith("run-") and f.endswith(".seg")
        )

    def test_corrupt_segment_is_not_silently_dropped(self):
        self._crash_midway()
        victim = os.path.join(self.work_dir, self._segment_files()[0])
        with open(victim, "ab") as fh:
            fh.write(b"corrupted-bytes")
        with self.assertRaises(CorruptSegmentError):
            self.make_sorter(memory_records=2, fan_in=2).sort()
        # No output may be published when a segment is corrupt.
        self.assertFalse(os.path.exists(self.output_path))

    def test_missing_segment_is_not_silently_dropped(self):
        self._crash_midway()
        os.remove(os.path.join(self.work_dir, self._segment_files()[0]))
        with self.assertRaises(CorruptSegmentError):
            self.make_sorter(memory_records=2, fan_in=2).sort()
        self.assertFalse(os.path.exists(self.output_path))

    def test_truncated_segment_detected(self):
        self._crash_midway()
        victim = os.path.join(self.work_dir, self._segment_files()[0])
        with open(victim, "r+b") as fh:
            fh.truncate(max(0, os.path.getsize(victim) - 5))
        with self.assertRaises(CorruptSegmentError):
            self.make_sorter(memory_records=2, fan_in=2).sort()

    def test_changed_input_after_crash_rejected(self):
        self._crash_midway()
        # Rewrite the input with different content; resume must refuse.
        write_input(self.input_path, [("z", 999)])
        with self.assertRaises(ManifestError):
            self.make_sorter(memory_records=2, fan_in=2).sort()

    def test_manifest_input_mismatch_rejected(self):
        self._crash_midway()
        other_input = os.path.join(self.dir, "other.jsonl")
        write_input(other_input, [("a", 1)])
        sorter = ExternalSorter(
            other_input, self.output_path, work_dir=self.work_dir
        )
        with self.assertRaises(ManifestError):
            sorter.sort()


class TestAtomicOutput(ExtsortTestBase):
    def test_output_replaces_existing_file_atomically(self):
        records = [(3, "c"), (1, "a"), (2, "b")]
        write_input(self.input_path, records)
        with open(self.output_path, "w") as fh:
            fh.write("stale garbage that must be replaced\n")
        self.make_sorter(memory_records=1, fan_in=2).sort()
        out = read_output(self.output_path)
        self.assertEqual([r["key"] for r in out], [1, 2, 3])
        # No temporary output files may be left behind.
        leftovers = [f for f in os.listdir(self.dir) if ".tmp-" in f]
        self.assertEqual(leftovers, [])

    def test_no_partial_output_on_crash(self):
        records = [(i, f"v{i}") for i in range(10)]
        write_input(self.input_path, records)
        hook = CrashHook(fail_on_call=4)
        with self.assertRaises(SimulatedCrash):
            self.make_sorter(memory_records=2, fan_in=2, hook=hook).sort()
        self.assertFalse(os.path.exists(self.output_path))


class TestInputValidation(ExtsortTestBase):
    def test_invalid_json_line_rejected(self):
        with open(self.input_path, "w") as fh:
            fh.write('{"key": 1, "value": "ok"}\nnot-json\n')
        with self.assertRaises(InputFormatError):
            self.make_sorter().sort()

    def test_missing_key_rejected(self):
        with open(self.input_path, "w") as fh:
            fh.write('{"value": 1}\n')
        with self.assertRaises(InputFormatError):
            self.make_sorter().sort()

    def test_mixed_key_types_rejected(self):
        write_input(self.input_path, [("a", 1), (2, 2)])
        with self.assertRaises(InputFormatError):
            self.make_sorter().sort()

    def test_invalid_budgets_rejected(self):
        write_input(self.input_path, [(1, 1)])
        with self.assertRaises(ValueError):
            ExternalSorter(self.input_path, self.output_path, memory_records=0)
        with self.assertRaises(ValueError):
            ExternalSorter(self.input_path, self.output_path, fan_in=1)


class TestCli(ExtsortTestBase):
    def test_cli_end_to_end_and_resume(self):
        rng = random.Random(4242)
        records = [(rng.randint(0, 9), f"v{i}") for i in range(50)]
        write_input(self.input_path, records)
        cmd = [
            sys.executable, EXTSORT_PY,
            self.input_path, self.output_path,
            "--memory-records", "3",
            "--fan-in", "2",
            "--work-dir", self.work_dir,
        ]
        proc = subprocess.run(cmd, capture_output=True, text=True)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        out = read_output(self.output_path)
        expected = reference_sort(records)
        self.assertEqual([r["seq"] for r in out], [i for i, _ in expected])
        # Work dir is cleaned up by default (no --keep-work).
        self.assertFalse(os.path.exists(self.work_dir))

    def test_cli_reports_corrupt_segment_with_nonzero_exit(self):
        records = [(i % 2, f"v{i}") for i in range(8)]
        write_input(self.input_path, records)
        hook = CrashHook(fail_on_call=2)
        with self.assertRaises(SimulatedCrash):
            self.make_sorter(memory_records=2, fan_in=2, hook=hook).sort()
        victim = sorted(
            f for f in os.listdir(self.work_dir) if f.endswith(".seg")
        )[0]
        with open(os.path.join(self.work_dir, victim), "ab") as fh:
            fh.write(b"junk")
        cmd = [
            sys.executable, EXTSORT_PY,
            self.input_path, self.output_path,
            "--memory-records", "2",
            "--fan-in", "2",
            "--work-dir", self.work_dir,
        ]
        proc = subprocess.run(cmd, capture_output=True, text=True)
        self.assertEqual(proc.returncode, 2)
        self.assertIn("checksum", proc.stderr)
        self.assertFalse(os.path.exists(self.output_path))


if __name__ == "__main__":
    unittest.main()
