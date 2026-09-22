import unittest

from plan_dag import (
    AmbiguousColumnError,
    BindingError,
    CycleError,
    MissingNodeError,
    UnknownTableError,
    ColumnRef,
    Cmp,
    Literal,
    Node,
    PlanDag,
    ProjectItem,
    ProjectSpec,
    ScanSpec,
    FilterSpec,
    JoinSpec,
    AggregateSpec,
    AggItem,
)
from plan_dag.datatypes import Field, INTEGER, Schema
from plan_dag.executor import Executor
from plan_dag.operators import ColumnResolver
from plan_dag.runtime import BoundNode, BoundOp
from plan_dag.sample_data import build_catalog


def bound_scan(node_id, schema):
    class _Scan(BoundOp):
        def __init__(self):
            self.schema = schema

        def execute(self, inputs, context):
            return []

    return BoundNode(node_id, (), _Scan())


def make_dag(nodes, roots=None):
    return PlanDag(nodes, roots)


class StructureTests(unittest.TestCase):
    def test_missing_input_node(self):
        with self.assertRaises(MissingNodeError):
            make_dag(
                [Node("f", FilterSpec(Cmp("eq", ColumnRef("id"), Literal(1))), ("ghost",))],
                roots=["f"],
            )

    def test_self_cycle(self):
        with self.assertRaises(CycleError):
            make_dag(
                [Node("a", ScanSpec("employees"), ("a",))],
                roots=["a"],
            )

    def test_two_node_cycle(self):
        with self.assertRaises(CycleError):
            make_dag(
                [
                    Node("a", ScanSpec("employees"), ("b",)),
                    Node("b", ScanSpec("departments"), ("a",)),
                ],
                roots=["a"],
            )

    def test_missing_root(self):
        with self.assertRaises(MissingNodeError):
            make_dag([Node("t", ScanSpec("employees"), ())], roots=["nope"])

    def test_diamond_is_valid_and_shared(self):
        from plan_dag import LimitSpec
        t = Node("t", ScanSpec("employees"), ())
        a = Node("a", LimitSpec(10), ("t",))
        b = Node("b", LimitSpec(10), ("t",))
        # structural sharing test only: use dept key after projecting down
        from plan_dag import ProjectSpec, ProjectItem
        pa = Node("pa", ProjectSpec([ProjectItem("id", ColumnRef("id")),
                                    ProjectItem("lname", ColumnRef("name"))]), ("a",))
        pb = Node("pb", ProjectSpec([ProjectItem("id", ColumnRef("id")),
                                    ProjectItem("rname", ColumnRef("name"))]), ("b",))
        c = Node("c", JoinSpec(["id"], ["id"]), ("pa", "pb"))
        nodes = [t, a, b, pa, pb, c]
        dag = make_dag(nodes, roots=["c"])
        bound = dag.bind(build_catalog())
        ex = Executor(build_catalog())
        ex.execute(bound, "c")
        self.assertEqual(ex.last_context.scan_count, 1)


