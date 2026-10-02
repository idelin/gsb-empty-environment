"""Run every counterexample script as a subprocess: they must pass standalone."""
import pathlib
import subprocess
import sys
import unittest

ROOT = pathlib.Path(__file__).resolve().parent.parent
SCRIPTS = sorted((ROOT / "counterexamples").glob("ce_*.py"))


class CounterexampleTest(unittest.TestCase):
    pass


def _make(script):
    def run(self):
        proc = subprocess.run(
            [sys.executable, str(script)],
            cwd=ROOT,
            capture_output=True,
            text=True,
            timeout=60,
        )
        self.assertEqual(
            proc.returncode,
            0,
            f"{script.name} failed:\n{proc.stdout}\n{proc.stderr}",
        )
        self.assertIn("PASS", proc.stdout)

    return run


for _script in SCRIPTS:
    setattr(
        CounterexampleTest,
        f"test_{_script.stem}",
        _make(_script),
    )

if __name__ == "__main__":
    unittest.main()
