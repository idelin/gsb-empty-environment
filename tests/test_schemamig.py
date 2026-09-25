import unittest
from decimal import Decimal

from schemamig import (
    AddColumn, AmbiguousMappingError, Batch, BatchError, Column,
    ConversionError, ConvertColumn, DropColumn, LossyImplicitError,
    MergeConflict, Migration, MigrationError, Policy, RenameColumn,
    Repository, RepositoryError, Schema, Type, WidenColumn,
    find_counterexample, three_way_merge, value_eq,
)

NAN = Decimal("NaN")


def base_schema():
    return Schema([
        Column("c_id", "id", Type.INT, nullable=False),
        Column("c_name", "name", Type.STRING),
        Column("c_bal", "balance", Type.INT),
    ])


def base_batch(schema):
    return Batch(schema, {
        "c_id": (1, 2, 3),
        "c_name": ("ada", "bob", None),
        "c_bal": (100, -5, 2 ** 70 + 1),
    })


class TestVersionModes(unittest.TestCase):
    def test_add_nullable_column_projects_nulls(self):
        schema = base_schema()
        mig = Migration([AddColumn("c_mail", "email", Type.STRING)])
        plan = mig.apply(schema)
        self.assertEqual(plan.schema.names(), ["id", "name", "balance", "email"])
        out, diags = mig.project(schema, base_batch(schema))
        self.assertEqual(diags, [])
        self.assertEqual(out.column("c_mail"), (None, None, None))

    def test_add_non_nullable_column_rejected(self):
        mig = Migration([AddColumn("c_x", "x", Type.INT, nullable=False)])
        with self.assertRaises(MigrationError):
            mig.apply(base_schema())

    def test_widen_int_to_decimal_is_exact(self):
        schema = base_schema()
        mig = Migration([WidenColumn("c_bal", Type.DECIMAL)])
        out, _ = mig.project(schema, base_batch(schema))
        self.assertEqual(out.column("c_bal"),
                         (Decimal(100), Decimal(-5), Decimal(2 ** 70 + 1)))
        self.assertEqual(out.column("c_bal")[2], Decimal(2 ** 70 + 1))

    def test_integer_arithmetic_stays_exact(self):
        schema = base_schema()
        big = 2 ** 100 + 12345
        batch = Batch(schema, {"c_id": (1,), "c_name": (None,), "c_bal": (big,)})
        out, _ = Migration([WidenColumn("c_bal", Type.DECIMAL)]).project(schema, batch)
        self.assertEqual(out.column("c_bal")[0], Decimal(big))
        self.assertEqual(int(out.column("c_bal")[0]), big)

    def test_rename_keeps_identity(self):
        schema = base_schema()
        mig = Migration([RenameColumn("c_name", "full_name")])
        plan = mig.apply(schema)
        renamed = plan.schema.column("c_name")
        self.assertEqual(renamed.name, "full_name")
        self.assertEqual(renamed.type, Type.STRING)
        out, _ = mig.project(schema, base_batch(schema))
        self.assertEqual(out.column("c_name"), ("ada", "bob", None))

    def test_rename_chain_keeps_identity(self):
        schema = base_schema()
        mig = Migration([
            RenameColumn("c_name", "tmp"),
            RenameColumn("c_name", "final_name"),
        ])
        plan = mig.apply(schema)
        self.assertEqual(plan.schema.column("c_name").name, "final_name")
        self.assertEqual(plan.schema.names(), ["id", "final_name", "balance"])

    def test_drop_column(self):
        schema = base_schema()
        mig = Migration([DropColumn("c_bal")])
        out, _ = mig.project(schema, base_batch(schema))
        self.assertEqual(out.schema.names(), ["id", "name"])
        self.assertFalse(out.schema.has("c_bal"))


