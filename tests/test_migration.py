import unittest
from dataclasses import replace
from decimal import Decimal

from schemamigrate import (
    NULL,
    AmbiguousMappingError,
    Batch,
    LossyConversionError,
    MigrationError,
    RoundingPolicy,
    Schema,
    SchemaError,
    TypeTag,
    make_column,
    plan_migration,
    plan_renames,
)


class MigrationTest(unittest.TestCase):
    def setUp(self):
        self.a = make_column("a", TypeTag.INT, nullable=False)
        self.b = make_column("b", TypeTag.DECIMAL)
        self.c = make_column("c", TypeTag.STRING)
        self.schema = Schema([self.a, self.b, self.c], version=1)
        self.batch = Batch(
            self.schema,
            {"a": [1, 2], "b": [Decimal("1.5"), NULL], "c": ["x", "y"]},
        )

    def test_add_nullable_column_fills_null(self):
        new_col = make_column("d", TypeTag.STRING)
        target = Schema([self.a, self.b, self.c, new_col], version=2)
        out, diags = plan_migration(self.schema, target).project(self.batch)
        self.assertEqual(out.column_values("d"), (NULL, NULL))
        self.assertEqual(diags, [])

    def test_add_non_nullable_column_rejected(self):
        new_col = make_column("d", TypeTag.STRING, nullable=False)
        target = Schema([self.a, self.b, self.c, new_col], version=2)
        with self.assertRaises(MigrationError):
            plan_migration(self.schema, target)

    def test_rename_chain_preserves_identity_and_data(self):
        s1 = Schema([replace(self.a, name="a2"), self.b, self.c])
        s2 = Schema([replace(self.a, name="a3"), self.b, self.c])
        mig = plan_migration(self.schema, s1).then(plan_migration(s1, s2))
        out, _ = mig.project(self.batch)
        self.assertEqual(out.schema.get(self.a.column_id).name, "a3")
        self.assertEqual(out.column_values("a3"), (1, 2))

    def test_int_to_decimal_widening_is_exact(self):
        big = 2**70 + 1
        schema = Schema([self.a])
        batch = Batch(schema, {"a": [big]})
        target = Schema([replace(self.a, type=TypeTag.DECIMAL)])
        out, _ = plan_migration(schema, target).project(batch)
        value = out.column_values("a")[0]
        self.assertIsInstance(value, Decimal)
        self.assertEqual(value, Decimal(big))

    def test_decimal_to_int_requires_explicit_policy(self):
        target = Schema([self.a, replace(self.b, type=TypeTag.INT), self.c])
        with self.assertRaises(MigrationError) as ctx:
            plan_migration(self.schema, target)
        self.assertIn("explicit policy", str(ctx.exception))

    def test_decimal_to_int_truncate_keeps_per_value_diagnostics(self):
        target = Schema([self.a, replace(self.b, type=TypeTag.INT), self.c])
        mig = plan_migration(
            self.schema, target, policies={self.b.column_id: RoundingPolicy.TRUNCATE}
        )
        out, diags = mig.project(self.batch)
        self.assertEqual(out.column_values("b"), (1, NULL))
        self.assertEqual(len(diags), 1)
        self.assertEqual(diags[0].row, 0)
        self.assertEqual(diags[0].column, "b")
        self.assertEqual(diags[0].original, Decimal("1.5"))
        self.assertEqual(diags[0].converted, 1)

    def test_round_half_up_conversion(self):
        schema = Schema([self.b])
        batch = Batch(schema, {"b": [Decimal("2.5"), Decimal("-2.5")]})
        target = Schema([replace(self.b, type=TypeTag.INT)])
        mig = plan_migration(
            schema, target, policies={self.b.column_id: RoundingPolicy.ROUND_HALF_UP}
        )
        out, diags = mig.project(batch)
        self.assertEqual(out.column_values("b"), (3, -3))
        self.assertEqual(len(diags), 2)

    def test_reject_on_loss_raises_with_location(self):
        target = Schema([self.a, replace(self.b, type=TypeTag.INT), self.c])
        mig = plan_migration(
            self.schema, target,
            policies={self.b.column_id: RoundingPolicy.REJECT_ON_LOSS},
        )
        with self.assertRaises(LossyConversionError) as ctx:
            mig.project(self.batch)
        self.assertEqual(ctx.exception.row, 0)
        self.assertEqual(ctx.exception.column, "b")
        self.assertEqual(ctx.exception.value, Decimal("1.5"))

    def test_integral_decimals_convert_without_diagnostics(self):
        schema = Schema([self.b])
        batch = Batch(schema, {"b": [Decimal("2.0"), Decimal("-7")]})
        target = Schema([replace(self.b, type=TypeTag.INT)])
        mig = plan_migration(
            schema, target,
            policies={self.b.column_id: RoundingPolicy.REJECT_ON_LOSS},
        )
        out, diags = mig.project(batch)
        self.assertEqual(out.column_values("b"), (2, -7))
        self.assertEqual(diags, [])

    def test_drop_column(self):
        target = Schema([self.a, self.b])
        out, _ = plan_migration(self.schema, target).project(self.batch)
        self.assertEqual(out.schema.names(), ("a", "b"))

    def test_ambiguous_rename_mapping_rejected(self):
        with self.assertRaises(AmbiguousMappingError):
            plan_renames(self.schema, {"a": "z", "c": "z"})

    def test_rename_into_existing_name_rejected(self):
        with self.assertRaises(AmbiguousMappingError):
            plan_renames(self.schema, {"a": "c"})

    def test_unsupported_type_change_rejected(self):
        target = Schema([self.a, self.b, replace(self.c, type=TypeTag.INT)])
        with self.assertRaises(MigrationError):
            plan_migration(self.schema, target)

    def test_nullability_tightening_rejected(self):
        target = Schema([self.a, replace(self.b, nullable=False), self.c])
        with self.assertRaises(MigrationError):
            plan_migration(self.schema, target)

    def test_nullability_relaxation_allowed(self):
        target = Schema([replace(self.a, nullable=True), self.b, self.c])
        out, _ = plan_migration(self.schema, target).project(self.batch)
        self.assertTrue(out.schema.get(self.a.column_id).nullable)

    def test_duplicate_target_names_rejected(self):
        with self.assertRaises(SchemaError):
            Schema([self.a, replace(self.b, name="a")])


if __name__ == "__main__":
    unittest.main()
