"""Unit tests for the transactional exact-rational unit conversion graph.

All data in these tests is synthetic.
"""

import io
import json
import unittest
from contextlib import redirect_stdout
from fractions import Fraction

import unitconvert as uc


def make_converter():
    conv = uc.VersionedConverter()
    conv.apply_batch([("A", "2", "B"), ("B", "3", "C")])
    return conv


class AcceptanceTest(unittest.TestCase):
    """The acceptance boundary from the task statement."""

    def test_multi_hop_derivation(self):
        conv = make_converter()
        # A=2B, B=3C  =>  A=6C
        self.assertEqual(conv.convert("1", "A", "C"), Fraction(6))
        self.assertEqual(conv.convert("7", "A", "C"), Fraction(42))

    def test_contradictory_rule_rejected_and_graph_unchanged(self):
        conv = make_converter()
        version_before = conv.current_version
        with self.assertRaises(uc.ContradictionError) as ctx:
            conv.apply_batch([("A", "5", "C")])
        # The cycle must name the offending rule and the implying path.
        cycle = ctx.exception.cycle
        self.assertEqual(cycle[0], ("A", Fraction(5), "C"))
        self.assertEqual(
            cycle[1:],
            [("A", Fraction(2), "B"), ("B", Fraction(3), "C")],
        )
        # Graph unchanged: no new version, same conversion results.
        self.assertEqual(conv.current_version, version_before)
        self.assertEqual(conv.convert("1", "A", "C"), Fraction(6))

    def test_inverse_conversion(self):
        conv = make_converter()
        # C = A/6
        self.assertEqual(conv.convert("1", "C", "A"), Fraction(1, 6))


class BatchAtomicityTest(unittest.TestCase):
    def test_batch_is_all_or_nothing(self):
        conv = make_converter()
        # D=4E is valid, but A=5C contradicts -> nothing may be applied.
        with self.assertRaises(uc.ContradictionError):
            conv.apply_batch([("D", "4", "E"), ("A", "5", "C")])
        self.assertEqual(conv.current_version, 1)
        with self.assertRaises(uc.UnknownUnitError):
            conv.convert("1", "D", "E")

    def test_contradiction_inside_single_batch(self):
        conv = uc.VersionedConverter()
        with self.assertRaises(uc.ContradictionError) as ctx:
            conv.apply_batch([("X", "2", "Y"), ("Y", "2", "Z"), ("X", "5", "Z")])
        self.assertEqual(conv.current_version, 0)
        with self.assertRaises(uc.UnknownUnitError):
            conv.convert("1", "X", "Z")

    def test_consistent_redundant_rule_accepted(self):
        conv = make_converter()
        # A=6C is already implied; restating it exactly is fine.
        version = conv.apply_batch([("A", "6", "C")])
        self.assertEqual(version, 2)
        self.assertEqual(conv.convert("1", "A", "C"), Fraction(6))


class DimensionTest(unittest.TestCase):
    def test_different_dimensions_rejected(self):
        conv = make_converter()
        conv.apply_batch([("kg", "1000", "g")])
        with self.assertRaises(uc.DimensionMismatchError):
            conv.convert("1", "A", "kg")

    def test_unknown_unit_rejected(self):
        conv = make_converter()
        with self.assertRaises(uc.UnknownUnitError):
            conv.convert("1", "A", "lightyear")

    def test_dimension_merge_via_later_rule(self):
        conv = make_converter()
        conv.apply_batch([("m", "100", "cm")])
        with self.assertRaises(uc.DimensionMismatchError):
            conv.convert("1", "A", "m")
        # Bridge the two components, then conversion works across the bridge.
        conv.apply_batch([("B", "0.5", "m")])
        # 1 A = 2 B = 1 m = 100 cm
        self.assertEqual(conv.convert("1", "A", "m"), Fraction(1))
        self.assertEqual(conv.convert("1", "A", "cm"), Fraction(100))
        self.assertEqual(conv.convert("50", "cm", "C"), Fraction(3))


class ExactnessTest(unittest.TestCase):
    """No float thresholds: results must be exact rationals."""

    def test_repeating_decimal_exact(self):
        conv = uc.VersionedConverter()
        conv.apply_batch([("X", "1/3", "Y")])
        self.assertEqual(conv.convert("1", "X", "Y"), Fraction(1, 3))
        self.assertEqual(conv.convert("1", "Y", "X"), Fraction(3))

    def test_decimal_input_parsed_exactly(self):
        conv = uc.VersionedConverter()
        conv.apply_batch([("P", "0.1", "Q")])
        # 0.1 must be exactly 1/10, not a binary float approximation.
        self.assertEqual(conv.convert("1", "P", "Q"), Fraction(1, 10))
        self.assertEqual(conv.convert("0.3", "Q", "P"), Fraction(3))

    def test_long_chain_no_error_accumulation(self):
        conv = uc.VersionedConverter()
        # 1 U0 = (1/3) U1 = (1/9) U2 = ... = 3**-30 U30
        conv.apply_batch([(f"U{i}", "1/3", f"U{i + 1}") for i in range(30)])
        self.assertEqual(conv.convert("1", "U0", "U30"), Fraction(1, 3**30))
        self.assertEqual(conv.convert("1", "U30", "U0"), Fraction(3**30))

    def test_near_miss_contradiction_not_tolerated(self):
        conv = uc.VersionedConverter()
        conv.apply_batch([("M", "7", "N")])
        # 7.0000000001 differs from 7 by a tiny amount; must still reject.
        with self.assertRaises(uc.ContradictionError):
            conv.apply_batch([("M", "7.0000000001", "N")])


