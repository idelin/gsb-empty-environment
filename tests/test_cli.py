import io
import unittest
from contextlib import redirect_stdout

from timefit.cli import main


class DemoTests(unittest.TestCase):
    def test_demo_runs_end_to_end(self):
        buf = io.StringIO()
        with redirect_stdout(buf):
            rc = main()
        self.assertEqual(rc, 0)
        out = buf.getvalue()
        self.assertIn("min removals:  2", out)
        self.assertIn("removed:       [3, 8]", out)
        self.assertIn("conflict cores", out)
        self.assertIn("extrapolation", out)
        self.assertIn("rejected", out)
        self.assertIn("budget_undecided", out)


if __name__ == "__main__":
    unittest.main()
