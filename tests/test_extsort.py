"""Tests for extsort.  All data is synthetic (generated in temp dirs)."""

from __future__ import annotations

import json
import os
import random
import subprocess
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from extsort import (  # noqa: E402
    CorruptRunError,
    ExternalSorter,
    ManifestCorruptError,
    SortInterrupted,
)


class ExternalSortTestBase(unittest.TestCase):
    def setUp(self) -> None:
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        root = self._tmp.name
        self.input_path = os.path.join(root, "input.jsonl")
        self.output_path = os.path.join(root, "output.jsonl")
        self.workdir = os.path.join(root, "work")

    def write_input(self, records: list[dict]) -> None:
        with open(self.input_path, "w", encoding="utf-8") as handle:
            for record in records:
                handle.write(json.dumps(record))
                handle.write("\n")

    def read_output(self) -> list[dict]:
        with open(self.output_path, "r", encoding="utf-8") as handle:
            return [json.loads(line) for line in handle]

    def make_sorter(self, **overrides) -> ExternalSorter:
        params = {
            "input_path": self.input_path,
            "output_path": self.output_path,
            "workdir": self.workdir,
            "memory_budget": 4,
            "handle_budget": 6,
        }
        params.update(overrides)
        return ExternalSorter(**params)

    @staticmethod
    def expected(records: list[dict]) -> list[dict]:
        indexed = [
            (rec["key"], seq, rec.get("value")) for seq, rec in enumerate(records)
        ]
        indexed.sort(key=lambda item: (item[0], item[1]))
        return [
            {"key": key, "seq": seq, "value": value}
            for key, seq, value in indexed
        ]


class BasicSortTests(ExternalSortTestBase):
    def test_empty_input_produces_empty_output(self):
        self.write_input([])
        count = self.make_sorter().sort()
        self.assertEqual(count, 0)
        self.assertEqual(self.read_output(), [])

    def test_single_record(self):
        self.write_input([{"key": 7, "value": "only"}])
        self.make_sorter().sort()
        self.assertEqual(self.read_output(), [{"key": 7, "seq": 0, "value": "only"}])

    def test_random_records_match_python_stable_sort(self):
        rng = random.Random(20261001)
        records = [
            {"key": rng.randint(0, 20), "value": f"v{i}"} for i in range(500)
        ]
        self.write_input(records)
        count = self.make_sorter(memory_budget=7, handle_budget=5).sort()
        self.assertEqual(count, len(records))
        self.assertEqual(self.read_output(), self.expected(records))

    def test_string_keys(self):
        records = [{"key": k, "value": k} for k in ["pear", "apple", "fig", "apple"]]
        self.write_input(records)
        self.make_sorter(memory_budget=2).sort()
        self.assertEqual(self.read_output(), self.expected(records))

    def test_same_key_order_preserved_across_runs_and_merge_rounds(self):
        # Acceptance boundary: three equal keys with input order 1,2,3 must
        # stay in that order.  memory_budget=1 forces one run per record and
        # fan-in 2 forces multiple merge rounds.
        records = [{"key": "same", "value": n} for n in (1, 2, 3)]
        self.write_input(records)
        self.make_sorter(memory_budget=1, handle_budget=4).sort()  # fan-in 2
        output = self.read_output()
        self.assertEqual([rec["value"] for rec in output], [1, 2, 3])
        self.assertEqual([rec["seq"] for rec in output], [0, 1, 2])

    def test_many_equal_keys_multi_round(self):
        records = [{"key": 1, "value": i} for i in range(40)]
        self.write_input(records)
        self.make_sorter(memory_budget=3, handle_budget=4).sort()
        output = self.read_output()
        self.assertEqual([rec["value"] for rec in output], list(range(40)))

    def test_output_replaces_existing_file(self):
        self.write_input([{"key": 1, "value": "new"}])
        with open(self.output_path, "w", encoding="utf-8") as handle:
            handle.write("stale junk\n")
        self.make_sorter().sort()
        self.assertEqual(self.read_output(), [{"key": 1, "seq": 0, "value": "new"}])

    def test_no_temp_files_left_after_success(self):
        self.write_input([{"key": i % 3, "value": i} for i in range(30)])
        self.make_sorter(memory_budget=2).sort()
        leftovers = [
            name
            for name in os.listdir(os.path.dirname(self.output_path))
            if ".tmp-" in name
        ]
        self.assertEqual(leftovers, [])
        self.assertFalse(os.path.exists(self.workdir))

    def test_invalid_budgets_rejected(self):
        self.write_input([])
        with self.assertRaises(ValueError):
            self.make_sorter(memory_budget=0)
        with self.assertRaises(ValueError):
            self.make_sorter(handle_budget=2)  # fan-in would be 0


