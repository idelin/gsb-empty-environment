import subprocess
import sys
import unittest
from pathlib import Path


class ReplayDemo(unittest.TestCase):
    def test_replay_demo_passes(self):
        root = Path(__file__).resolve().parent.parent
        proc = subprocess.run(
            [sys.executable, str(root / "examples" / "replay_demo.py")],
            capture_output=True,
            text=True,
            cwd=root,
        )
        self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
        self.assertIn("ALL CHECKS PASSED", proc.stdout)


if __name__ == "__main__":
    unittest.main()
