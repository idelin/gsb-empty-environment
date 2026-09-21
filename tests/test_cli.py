import io
import unittest
from pathlib import Path

from minihm.cli import run_file

EXAMPLES = Path(__file__).resolve().parent.parent / "examples"


def run(name: str):
    out, err = io.StringIO(), io.StringIO()
    code = run_file(str(EXAMPLES / name), out=out, err=err)
    return code, out.getvalue(), err.getvalue()


class CliTest(unittest.TestCase):
    def test_identity_example(self):
        code, out, _ = run("identity.mh")
        self.assertEqual(code, 0)
        self.assertIn("type: Int", out)
        self.assertIn("value: 1", out)

    def test_compose_example(self):
        code, out, _ = run("compose.mh")
        self.assertEqual(code, 0)
        self.assertIn("type: Int", out)
        self.assertIn("value: 7", out)

    def test_unicode_example(self):
        code, out, _ = run("unicode.mh")
        self.assertEqual(code, 0)
        self.assertIn("type: Int", out)
        self.assertIn("value: 5", out)

    def test_self_application_example_rejected(self):
        code, _, err = run("self_application.mh")
        self.assertEqual(code, 1)
        self.assertIn("infinite type", err)
        self.assertIn("occurs check", err)

    def test_bad_add_example_reports_constraint_chain(self):
        code, _, err = run("bad_add.mh")
        self.assertEqual(code, 1)
        self.assertIn("type mismatch", err)
        self.assertIn("constraint chain", err)
        self.assertIn("Bool", err)
        self.assertIn("Int", err)
        self.assertIn("bytes", err)

    def test_bad_if_example(self):
        code, _, err = run("bad_if.mh")
        self.assertEqual(code, 1)
        self.assertIn("condition of `if`", err)


if __name__ == "__main__":
    unittest.main()
