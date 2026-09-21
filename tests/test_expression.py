import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from license_solver.expression import And, License, Or, With, evaluate, or_nodes, parse
from license_solver.model import ExceptionInfo, LicenseInfo

LICENSES = {
    "MIT": LicenseInfo(frozenset({"notice"}), frozenset({"permissive"})),
    "Apache-2.0": LicenseInfo(frozenset({"notice"}), frozenset({"permissive"})),
    "GPL-2.0-only": LicenseInfo(
        frozenset({"notice", "disclose", "same-license"}),
        frozenset({"copyleft-strong"}),
    ),
}

EXCEPTIONS = {
    "Classpath-Exception-2.0": ExceptionInfo(
        "independent_modules",
        frozenset({"disclose", "same-license"}),
        frozenset({"copyleft-strong"}),
    )
}


def eval_expr(text, branches=(), attributes=None):
    node = parse(text)
    lics, pos = evaluate(
        node, branches, 0, attributes or {}, LICENSES, EXCEPTIONS
    )
    return lics, pos


class ParseTest(unittest.TestCase):
    def test_or_and_precedence(self):
        node = parse("MIT OR Apache-2.0 AND GPL-2.0-only")
        self.assertIsInstance(node, Or)
        self.assertIsInstance(node.right, And)
        self.assertEqual(node.left, License("MIT"))

    def test_parentheses_override_precedence(self):
        node = parse("(MIT OR Apache-2.0) AND GPL-2.0-only")
        self.assertIsInstance(node, And)
        self.assertIsInstance(node.left, Or)

    def test_with_binds_to_single_license(self):
        node = parse("GPL-2.0-only WITH Classpath-Exception-2.0 AND MIT")
        self.assertIsInstance(node, And)
        self.assertIsInstance(node.left, With)
        self.assertEqual(node.left.exception, "Classpath-Exception-2.0")
        self.assertEqual(node.right, License("MIT"))

    def test_with_rejects_compound_operand(self):
        with self.assertRaises(ValueError):
            parse("(MIT OR Apache-2.0) WITH Classpath-Exception-2.0")

    def test_keywords_case_insensitive(self):
        node = parse("mit or MIT")
        self.assertIsInstance(node, Or)

    def test_malformed_expressions_rejected(self):
        for bad in ["", "MIT AND", "OR MIT", "(MIT", "MIT)", "MIT WITH"]:
            with self.assertRaises(ValueError, msg=bad):
                parse(bad)

    def test_or_nodes_left_to_right(self):
        node = parse("MIT OR Apache-2.0 AND (MIT OR GPL-2.0-only)")
        self.assertEqual(len(or_nodes(node)), 2)


class EvaluateTest(unittest.TestCase):
    def test_and_unions_obligations(self):
        lics, _ = eval_expr("MIT AND GPL-2.0-only")
        obligations = set().union(*(l.obligations for l in lics))
        self.assertEqual(obligations, {"notice", "disclose", "same-license"})

    def test_or_selects_exactly_one_branch(self):
        lics, _ = eval_expr("MIT OR GPL-2.0-only", branches=(0,))
        self.assertEqual([l.name for l in lics], ["MIT"])
        lics, _ = eval_expr("MIT OR GPL-2.0-only", branches=(1,))
        self.assertEqual([l.name for l in lics], ["GPL-2.0-only"])

    def test_with_condition_unmet_keeps_base_obligations(self):
        # WITH must not be split into an unconditional allowance: when the
        # exception condition fails, the base license applies in full.
        lics, _ = eval_expr(
            "GPL-2.0-only WITH Classpath-Exception-2.0",
            attributes={"independent_modules": False},
        )
        self.assertEqual(lics[0].name, "GPL-2.0-only")
        self.assertIn("disclose", lics[0].obligations)
        self.assertIn("copyleft-strong", lics[0].tags)
        self.assertIn("NOT applied", lics[0].notes[0])

    def test_with_condition_met_applies_exception(self):
        lics, _ = eval_expr(
            "GPL-2.0-only WITH Classpath-Exception-2.0",
            attributes={"independent_modules": True},
        )
        self.assertNotIn("disclose", lics[0].obligations)
        self.assertNotIn("copyleft-strong", lics[0].tags)
        self.assertIn("notice", lics[0].obligations)

    def test_unknown_license_rejected(self):
        with self.assertRaises(ValueError):
            eval_expr("NoSuch-License")


if __name__ == "__main__":
    unittest.main()