class TestRejection(unittest.TestCase):
    def test_two_sources_same_target_name_rejected(self):
        mig = Migration([
            RenameColumn("c_id", "dup"),
            RenameColumn("c_name", "dup"),
        ])
        with self.assertRaises(AmbiguousMappingError):
            mig.apply(base_schema())

    def test_rename_onto_existing_name_rejected(self):
        mig = Migration([RenameColumn("c_name", "balance")])
        with self.assertRaises(AmbiguousMappingError):
            mig.apply(base_schema())

    def test_add_column_with_existing_name_rejected(self):
        mig = Migration([AddColumn("c_new", "name", Type.STRING)])
        with self.assertRaises(AmbiguousMappingError):
            mig.apply(base_schema())

    def test_lossy_implicit_widening_rejected(self):
        mig = Migration([WidenColumn("c_bal", Type.STRING)])
        with self.assertRaises(LossyImplicitError):
            mig.apply(base_schema())

    def test_decimal_to_int_requires_explicit_policy(self):
        schema = Schema([Column("d", "amount", Type.DECIMAL)])
        with self.assertRaises(LossyImplicitError):
            Migration([WidenColumn("d", Type.INT)]).apply(schema)
        with self.assertRaises(LossyImplicitError):
            Migration([ConvertColumn("d", Type.INT)]).apply(schema)

    def test_unsupported_conversion_rejected(self):
        schema = Schema([Column("b", "flag", Type.BOOL)])
        with self.assertRaises(MigrationError):
            Migration([ConvertColumn("b", Type.INT)]).apply(schema)

    def test_unconvertible_value_raises(self):
        schema = Schema([Column("s", "s", Type.STRING)])
        batch = Batch(schema, {"s": ("abc",)})
        mig = Migration([ConvertColumn("s", Type.INT)])
        with self.assertRaises(ConversionError):
            mig.project(schema, batch)


class TestExplicitLossyConversion(unittest.TestCase):
    def test_decimal_to_int_keeps_per_value_diagnostics(self):
        schema = Schema([Column("d", "amount", Type.DECIMAL)])
        batch = Batch(schema, {"d": (Decimal("1.5"), Decimal("-2.25"),
                                     Decimal("7"), None)})
        mig = Migration([ConvertColumn("d", Type.INT, Policy.TRUNCATE)])
        out, diags = mig.project(schema, batch)
        self.assertEqual(out.column("d"), (1, -2, 7, None))
        self.assertEqual(len(diags), 2)
        self.assertEqual(diags[0].row, 0)
        self.assertEqual(diags[0].old_value, Decimal("1.5"))
        self.assertEqual(diags[0].new_value, 1)
        self.assertEqual(diags[1].row, 1)
        self.assertEqual(diags[1].old_value, Decimal("-2.25"))
        self.assertEqual(diags[1].new_value, -2)

    def test_policies_differ_on_negative_fractions(self):
        schema = Schema([Column("d", "amount", Type.DECIMAL)])
        batch = Batch(schema, {"d": (Decimal("-2.25"),)})
        trunc, _ = Migration(
            [ConvertColumn("d", Type.INT, Policy.TRUNCATE)]).project(schema, batch)
        floor, _ = Migration(
            [ConvertColumn("d", Type.INT, Policy.FLOOR)]).project(schema, batch)
        self.assertEqual(trunc.column("d"), (-2,))
        self.assertEqual(floor.column("d"), (-3,))

    def test_nan_cannot_become_int(self):
        schema = Schema([Column("d", "amount", Type.DECIMAL)])
        batch = Batch(schema, {"d": (NAN,)})
        mig = Migration([ConvertColumn("d", Type.INT, Policy.TRUNCATE)])
        with self.assertRaises(ConversionError):
            mig.project(schema, batch)


class TestNullAndNaN(unittest.TestCase):
    def test_null_and_nan_are_distinct(self):
        self.assertFalse(value_eq(None, NAN))
        self.assertFalse(value_eq(NAN, None))
        self.assertTrue(value_eq(NAN, Decimal("NaN")))
        self.assertTrue(value_eq(None, None))

    def test_batch_keeps_null_and_nan_distinct_through_projection(self):
        schema = Schema([Column("d", "amount", Type.DECIMAL)])
        batch = Batch(schema, {"d": (None, NAN, Decimal("1.5"))})
        out, _ = Migration([RenameColumn("d", "amt")]).project(schema, batch)
        values = out.column("d")
        self.assertIsNone(values[0])
        self.assertTrue(values[1].is_nan())
        self.assertEqual(values[2], Decimal("1.5"))

    def test_null_rejected_in_non_nullable_column(self):
        schema = Schema([Column("i", "id", Type.INT, nullable=False)])
        with self.assertRaises(BatchError):
            Batch(schema, {"i": (None,)})