class BindingTests(unittest.TestCase):
    def setUp(self):
        self.catalog = build_catalog()

    def test_unknown_table(self):
        dag = make_dag([Node("t", ScanSpec("nope"), ())], roots=["t"])
        with self.assertRaises(UnknownTableError):
            dag.bind(self.catalog)

    def test_unknown_column(self):
        dag = make_dag(
            [
                Node("t", ScanSpec("employees"), ()),
                Node("f", FilterSpec(Cmp("eq", ColumnRef("ghost"), Literal(1))), ("t",)),
            ],
            roots=["f"],
        )
        with self.assertRaises(BindingError):
            dag.bind(self.catalog)

    def test_type_mismatch_detected_before_execution(self):
        dag = make_dag(
            [
                Node("t", ScanSpec("employees"), ()),
                Node("f", FilterSpec(Cmp("gt", ColumnRef("name"), Literal(10))), ("t",)),
            ],
            roots=["f"],
        )
        with self.assertRaises(BindingError):
            dag.bind(self.catalog)

    def test_non_boolean_predicate(self):
        dag = make_dag(
            [
                Node("t", ScanSpec("employees"), ()),
                Node("f", FilterSpec(ColumnRef("name")), ("t",)),
            ],
            roots=["f"],
        )
        with self.assertRaises(BindingError):
            dag.bind(self.catalog)

    def test_ambiguous_column_resolver(self):
        left = bound_scan("l", Schema([Field("x", INTEGER), Field("y", INTEGER)]))
        right = bound_scan("r", Schema([Field("x", INTEGER)]))
        resolver = ColumnResolver([left, right], "n")
        with self.assertRaises(AmbiguousColumnError):
            resolver.resolve("x")
        index, t = resolver.resolve("y")  # type: ignore[unreachable]
        self.assertEqual((index, t), (1, INTEGER))

    def test_join_key_type_mismatch(self):
        from plan_dag.datatypes import TEXT
        left = Node("l", ScanSpec("employees"), ())  # dept INTEGER
        right_scan = Node("r", ScanSpec("departments"), ())  # dept INTEGER
        # create a text-typed right by projecting dept-as-text is heavy;
        # instead unit-test the resolver path directly.
        from plan_dag.datatypes import Field as F, Schema as S
        lb = bound_scan("l", S([F("k", INTEGER)]))
        rb = bound_scan("r", S([F("k", TEXT)]))
        spec = JoinSpec(["k"], ["k"])
        with self.assertRaises(BindingError):
            spec.bind([lb, rb], self.catalog, "j")

    def test_join_output_collision(self):
        from plan_dag.datatypes import TEXT
        from plan_dag.datatypes import Field as F, Schema as S
        lb = bound_scan("l", S([F("k", INTEGER), F("name", TEXT)]))
        rb = bound_scan("r", S([F("k", INTEGER), F("name", TEXT)]))
        with self.assertRaises(BindingError):
            JoinSpec(["k"], ["k"]).bind([lb, rb], self.catalog, "j")

    def test_sum_requires_numeric(self):
        lb = bound_scan("l", Schema([Field("name", __import__("plan_dag").TEXT)]))
        spec = AggregateSpec(
            [AggItem("sum", ColumnRef("name"), "s")], group_keys=[]
        )
        with self.assertRaises(BindingError):
            spec.bind([lb], self.catalog, "a")


if __name__ == "__main__":
    unittest.main()


class ExtraBindingTests(unittest.TestCase):
    def setUp(self):
        self.catalog = build_catalog()

    def test_project_arithmetic_on_text_rejected(self):
        from plan_dag import Arith
        dag = make_dag(
            [
                Node("t", ScanSpec("employees"), ()),
                Node("p", ProjectSpec([
                    ProjectItem("x", Arith("add", ColumnRef("name"), Literal(1))),
                ]), ("t",)),
            ],
            roots=["p"],
        )
        with self.assertRaises(BindingError):
            dag.bind(self.catalog)

    def test_missing_field_inside_nested_expression(self):
        from plan_dag import Arith
        dag = make_dag(
            [
                Node("t", ScanSpec("employees"), ()),
                Node("f", FilterSpec(
                    Cmp("gt",
                        Arith("add", ColumnRef("id"), ColumnRef("nope")),
                        Literal(0))), ("t",)),
            ],
            roots=["f"],
        )
        with self.assertRaises(BindingError):
            dag.bind(self.catalog)

    def test_count_star_output_is_integer(self):
        from plan_dag import INTEGER as INT
        dag = make_dag(
            [
                Node("t", ScanSpec("employees"), ()),
                Node("a", AggregateSpec([AggItem("count_star", None, "n")], []), ("t",)),
            ],
            roots=["a"],
        )
        bound = dag.bind(self.catalog)
        self.assertIs(bound.schema_of("a").fields[0].type, INT)
