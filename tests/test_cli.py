import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

REPO = {
    "packages": {
        "app": {
            "1.0.0": {"dependencies": {"lib": ">=1.0.0,<2.0.0"}},
            "2.0.0": {"dependencies": {"lib": ">=2.0.0"}},
        },
        "lib": {"1.5.0": {}, "2.0.0": {}, "2.1.0": {}},
    }
}


def write(tmp, name, data):
    path = Path(tmp) / name
    path.write_text(json.dumps(data), encoding="utf-8")
    return str(path)


def run_cli(*args):
    return subprocess.run(
        [sys.executable, "-m", "depsolver", *args],
        cwd=ROOT,
        capture_output=True,
        text=True,
    )


class TestCli(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        tmp = self._tmp.name
        self.repo = write(tmp, "repo.json", REPO)
        self.lock = write(
            tmp, "lock.json", {"locked": {"app": "1.0.0", "lib": "1.5.0"}}
        )
        self.reqs = write(
            tmp, "reqs.json", {"requirements": {"app": "*", "lib": ">=2.0.0"}}
        )
        self.out = str(Path(tmp) / "new-lock.json")

    def tearDown(self):
        self._tmp.cleanup()

    def base_args(self):
        return [
            "resolve",
            "--repo", self.repo,
            "--lock", self.lock,
            "--requirements", self.reqs,
        ]

    def test_solved_exit_zero_and_writes_lock(self):
        proc = run_cli(*self.base_args(), "--write-lock", self.out)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertIn("status: solved", proc.stdout)
        self.assertIn("chain: root -> lib", proc.stdout)
        new_lock = json.loads(Path(self.out).read_text())
        self.assertEqual(
            new_lock, {"locked": {"app": "2.0.0", "lib": "2.1.0"}}
        )

    def test_json_output(self):
        proc = run_cli(*self.base_args(), "--json")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        report = json.loads(proc.stdout)
        self.assertEqual(report["status"], "solved")
        self.assertEqual(report["resolution"]["lib"], "2.1.0")

    def test_unsatisfiable_exit_one(self):
        reqs = write(
            self._tmp.name, "bad-reqs.json", {"requirements": {"lib": ">=9.0.0"}}
        )
        proc = run_cli(
            "resolve", "--repo", self.repo, "--lock", self.lock,
            "--requirements", reqs,
        )
        self.assertEqual(proc.returncode, 1)
        self.assertIn("status: unsatisfiable", proc.stdout)

    def test_limit_exit_two(self):
        proc = run_cli(*self.base_args(), "--max-states", "1")
        self.assertEqual(proc.returncode, 2)
        self.assertIn("status: search-limit-reached", proc.stdout)

    def test_lock_error_exit_three(self):
        bad_lock = write(
            self._tmp.name, "bad-lock.json", {"locked": {"lib": "9.9.9"}}
        )
        proc = run_cli(
            "resolve", "--repo", self.repo, "--lock", bad_lock,
            "--requirements", self.reqs,
        )
        self.assertEqual(proc.returncode, 3)
        self.assertIn("lock error:", proc.stderr)
        self.assertIn("9.9.9", proc.stderr)


if __name__ == "__main__":
    unittest.main()