class TestThreeWayMerge(unittest.TestCase):
    def test_rename_chains_must_converge_ok(self):
        base = base_schema()
        mig_a = Migration([RenameColumn("c_name", "full_name")])
        mig_b = Migration([
            RenameColumn("c_name", "tmp"),
            RenameColumn("c_name", "full_name"),
        ])
        result = three_way_merge(base, mig_a, mig_b)
        self.assertEqual(result.schema.column("c_name").name, "full_name")

    def test_rename_chains_diverging_conflict(self):
        base = base_schema()
        mig_a = Migration([RenameColumn("c_name", "first_name")])
        mig_b = Migration([RenameColumn("c_name", "last_name")])
        with self.assertRaises(MergeConflict):
            three_way_merge(base, mig_a, mig_b)

    def test_new_column_must_not_swallow_old_column(self):
        base = base_schema()
        mig_a = Migration([RenameColumn("c_bal", "total")])
        mig_b = Migration([AddColumn("c_new", "total", Type.DECIMAL)])
        with self.assertRaises(MergeConflict):
            three_way_merge(base, mig_a, mig_b)

    def test_two_new_columns_same_name_conflict(self):
        base = base_schema()
        mig_a = Migration([AddColumn("n1", "email", Type.STRING)])
        mig_b = Migration([AddColumn("n2", "email", Type.STRING)])
        with self.assertRaises(MergeConflict):
            three_way_merge(base, mig_a, mig_b)

    def test_rename_frees_name_for_new_column(self):
        base = base_schema()
        mig_a = Migration([RenameColumn("c_bal", "total")])
        mig_b = Migration([RenameColumn("c_bal", "total"),
                           AddColumn("n1", "balance", Type.DECIMAL)])
        result = three_way_merge(base, mig_a, mig_b)
        self.assertEqual(result.schema.column("c_bal").name, "total")
        self.assertEqual(result.schema.column("n1").name, "balance")
        self.assertEqual(len(result.schema), 4)

    def test_drop_vs_modify_conflicts(self):
        base = base_schema()
        mig_a = Migration([DropColumn("c_bal")])
        mig_b = Migration([RenameColumn("c_bal", "total")])
        with self.assertRaises(MergeConflict):
            three_way_merge(base, mig_a, mig_b)

    def test_drop_vs_untouched_drops(self):
        base = base_schema()
        result = three_way_merge(base, Migration([DropColumn("c_bal")]), Migration([]))
        self.assertFalse(result.schema.has("c_bal"))

    def test_widening_merges_with_untouched(self):
        base = base_schema()
        mig_a = Migration([WidenColumn("c_bal", Type.DECIMAL)])
        result = three_way_merge(base, mig_a, Migration([]))
        self.assertEqual(result.schema.column("c_bal").type, Type.DECIMAL)


