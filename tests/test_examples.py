import pathlib
import unittest

from recordlang.api import run_program

EXAMPLES = pathlib.Path(__file__).resolve().parent.parent / "examples"


class ExamplesTest(unittest.TestCase):
    def test_points(self):
        out = run_program((EXAMPLES / "points.rl").read_text(encoding="utf-8"))
        self.assertEqual(out, ["{x = 10, y = 0, z = 7}", "17", "{x = 0, y = 0}"])

    def test_width_subtyping(self):
        out = run_program((EXAMPLES / "width_subtyping.rl").read_text(encoding="utf-8"))
        self.assertEqual(out, ["1"])

    def test_functions(self):
        out = run_program((EXAMPLES / "functions.rl").read_text(encoding="utf-8"))
        self.assertEqual(out, ["42"])


if __name__ == "__main__":
    unittest.main()
