import unittest

from plan_dag import (
    BOOL,
    FLOAT,
    INT,
    NULL,
    TEXT,
    Catalog,
    Column,
    Relation,
    Schema,
    Table,
    TypeMismatchError,
)


class NullAndSchemaTests(unittest.TestCase):
    def test_null_is_singleton_and_normalizes_none(self):
        from plan_dag.types import _Null, normalize_value

        self.assertIs(_Null(), _Null())
        self.assertIs(normalize_value(None), NULL)
        self.assertEqual(NULL, NULL)
        with self.assertRaises(TypeError):
            bool(NULL)

    def test_table_validates_scalar_types(self):
        schema = Schema((Column("a", INT), Column("b", TEXT), Column("c", BOOL)))
        table = Table(schema)
        table.add_row((1, "x", True))
        table.add_row((NULL, NULL, NULL))
        with self.assertRaises(TypeMismatchError):
            table.add_row(("1", "x", True))
        with self.assertRaises(TypeMismatchError):
            # bool must not be accepted as INT
            table.add_row((True, "x", True))
        with self.assertRaises(TypeMismatchError):
            table.add_row((1, "x", "yes"))

    def test_relation_is_a_multiset(self):
        schema = Schema((Column("a", INT),))
        relation = Relation(schema)
        for _ in range(3):
            relation.add_row((7,))
        self.assertEqual(len(relation.rows), 3)
        self.assertEqual(relation.rows, [(7,), (7,), (7,)])

    def test_relation_row_arity_checked(self):
        schema = Schema((Column("a", INT), Column("b", INT)))
        with self.assertRaises(TypeMismatchError):
            Table.create(schema, [(1,)])

    def test_unknown_type_rejected(self):
        with self.assertRaises(TypeMismatchError):
            Column("a", "JSON")

    def test_schema_lookup(self):
        schema = Schema((Column("a", INT), Column("b", TEXT)))
        self.assertEqual(schema.index_of("b"), 1)
        self.assertEqual(schema.index_of("missing"), -1)
        duplicated = Schema((Column("a", INT), Column("a", INT)))
        self.assertEqual(duplicated.index_of("a"), -2)

    def test_catalog_unknown_table(self):
        catalog = Catalog()
        with self.assertRaises(Exception):
            catalog.get("nope")

    def test_type_constants(self):
        self.assertEqual((INT, FLOAT, TEXT, BOOL), ("INT", "FLOAT", "TEXT", "BOOL"))


if __name__ == "__main__":
    unittest.main()
