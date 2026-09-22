import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
VERIFIER = ROOT / "scripts" / "verify_trajectory.py"
SCENARIO = ROOT / "scripts" / "run_scenario.py"


class VerifierTests(unittest.TestCase):
    def _run_verifier(self, records):
        with tempfile.NamedTemporaryFile("w", suffix=".jsonl", delete=False) as fh:
            for record in records:
                fh.write(json.dumps(record) + "\n")
            path = fh.name
        proc = subprocess.run(
            [sys.executable, str(VERIFIER), path],
            capture_output=True, text=True,
        )
        Path(path).unlink(missing_ok=True)
        return proc

    def test_real_scenario_passes(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / "state"
            trace = Path(tmp) / "trace.jsonl"
            proc = subprocess.run(
                [sys.executable, str(SCENARIO),
                 "--root", str(root), "--trajectory", str(trace)],
                capture_output=True, text=True,
            )
            self.assertEqual(proc.returncode, 0, proc.stderr)
            proc = subprocess.run(
                [sys.executable, str(VERIFIER), str(trace)],
                capture_output=True, text=True,
            )
            self.assertEqual(proc.returncode, 0, proc.stdout)

    def test_gap_jump_is_rejected(self):
        records = [
            {"step": 1, "t": "CONFIG", "partition": "P", "modulus": 16,
             "initial_seq": 1, "initial_abs": 0, "capacity_live": 64,
             "memory_slots": 4, "inflight_max": 8, "reorder_window": 7},
            {"step": 2, "t": "STUCK_BEGIN", "partition": "X"},
            {"step": 3, "t": "STUCK_END", "partition": "X"},
            {"step": 4, "t": "DELIVER", "delivery_id": "dlv:P:0",
             "partition": "P", "epoch": 1, "seq": 1, "abs": 0},
            # seq 2 missing, no proof: jumping to abs 2 is forbidden
            {"step": 5, "t": "DELIVER", "delivery_id": "dlv:P:2",
             "partition": "P", "epoch": 1, "seq": 3, "abs": 2},
            {"step": 6, "t": "CRASH"},
            {"step": 7, "t": "RECOVER"},
        ]
        proc = self._run_verifier(records)
        self.assertEqual(proc.returncode, 1)
        self.assertIn("jumped over ungapped", proc.stdout)

    def test_unstable_id_is_rejected(self):
        records = [
            {"step": 1, "t": "CONFIG", "partition": "P", "modulus": 16,
             "initial_seq": 1, "initial_abs": 0, "capacity_live": 64,
             "memory_slots": 4, "inflight_max": 8, "reorder_window": 7},
            {"step": 2, "t": "STUCK_BEGIN", "partition": "X"},
            {"step": 3, "t": "STUCK_END", "partition": "X"},
            {"step": 4, "t": "DELIVER", "delivery_id": "dlv:P:0",
             "partition": "P", "epoch": 1, "seq": 1, "abs": 0},
            {"step": 5, "t": "REDELIVER", "delivery_id": "dlv:P:99",
             "partition": "P", "epoch": 1, "seq": 1, "abs": 0},
            {"step": 6, "t": "CRASH"},
            {"step": 7, "t": "RECOVER"},
        ]
        proc = self._run_verifier(records)
        self.assertEqual(proc.returncode, 1)
        self.assertIn("unstable id", proc.stdout)


if __name__ == "__main__":
    unittest.main()
