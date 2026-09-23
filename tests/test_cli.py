import io
import unittest
from contextlib import redirect_stdout

from rational_id.cli import main


class CliTest(unittest.TestCase):
    def run_cli(self, *argv):
        buf = io.StringIO()
        with redirect_stdout(buf):
            code = main(list(argv))
        return code, buf.getvalue()

    def test_identical_exit_code(self):
        code, out = self.run_cli("(x+1)^2", "x^2+2*x+1")
        self.assertEqual(code, 0)
        self.assertIn("结论：恒等", out)
        self.assertIn("适用域", out)

    def test_not_identical_exit_code(self):
        code, out = self.run_cli("x/x", "1")
        self.assertEqual(code, 1)
        self.assertIn("结论：不恒等", out)
        self.assertIn("反例：x = 0", out)

    def test_unknown_exit_code(self):
        code, out = self.run_cli("sin(x)", "sin(x)")
        self.assertEqual(code, 2)
        self.assertIn("结论：未知", out)


if __name__ == "__main__":
    unittest.main()
