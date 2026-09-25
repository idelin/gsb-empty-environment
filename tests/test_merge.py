import unittest
from dataclasses import replace

from schemamigrate import (
    MergeConflict,
    Schema,
    TypeTag,
    make_column,
    merge_schemas,
)


class MergeTest(unittest.TestCase):
    def setUp(self):
        self.a = make_column("a", TypeTag.INT, nullable=False, column_id="A")
        self.b = make_column("b", TypeTag.STRING, column_id="B")
        self.base = Schema([self.a, self.b], version=1)

    def test_converging_rename_chains_merge(self):
        left = Schema([replace(self.a, name="x"), self.b])
        right = Schema([replace(self.a, name="x"), self.b])
        merged = merge_schemas(self.base, left, right)
        self.assertEqual(merged.get("A").name, "x")

    def test_diverging_rename_chains_conflict(self):
        left = Schema([replace(self.a, name="x"), self.b])
        right = Schema([replace(self.a, name="y"), self.b])
        with self.assertRaises(MergeConflict):
            merge_schemas(self.base, left, right)

    def test_one_sided_rename_merges(self):
        left = Schema([replace(self.a, name="x"), self.b])
        merged = merge_schemas(self.base, left, self.base)
        self.assertEqual(merged.get("A").name, "x")

    def test_widening_on_one_branch_merges(self):
        left = Schema([replace(self.a, type=TypeTag.DECIMAL), self.b])
        merged = merge_schemas(self.base, left, self.base)
        self.assertEqual(merged.get("A").type, TypeTag.DECIMAL)

    def test_divergent_type_changes_conflict(self):
        left = Schema([replace(self.a, type=TypeTag.DECIMAL), self.b])
        right = Schema([replace(self.a, type=TypeTag.STRING), self.b])
        with self.assertRaises(MergeConflict):
            merge_schemas(self.base, left, right)

    def test_drop_vs_modify_conflicts(self):
        left = Schema([self.a])
        right = Schema([self.a, replace(self.b, name="b2")])
        with self.assertRaises(MergeConflict):
            merge_schemas(self.base, left, right)

    def test_drop_on_one_branch_unmodified_on_other_drops(self):
        left = Schema([self.a])
        merged = merge_schemas(self.base, left, self.base)
        self.assertIsNone(merged.get("B"))

    def test_drop_on_both_branches_drops(self):
        left = Schema([self.a])
        right = Schema([self.a])
        merged = merge_schemas(self.base, left, right)
        self.assertIsNone(merged.get("B"))

    def test_same_name_new_column_does_not_swallow_old_identity(self):
        # left renames old b -> b2 and adds a brand-new column named "b";
        # the old identity B must survive as b2, the new column stays B-NEW.
        new_b = make_column("b", TypeTag.INT, column_id="B-NEW")
        left = Schema([self.a, replace(self.b, name="b2"), new_b])
        merged = merge_schemas(self.base, left, self.base)
        self.assertEqual(merged.get("B").name, "b2")
        self.assertEqual(merged.get("B").type, TypeTag.STRING)
        self.assertEqual(merged.get("B-NEW").name, "b")
        self.assertEqual(merged.get("B-NEW").type, TypeTag.INT)

    def test_branches_add_same_name_different_identity_conflict(self):
        left = Schema([self.a, self.b, make_column("c", TypeTag.BOOL, column_id="C1")])
        right = Schema([self.a, self.b, make_column("c", TypeTag.BOOL, column_id="C2")])
        with self.assertRaises(MergeConflict):
            merge_schemas(self.base, left, right)

    def test_both_branches_add_identical_column_merges(self):
        new_col = make_column("c", TypeTag.BOOL, column_id="C")
        left = Schema([self.a, self.b, new_col])
        right = Schema([self.a, self.b, new_col])
        merged = merge_schemas(self.base, left, right)
        self.assertEqual(len(merged.columns), 3)
        self.assertEqual(merged.get("C").name, "c")

    def test_same_identity_added_with_different_def_conflict(self):
        left = Schema([self.a, self.b, make_column("c", TypeTag.BOOL, column_id="C")])
        right = Schema([self.a, self.b, make_column("c", TypeTag.INT, column_id="C")])
        with self.assertRaises(MergeConflict):
            merge_schemas(self.base, left, right)


if __name__ == "__main__":
    unittest.main()