class RecoveryTests(ExternalSortTestBase):
    def interruptible_records(self, n: int = 6) -> list[dict]:
        rng = random.Random(7)
        return [{"key": rng.randint(0, 4), "value": f"v{i}"} for i in range(n)]

    def test_resume_after_interrupt_during_split(self):
        records = self.interruptible_records()
        self.write_input(records)
        sorter = self.make_sorter(memory_budget=1, interrupt_after=2)
        with self.assertRaises(SortInterrupted):
            sorter.sort()
        self.assertFalse(os.path.exists(self.output_path))
        count = self.make_sorter(memory_budget=1).sort()
        self.assertEqual(count, len(records))
        self.assertEqual(self.read_output(), self.expected(records))

    def test_resume_after_interrupt_during_merge(self):
        records = self.interruptible_records()
        self.write_input(records)
        # memory_budget=1 -> 6 split writes; interrupt on the 2nd merge write.
        sorter = self.make_sorter(memory_budget=1, handle_budget=4, interrupt_after=8)
        with self.assertRaises(SortInterrupted):
            sorter.sort()
        count = self.make_sorter(memory_budget=1, handle_budget=4).sort()
        self.assertEqual(count, len(records))
        self.assertEqual(self.read_output(), self.expected(records))

    def test_no_duplicate_records_after_recovery(self):
        records = self.interruptible_records(25)
        self.write_input(records)
        sorter = self.make_sorter(memory_budget=2, handle_budget=4, interrupt_after=5)
        with self.assertRaises(SortInterrupted):
            sorter.sort()
        self.make_sorter(memory_budget=2, handle_budget=4).sort()
        output = self.read_output()
        self.assertEqual(len(output), len(records))
        self.assertEqual(sorted(rec["seq"] for rec in output), list(range(25)))

    def test_corrupt_run_file_is_rejected_not_dropped(self):
        records = self.interruptible_records()
        self.write_input(records)
        sorter = self.make_sorter(memory_budget=1, interrupt_after=1)
        with self.assertRaises(SortInterrupted):
            sorter.sort()
        (run_name,) = [
            n for n in os.listdir(self.workdir) if n.endswith(".run")
        ]
        run_path = os.path.join(self.workdir, run_name)
        with open(run_path, "r+b") as handle:
            handle.seek(2)
            handle.write(b"X")  # flip a byte: checksum no longer matches
        with self.assertRaises(CorruptRunError):
            self.make_sorter(memory_budget=1).sort()

    def test_missing_run_file_is_rejected(self):
        records = self.interruptible_records()
        self.write_input(records)
        sorter = self.make_sorter(memory_budget=1, interrupt_after=1)
        with self.assertRaises(SortInterrupted):
            sorter.sort()
        (run_name,) = [
            n for n in os.listdir(self.workdir) if n.endswith(".run")
        ]
        os.unlink(os.path.join(self.workdir, run_name))
        with self.assertRaises(CorruptRunError):
            self.make_sorter(memory_budget=1).sort()

    def test_corrupt_manifest_is_rejected(self):
        records = self.interruptible_records()
        self.write_input(records)
        sorter = self.make_sorter(memory_budget=1, interrupt_after=1)
        with self.assertRaises(SortInterrupted):
            sorter.sort()
        manifest = os.path.join(self.workdir, "manifest.json")
        with open(manifest, "a", encoding="utf-8") as handle:
            handle.write("garbage")
        with self.assertRaises(ManifestCorruptError):
            self.make_sorter(memory_budget=1).sort()

    def test_changed_input_is_rejected_on_resume(self):
        records = self.interruptible_records()
        self.write_input(records)
        sorter = self.make_sorter(memory_budget=1, interrupt_after=1)
        with self.assertRaises(SortInterrupted):
            sorter.sort()
        self.write_input([{"key": 999, "value": "tampered"}] + records)
        with self.assertRaises(ManifestCorruptError):
            self.make_sorter(memory_budget=1).sort()

    def test_full_crash_resume_cycle_via_cli(self):
        records = self.interruptible_records(50)
        self.write_input(records)
        sorter = self.make_sorter(memory_budget=3, handle_budget=5, interrupt_after=4)
        with self.assertRaises(SortInterrupted):
            sorter.sort()
        result = subprocess.run(
            [
                sys.executable,
                "-m",
                "extsort",
                self.input_path,
                self.output_path,
                "--workdir",
                self.workdir,
                "--memory",
                "3",
                "--handle-budget",
                "5",
            ],
            cwd=os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
            capture_output=True,
            text=True,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.read_output(), self.expected(records))


class CliTests(ExternalSortTestBase):
    def run_cli(self, *extra: str) -> subprocess.CompletedProcess:
        return subprocess.run(
            [sys.executable, "-m", "extsort", *extra],
            cwd=os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
            capture_output=True,
            text=True,
        )

    def test_cli_sorts_file(self):
        records = [{"key": k, "value": f"v{k}"} for k in (5, 3, 9, 3, 1)]
        self.write_input(records)
        result = self.run_cli(
            self.input_path, self.output_path, "--memory", "2",
            "--handle-budget", "4",
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("5 records", result.stdout)
        self.assertEqual(self.read_output(), self.expected(records))

    def test_cli_reports_corruption_with_exit_code_2(self):
        records = [{"key": i, "value": i} for i in range(4)]
        self.write_input(records)
        sorter = self.make_sorter(memory_budget=1, interrupt_after=1)
        with self.assertRaises(SortInterrupted):
            sorter.sort()
        (run_name,) = [
            n for n in os.listdir(self.workdir) if n.endswith(".run")
        ]
        os.unlink(os.path.join(self.workdir, run_name))
        result = self.run_cli(
            self.input_path, self.output_path, "--workdir", self.workdir,
            "--memory", "1",
        )
        self.assertEqual(result.returncode, 2)
        self.assertIn("run file missing", result.stderr)


if __name__ == "__main__":
    unittest.main()