class TestPathEquivalence(unittest.TestCase):
    def test_equivalent_paths_have_no_counterexample(self):
        schema = base_schema()
        left = Migration([RenameColumn("c_bal", "total"),
                          WidenColumn("c_bal", Type.DECIMAL)])
        right = Migration([WidenColumn("c_bal", Type.DECIMAL),
                           RenameColumn("c_bal", "total")])
        self.assertIsNone(find_counterexample(schema, left, right))

    def test_merged_paths_are_value_equivalent(self):
        base = base_schema()
        mig_a = Migration([RenameColumn("c_name", "full_name")])
        mig_b = Migration([WidenColumn("c_bal", Type.DECIMAL)])
        result = three_way_merge(base, mig_a, mig_b)
        path_a = mig_a + result.from_a
        path_b = mig_b + result.from_b
        self.assertIsNone(find_counterexample(base, path_a, path_b))

    def test_counterexample_found_for_different_policies(self):
        schema = Schema([Column("d", "amount", Type.DECIMAL)])
        left = Migration([ConvertColumn("d", Type.INT, Policy.TRUNCATE)])
        right = Migration([ConvertColumn("d", Type.INT, Policy.FLOOR)])
        cex = find_counterexample(schema, left, right)
        self.assertIsNotNone(cex)
        self.assertEqual(cex.reason, "value mismatch")
        self.assertNotEqual(cex.left.column("d"), cex.right.column("d"))
        self.assertIn(Decimal("-2.25"), cex.batch.column("d"))

    def test_counterexample_when_target_schemas_differ(self):
        schema = base_schema()
        left = Migration([RenameColumn("c_bal", "total")])
        right = Migration([RenameColumn("c_bal", "sum")])
        cex = find_counterexample(schema, left, right)
        self.assertIsNotNone(cex)
        self.assertEqual(cex.reason, "target schemas differ")

    def test_counterexample_when_one_path_fails(self):
        schema = Schema([Column("s", "s", Type.STRING)])
        left = Migration([ConvertColumn("s", Type.INT)])
        right = Migration([ConvertColumn("s", Type.DECIMAL),
                           ConvertColumn("s", Type.INT, Policy.TRUNCATE)])
        cex = find_counterexample(schema, left, right)
        self.assertIsNotNone(cex)
        self.assertIsInstance(cex.left, Exception)
        self.assertIsInstance(cex.right, Batch)


class TestAtomicPublish(unittest.TestCase):
    def test_publish_projects_batches_and_schema_atomically(self):
        schema = base_schema()
        repo = Repository(schema)
        repo.ingest(base_batch(schema))
        mig = Migration([
            RenameColumn("c_name", "full_name"),
            WidenColumn("c_bal", Type.DECIMAL),
            AddColumn("c_mail", "email", Type.STRING),
        ])
        new_batch = Batch(schema, {
            "c_id": (4,), "c_name": ("cid",), "c_bal": (9,),
        })
        diags = repo.publish(mig, batch=new_batch)
        self.assertEqual(diags, [])
        self.assertEqual(repo.schema.names(),
                         ["id", "full_name", "balance", "email"])
        self.assertEqual(len(repo.batches), 2)
        self.assertEqual(repo.batches[0].column("c_bal")[2],
                         Decimal(2 ** 70 + 1))
        self.assertEqual(repo.batches[1].column("c_mail"), (None,))

    def test_failed_publish_leaves_no_partial_columns(self):
        schema = base_schema()
        repo = Repository(schema)
        repo.ingest(base_batch(schema))
        bad = Migration([
            AddColumn("c_mail", "email", Type.STRING),
            RenameColumn("c_name", "balance"),  # ambiguous -> must fail
        ])
        with self.assertRaises(AmbiguousMappingError):
            repo.publish(bad)
        self.assertEqual(repo.schema, schema)
        self.assertFalse(repo.schema.has("c_mail"))
        self.assertEqual(len(repo.batches), 1)
        self.assertEqual(repo.batches[0].schema, schema)

    def test_failed_projection_leaves_state_untouched(self):
        schema = Schema([Column("s", "s", Type.STRING)])
        repo = Repository(schema)
        repo.ingest(Batch(schema, {"s": ("not-an-int",)}))
        mig = Migration([
            AddColumn("extra", "extra", Type.INT),
            ConvertColumn("s", Type.INT),
        ])
        with self.assertRaises(ConversionError):
            repo.publish(mig)
        self.assertEqual(repo.schema, schema)
        self.assertFalse(repo.schema.has("extra"))

    def test_ingest_rejects_mismatched_batch(self):
        repo = Repository(base_schema())
        other = Batch(Schema([Column("x", "x", Type.INT)]), {"x": (1,)})
        with self.assertRaises(RepositoryError):
            repo.ingest(other)


if __name__ == "__main__":
    unittest.main()
