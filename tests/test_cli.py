import json
import subprocess
import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
EXAMPLES = ROOT / "examples"


def run_merge(base, ours, theirs, out=None):
    cmd = [sys.executable, "-m", "lockmerge",
           "--base", str(EXAMPLES / base),
           "--ours", str(EXAMPLES / ours),
           "--theirs", str(EXAMPLES / theirs),
           "--registry", str(EXAMPLES / "registry.json")]
    if out:
        cmd += ["--out", str(out)]
    return subprocess.run(cmd, cwd=ROOT, capture_output=True, text=True)


class TestCli(unittest.TestCase):
    def test_clean_merge_exit_zero(self):
        proc = run_merge("base.lock.json", "ours.clean.lock.json",
                         "theirs.clean.lock.json")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        payload = json.loads(proc.stdout)
        self.assertEqual(payload["status"], "merged")
        self.assertEqual(payload["unprocessable"], [])
        self.assertEqual(payload["sources"]["packages"]["web"], "ours")
        self.assertEqual(payload["sources"]["packages"]["cache"], "theirs")

    def test_conflict_merge_exit_two(self):
        proc = run_merge("base.lock.json", "ours.conflict.lock.json",
                         "theirs.conflict.lock.json")
        self.assertEqual(proc.returncode, 2, proc.stderr)
        payload = json.loads(proc.stdout)
        self.assertEqual(payload["status"], "needs_manual")
        self.assertTrue(payload["conflict_hunks"])
        self.assertTrue(payload["closure_violations"])
        self.assertTrue(payload["unprocessable"])

    def test_delete_rejection_exit_two(self):
        proc = run_merge("base.lock.json", "ours.delete.lock.json",
                         "theirs.delete.lock.json")
        self.assertEqual(proc.returncode, 2, proc.stderr)
        payload = json.loads(proc.stdout)
        self.assertEqual(len(payload["rejected_deletions"]), 1)
        self.assertEqual(payload["rejected_deletions"][0]["package"], "web")

    def test_out_file_written(self):
        import tempfile
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp) / "merged.json"
            proc = run_merge("base.lock.json", "ours.clean.lock.json",
                             "theirs.clean.lock.json", out=out)
            self.assertEqual(proc.returncode, 0, proc.stderr)
            payload = json.loads(out.read_text())
            self.assertEqual(payload["status"], "merged")


if __name__ == "__main__":
    unittest.main()
