import pathlib
import unittest

from plan_dag import (
    AggregateNode,
    Catalog,
    FilterNode,
    HashJoinNode,
    INT,
    NamedExpr,
    ProjectNode,
    Relation,
    ScanNode,
    Schema,
    Table,
    TEXT,
    TypeMismatchError,
    UnionAllNode,
    binop,
    build_plan,
    col,
    execute,
    lit,
)
from plan_dag.expr import IsNullExpr
from plan_dag.types import NULL


class OperatorTests(unittest.TestCase):
    def setUp(self):
        left = Table.create(
            Schema.of(("k", INT), ("v", TEXT)),
            [(1, "a"), (2, "b"), (2, "b2"), (3, None)],
        )
        right = Table.create(
            Schema.of(("k", INT), ("w", INT)),
            [(2, 20), (2, 21), (4, 40)],
        )
        self.catalog = Catalog({"l": left, "r": right})

    def test_inner_hash_join_multiset_and_key_dedup_output(self):
        plan = build_plan(
            "j",
            [ScanNode(id="sl", table="l"), ScanNode(id="sr", table="r"),
             HashJoinNode(id="j", inputs=["sl", "sr"], left_keys=("k",), right_keys=("k",))],
            self.catalog,
        )
        relation, _ = execute(plan, self.catalog)
        self.assertEqual(relation.schema.names, ("k", "v", "w"))
        self.assertEqual(
            sorted(relation.rows),
            [(2, "b", 20), (2, "b", 21), (2, "b2", 20), (2, "b2", 21)],
        )

    def test_union_all_preserves_duplicates_and_requires_same_schema(self):
        plan = build_plan(
            "u",
            [ScanNode(id="sl", table="l"), ScanNode(id="sr2", table="l"),
             UnionAllNode(id="u", inputs=["sl", "sr2"])],
            self.catalog,
        )
        relation, _ = execute(plan, self.catalog)
        self.assertEqual(len(relation.rows), 8)

        good = Table.create(Schema.of(("k", INT), ("v", TEXT)), [(1, "a")])
        bad = Table.create(Schema.of(("k", TEXT), ("v", TEXT)), [("x", "y")])
        catalog = Catalog({"l": good, "b": bad})
        with self.assertRaises(TypeMismatchError):
            build_plan(
                "u",
                [ScanNode(id="a", table="l"), ScanNode(id="b", table="b"),
                 UnionAllNode(id="u", inputs=["a", "b"])],
                catalog,
            )

    def test_project_duplicate_output_name_rejected(self):
        scan = ScanNode(id="s", table="l")
        project = ProjectNode(
            id="p",
            inputs=["s"],
            items=[NamedExpr("x", col("k")), NamedExpr("x", col("v"))],
        )
        with self.assertRaises(TypeMismatchError):
            build_plan("p", [scan, project], self.catalog)

    def test_filter_predicate_must_be_boolean(self):
        scan = ScanNode(id="s", table="l")
        bad_filter = FilterNode(id="f", inputs=["s"], predicate=col("v"))
        with self.assertRaises(TypeMismatchError):
            build_plan("f", [scan, bad_filter], self.catalog)

    def test_isnull_filter_keeps_null_rows(self):
        scan = ScanNode(id="s", table="l")
        null_filter = FilterNode(id="f", inputs=["s"], predicate=IsNullExpr(col("v")))
        project = ProjectNode(
            id="p", inputs=["f"], items=[NamedExpr("k", col("k"))]
        )
        plan = build_plan("p", [scan, null_filter, project], self.catalog)
        relation, _ = execute(plan, self.catalog)
        self.assertEqual(relation.rows, [(3,)])

    def test_aggregate_avg_returns_float_and_ignores_null(self):
        values = Table.create(
            Schema.of(("g", TEXT), ("x", INT)),
            [("a", 10), ("a", 20), ("a", None), ("b", None)],
        )
        catalog = Catalog({"t": values})
        scan = ScanNode(id="s", table="t")
        aggregate = AggregateNode(
            id="g",
            inputs=["s"],
            group_keys=("g",),
            aggregates=({"func": "avg", "alias": "avg_x",
                         "expr": {"e": "col", "name": "x"}},),
        )
        plan = build_plan("g", [scan, aggregate], catalog)
        relation, _ = execute(plan, catalog)
        rows = dict(relation.rows)
        self.assertEqual(rows["a"], 15.0)
        self.assertIs(rows["b"], NULL)
        self.assertEqual(relation.schema.types, (TEXT, "FLOAT"))

    def test_sum_on_text_rejected_during_binding(self):
        scan = ScanNode(id="s", table="l")
        aggregate = AggregateNode(
            id="g",
            inputs=["s"],
            aggregates=({"func": "sum", "alias": "bad",
                         "expr": {"e": "col", "name": "v"}},),
        )
        with self.assertRaises(TypeMismatchError):
            build_plan("g", [scan, aggregate], self.catalog)

    def test_ambiguous_join_output_column_rejected(self):
        left = Table.create(Schema.of(("k", INT), ("x", INT)), [(1, 1)])
        right = Table.create(Schema.of(("k", INT), ("x", INT)), [(1, 2)])
        catalog = Catalog({"l": left, "r": right})
        scan_left = ScanNode(id="sl", table="l")
        scan_right = ScanNode(id="sr", table="r")
        # joining on different key names leaves both x columns in scope
        join = HashJoinNode(
            id="j", inputs=["sl", "sr"], left_keys=("k",), right_keys=("k",)
        )
        with self.assertRaises(Exception):
            build_plan("j", [scan_left, scan_right, join], catalog)


class SecurityAuditTests(unittest.TestCase):
    PACKAGE = pathlib.Path(__file__).resolve().parents[1] / "plan_dag"

    def test_no_dynamic_code_execution_primitives(self):
        import re

        offenders = []
        for path in self.PACKAGE.glob("*.py"):
            text = path.read_text()
            for match in re.finditer(r"(?<![.\w])(eval|exec)\(", text):
                line_start = text.rfind("\n", 0, match.start()) + 1
                line_end = text.find("\n", match.end())
                line = text[line_start : line_end if line_end != -1 else len(text)]
                stripped = line.lstrip()
                if not stripped.startswith("def ") and not stripped.startswith("abstractmethod"):
                    offenders.append((path.name, match.group(0)))
            for token in ("__import__", "pickle", "subprocess", "os.system",
                          "globals()", "locals()"):
                if token in text:
                    offenders.append((path.name, token))
        self.assertEqual(offenders, [])

    def test_no_sql_engine_imports(self):
        forbidden = ("sqlite3", "sqlalchemy", "psycopg", "duckdb")
        offenders = []
        for path in self.PACKAGE.glob("*.py"):
            text = path.read_text()
            for token in forbidden:
                if token in text:
                    offenders.append((path.name, token))
        self.assertEqual(offenders, [])

    def test_operators_come_only_from_static_registry(self):
        from plan_dag import NODE_REGISTRY

        self.assertEqual(
            set(NODE_REGISTRY),
            {"scan", "filter", "project", "hashjoin", "aggregate", "unionall"},
        )
        for cls in NODE_REGISTRY.values():
            self.assertTrue(hasattr(cls, "run"))


if __name__ == "__main__":
    unittest.main()