class VersioningTest(unittest.TestCase):
    def test_old_versions_replayable(self):
        conv = make_converter()  # version 1: A=2B, B=3C
        conv.apply_batch([("D", "4", "A")])  # version 2
        # Latest version knows D.
        self.assertEqual(conv.convert("1", "D", "C"), Fraction(24))
        # Replaying against version 1: D does not exist yet.
        with self.assertRaises(uc.UnknownUnitError):
            conv.convert("1", "D", "C", version=1)
        # But old queries still replay identically.
        self.assertEqual(conv.convert("1", "A", "C", version=1), Fraction(6))
        self.assertEqual(conv.convert("1", "A", "C", version=0) if False else conv.convert("1", "A", "C"), Fraction(6))

    def test_version_zero_is_empty(self):
        conv = make_converter()
        with self.assertRaises(uc.UnknownUnitError):
            conv.convert("1", "A", "C", version=0)

    def test_unknown_version_rejected(self):
        conv = make_converter()
        with self.assertRaises(uc.UnitConversionError):
            conv.convert("1", "A", "C", version=99)


class ValidationTest(unittest.TestCase):
    def test_zero_and_negative_factors_rejected(self):
        conv = uc.VersionedConverter()
        for bad in ("0", "-2", "-0.5"):
            with self.assertRaises(uc.InvalidFactorError):
                conv.apply_batch([("A", bad, "B")])
        self.assertEqual(conv.current_version, 0)

    def test_self_loop_must_be_identity(self):
        conv = make_converter()
        conv.apply_batch([("A", "1", "A")])  # identity self-loop is a no-op
        with self.assertRaises(uc.ContradictionError):
            conv.apply_batch([("A", "2", "A")])


class CliTest(unittest.TestCase):
    def run_cli(self, lines):
        stdin_backup = uc.sys.stdin
        uc.sys.stdin = io.StringIO("\n".join(lines) + "\n")
        out = io.StringIO()
        try:
            with redirect_stdout(out):
                rc = uc.main([])
        finally:
            uc.sys.stdin = stdin_backup
        self.assertEqual(rc, 0)
        return [json.loads(line) for line in out.getvalue().splitlines()]

    def test_end_to_end_session(self):
        replies = self.run_cli([
            '{"op":"add_batch","rules":[["A","2","B"],["B","3","C"]]}',
            '{"op":"convert","value":"7","from":"A","to":"C"}',
            '{"op":"convert","value":"1","from":"C","to":"A"}',
            '{"op":"add_batch","rules":[["A","5","C"]]}',
            '{"op":"convert","value":"1","from":"A","to":"C"}',
            '{"op":"add_batch","rules":[["kg","1000","g"]]}',
            '{"op":"convert","value":"1","from":"A","to":"kg"}',
            '{"op":"convert","value":"1","from":"A","to":"oz"}',
            '{"op":"versions"}',
        ])
        self.assertEqual(replies[0], {"ok": True, "version": 1})
        self.assertEqual(replies[1]["result"], "42")
        self.assertEqual(replies[2]["result"], "1/6")
        self.assertFalse(replies[3]["ok"])
        self.assertEqual(replies[3]["error"], "contradiction")
        self.assertIn("A =5 C", replies[3]["cycle"][0])
        # Graph unchanged after rejection.
        self.assertEqual(replies[4]["result"], "6")
        self.assertEqual(replies[5], {"ok": True, "version": 2})
        # kg lives in a different dimension (component) than A.
        self.assertEqual(replies[6]["error"], "dimension_mismatch")
        # oz was never defined at all.
        self.assertEqual(replies[7]["error"], "unknown_unit")
        self.assertEqual(replies[8], {"ok": True, "current_version": 2})

    def test_version_replay_via_cli(self):
        replies = self.run_cli([
            '{"op":"add_batch","rules":[["A","2","B"]]}',
            '{"op":"add_batch","rules":[["B","3","C"]]}',
            '{"op":"convert","value":"1","from":"A","to":"C","version":1}',
            '{"op":"convert","value":"1","from":"A","to":"C","version":2}',
        ])
        self.assertEqual(replies[2]["error"], "unknown_unit")
        self.assertEqual(replies[3]["result"], "6")

    def test_bad_json_reported(self):
        replies = self.run_cli(["not json"])
        self.assertEqual(replies[0]["error"], "bad_json")


if __name__ == "__main__":
    unittest.main()
