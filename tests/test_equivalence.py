import unittest
from decimal import Decimal

from schemamigrate import (
    Migration,
    RenameColumn,
    RoundingPolicy,
    Schema,
    TypeTag,
    WidenType,
    check_path_equivalence,
    make_column,
    plan_migration,
)


class EquivalenceTest(unittest.TestCase):
    def test_commuting_paths_are_value_equivalent(self):
        col = make_column("n", TypeTag.INT, nullable=False, column_id="N")
        src = Schema([col])
        path1 = Migration(src, [RenameColumn("N", "m"), WidenType("N", TypeTag.DECIMAL)])
        path2 = Migration(src, [WidenType("N", TypeTag.DECIMAL), RenameColumn("N", "m")])
        report = check_path_equivalence(path1, path2)
        self.assertTrue(report.equivalent, report.detail)

    def test_distinct_rounding_policies_yield_counterexample(self):
        col = make_column("r", TypeTag.DECIMAL, nullable=False, column_id="R")
        src = Schema([col])
        dst = Schema([make_column("r", TypeTag.INT, nullable=False, column_id="R")])
        truncate = plan_migration(src, dst, policies={"R": RoundingPolicy.TRUNCATE})
        half_up = plan_migration(src, dst, policies={"R": RoundingPolicy.ROUND_HALF_UP})
        report = check_path_equivalence(truncate, half_up)
        self.assertFalse(report.equivalent)
        self.assertIsNotNone(report.counterexample)
        self.assertIn(Decimal("1.5"), report.counterexample.column_values("r"))
        self.assertEqual(report.left_output.column_values("r")[1], 1)
        self.assertEqual(report.right_output.column_values("r")[1], 2)

    def test_target_schema_mismatch_reported(self):
        col = make_column("n", TypeTag.INT, nullable=False, column_id="N")
        src = Schema([col])
        m1 = Migration(src, [RenameColumn("N", "x")])
        m2 = Migration(src, [RenameColumn("N", "y")])
        report = check_path_equivalence(m1, m2)
        self.assertFalse(report.equivalent)
        self.assertIn("target schemas differ", report.detail)

    def test_composed_path_equals_single_step_path(self):
        col = make_column("n", TypeTag.INT, nullable=False, column_id="N")
        src = Schema([col])
        mid = Schema([make_column("m", TypeTag.INT, nullable=False, column_id="N")])
        dst = Schema([make_column("m", TypeTag.DECIMAL, nullable=False, column_id="N")])
        composed = plan_migration(src, mid).then(plan_migration(mid, dst))
        direct = plan_migration(src, dst)
        report = check_path_equivalence(composed, direct)
        self.assertTrue(report.equivalent, report.detail)


if __name__ == "__main__":
    unittest.main()
