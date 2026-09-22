"""Each counterexample script is independently executable; run them all."""
import os
import subprocess
import sys
import unittest

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SCRIPTS = [
    "chunk_restart.py",
    "lifo_buffer.py",
    "unbounded_buffer.py",
    "lost_cancellation.py",
]


class TestCounterexamples(unittest.TestCase):
    def test_scripts_reproduce_their_flaw(self):
        for script in SCRIPTS:
            with self.subTest(script=script):
                path = os.path.join(ROOT, "counterexamples", script)
                proc = subprocess.run(
                    [sys.executable, path],
                    capture_output=True,
                    text=True,
                    cwd=os.path.join(ROOT, "counterexamples"),
                    timeout=120,
                )
                self.assertEqual(
                    proc.returncode, 0,
                    msg=f"{script} failed:\n{proc.stdout}\n{proc.stderr}",
                )
                self.assertIn("COUNTEREXAMPLE REPRODUCED", proc.stdout)


if __name__ == "__main__":
    unittest.main()
