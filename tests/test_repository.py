import unittest
from dataclasses import replace
from decimal import Decimal

from schemamigrate import (
    NULL,
    Batch,
    BatchError,
    LossyConversionError,
    MigrationError,
    Repository,
    RoundingPolicy,
    Schema,
    TypeTag,
    make_column,
    plan_migration,
)


class RepositoryTest(unittest.TestCase):
    def setUp(self):
        self.a = make_column("n", TypeTag.INT, nullable=False)
        self.d = make_column("r", TypeTag.DECIMAL)
        self.v1 = Schema([self.a, self.d], version=1)
        self.batch = Batch(
            self.v1, {"n": [1, 2], "r": [Decimal("1.5"), Decimal("2.0")]}
        )

    def test_atomic_publish_projects_batches(self):
        repo = Repository(self.v1)
        repo.append(self.batch)
        new_col = make_column("s", TypeTag.STRING)
        v2 = Schema([self.a, self.d, new_col], version=2)
        diags = repo.publish(plan_migration(self.v1, v2))
        self.assertEqual(repo.schema, v2)
        self.assertEqual(diags, [])
        self.assertEqual(len(repo.batches), 1)
        self.assertEqual(repo.batches[0].schema, v2)
        self.assertEqual(repo.batches[0].column_values("s"), (NULL, NULL))

    def test_failed_publish_leaves_no_partial_state(self):
        repo = Repository(self.v1)
        repo.append(self.batch)
        new_col = make_column("s", TypeTag.STRING)
        v2 = Schema([self.a, replace(self.d, type=TypeTag.INT), new_col], version=2)
        mig = plan_migration(
            self.v1, v2, policies={self.d.column_id: RoundingPolicy.REJECT_ON_LOSS}
        )
        with self.assertRaises(LossyConversionError):
            repo.publish(mig)
        self.assertEqual(repo.schema, self.v1)
        self.assertEqual(len(repo.batches), 1)
        self.assertEqual(repo.batches[0].schema, self.v1)
        self.assertEqual(
            repo.batches[0].column_values("r"), (Decimal("1.5"), Decimal("2.0"))
        )

    def test_failed_publish_with_bad_batch_value(self):
        repo = Repository(self.v1)
        repo.append(self.batch)
        new_col = make_column("s", TypeTag.STRING)
        v2 = Schema([self.a, self.d, new_col], version=2)
        bad_batch = Batch(self.v1, {"n": [3], "r": [Decimal("0.5")]})
        repo.append(bad_batch)
        # replace stored batches with one that violates the target schema
        # (NULL would be fine; a wrong-typed value in the source is not)
        with self.assertRaises(BatchError):
            Batch(self.v1, {"n": [3], "r": ["not-a-decimal"]})
        # publish itself stays consistent
        repo.publish(plan_migration(self.v1, v2))
        self.assertEqual(repo.schema, v2)
        self.assertEqual(repo.batches[1].column_values("s"), (NULL,))

    def test_source_schema_mismatch_rejected(self):
        repo = Repository(self.v1)
        other = Schema([self.a], version=9)
        mig = plan_migration(other, Schema([self.a, make_column("x", TypeTag.STRING)]))
        with self.assertRaises(MigrationError):
            repo.publish(mig)
        self.assertEqual(repo.schema, self.v1)

    def test_append_schema_mismatch_rejected(self):
        repo = Repository(self.v1)
        with self.assertRaises(BatchError):
            repo.append(Batch(Schema([self.a]), {"n": [1]}))


if __name__ == "__main__":
    unittest.main()
