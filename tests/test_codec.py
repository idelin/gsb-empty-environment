import copy
import json
import unittest

from plan_dag import (
    AggregateNode,
    BOOL,
    Catalog,
    CycleError,
    DocumentFormatError,
    FilterNode,
    FLOAT,
    HashJoinNode,
    INT,
    MissingNodeError,
    NamedExpr,
    NodeConflictError,
    ProjectNode,
    Relation,
    ScanNode,
    Schema,
    Table,
    TEXT,
    TypeMismatchError,
    UnknownOperatorError,
    UnknownTableError,
    UnsupportedVersionError,
    binop,
    build_plan,
    col,
    execute,
    from_document,
    from_json,
    lit,
)


def base_catalog():
    customers = Table.create(
        Schema.of(("cid", INT), ("name", TEXT)),
        [(1, "Ann"), (2, "Bo"), (3, "Cy")],
    )
    orders = Table.create(
        Schema.of(
            ("oid", INT), ("cid", INT), ("region", TEXT), ("amount", INT)
        ),
        [
            (10, 1, "east", 100),
            (11, 1, "east", None),
            (12, 2, "west", 50),
            (13, 2, "west", 50),
        ],
    )
    return Catalog({"customers": customers, "orders": orders})


def make_plan(catalog):
    scan_customers = ScanNode(id="sc", table="customers")
    scan_orders = ScanNode(id="so", table="orders")
    predicate = binop(">=", col("amount"), lit(50))
    filtered = FilterNode(id="f", inputs=["so"], predicate=predicate)
    aggregate = AggregateNode(
        id="agg",
        inputs=["f"],
        group_keys=("cid",),
        aggregates=(
            {"func": "count", "alias": "order_count", "expr": {"e": "star"}},
            {
                "func": "sum",
                "alias": "total_amount",
                "expr": {"e": "col", "name": "amount"},
            },
        ),
    )
    join = HashJoinNode(
        id="join",
        inputs=["sc", "agg"],
        left_keys=("cid",),
        right_keys=("cid",),
    )
    project = ProjectNode(
        id="root",
        inputs=["join"],
        items=[
            NamedExpr("name", col("name")),
            NamedExpr("orders", col("order_count")),
            NamedExpr("total", col("total_amount")),
        ],
    )
    return build_plan(
        "root",
        [scan_customers, scan_orders, filtered, aggregate, join, project],
        catalog,
    )


class RoundTripTests(unittest.TestCase):
    def setUp(self):
        self.catalog = base_catalog()

    def test_document_is_typed_and_versioned(self):
        plan = make_plan(self.catalog)
        document = plan.to_document()
        self.assertEqual(document["doc_type"], "plan-dag")
        self.assertEqual(document["version"], 1)
        self.assertEqual(document["root"], "root")
        for node in document["nodes"]:
            self.assertEqual(node["version"], 1)
            self.assertIn(node["op"], {"scan", "filter", "project", "hashjoin", "aggregate"})

    def test_json_roundtrip_execution(self):
        plan = make_plan(self.catalog)
        restored = from_json(plan.to_json(), self.catalog)
        relation, _ = execute(restored, self.catalog)
        self.assertEqual(relation.rows, [("Ann", 1, 100), ("Bo", 2, 100)])

    def test_output_schema_types_are_checked_and_preserved(self):
        plan = make_plan(self.catalog)
        root_schema = plan.nodes["root"].bound.output_schema
        self.assertEqual(root_schema.types, (TEXT, INT, INT))
        self.assertEqual(root_schema.names, ("name", "orders", "total"))

    def test_duplicate_rows_are_preserved(self):
        # Bo has two identical order rows: count(*) reflects the multiset.
        plan = make_plan(self.catalog)
        relation, _ = execute(plan, self.catalog)
        bo_row = [row for row in relation.rows if row[0] == "Bo"][0]
        self.assertEqual(bo_row, ("Bo", 2, 100))

    def test_null_row_is_excluded_by_three_valued_filter(self):
        plan = make_plan(self.catalog)
        relation, _ = execute(plan, self.catalog)
        ann_row = [row for row in relation.rows if row[0] == "Ann"][0]
        # NULL >= 50 is NULL, not true, so only the 100 order survives.
        self.assertEqual(ann_row, ("Ann", 1, 100))


class DeserializationValidationTests(unittest.TestCase):
    def setUp(self):
        self.catalog = base_catalog()
        self.document = make_plan(self.catalog).to_document()

    def node(self, node_id):
        return next(node for node in self.document["nodes"] if node["id"] == node_id)

    def test_unknown_operator_rejected(self):
        self.node("f")["op"] = "os.system"
        with self.assertRaises(UnknownOperatorError):
            from_document(self.document, self.catalog)

    def test_unknown_operator_payload_is_never_executed(self):
        malicious = {
            "doc_type": "plan-dag",
            "version": 1,
            "root": "evil",
            "nodes": [
                {
                    "id": "evil",
                    "op": "evil.eval",
                    "version": 1,
                    "inputs": [],
                    "code": "import os\nos.system('echo PWNED')",
                }
            ],
        }
        with self.assertRaises(UnknownOperatorError):
            from_document(malicious, self.catalog)

    def test_unsupported_document_version(self):
        doc = copy.deepcopy(self.document)
        doc["version"] = 99
        with self.assertRaises(UnsupportedVersionError):
            from_document(doc, self.catalog)

    def test_unsupported_doc_type(self):
        doc = copy.deepcopy(self.document)
        doc["doc_type"] = "sql-script"
        with self.assertRaises(UnsupportedVersionError):
            from_document(doc, self.catalog)

    def test_unsupported_node_version(self):
        doc = copy.deepcopy(self.document)
        self._node(doc, "f")["version"] = 7
        with self.assertRaises(UnsupportedVersionError):
            from_document(doc, self.catalog)

    def test_missing_input_node(self):
        doc = copy.deepcopy(self.document)
        self._node(doc, "f")["inputs"] = ["ghost"]
        with self.assertRaises(MissingNodeError):
            from_document(doc, self.catalog)

    def test_missing_root(self):
        doc = copy.deepcopy(self.document)
        doc["root"] = "ghost"
        with self.assertRaises(MissingNodeError):
            from_document(doc, self.catalog)

    def test_cycle_is_detected(self):
        doc = copy.deepcopy(self.document)
        self._node(doc, "so")["inputs"] = ["f"]
        with self.assertRaises(CycleError):
            from_document(doc, self.catalog)

    def test_self_cycle_is_detected(self):
        doc = copy.deepcopy(self.document)
        self._node(doc, "so")["inputs"] = ["so"]
        with self.assertRaises(CycleError):
            from_document(doc, self.catalog)

    def test_unknown_field_binding(self):
        doc = copy.deepcopy(self.document)
        self._node(doc, "f")["predicate"] = {
            "e": "bin",
            "op": "=",
            "left": {"e": "col", "name": "not_a_column"},
            "right": {"e": "lit", "value": 1, "type": INT},
        }
        from plan_dag.errors import BindingError

        with self.assertRaises(BindingError):
            from_document(doc, self.catalog)

    def test_misaligned_join_key_types_rejected_before_execution(self):
        doc = copy.deepcopy(self.document)
        join = self._node(doc, "join")
        join["left_keys"] = ["name"]   # TEXT
        join["right_keys"] = ["cid"]   # INT
        with self.assertRaises(TypeMismatchError):
            from_document(doc, self.catalog)

    def test_compare_text_to_int_rejected_before_execution(self):
        doc = copy.deepcopy(self.document)
        self._node(doc, "f")["predicate"] = {
            "e": "bin",
            "op": "=",
            "left": {"e": "col", "name": "region"},
            "right": {"e": "lit", "value": 1, "type": INT},
        }
        with self.assertRaises(TypeMismatchError):
            from_document(doc, self.catalog)

    def test_same_id_different_content_conflict(self):
        doc = copy.deepcopy(self.document)
        scan = self._node(doc, "so")
        duplicate = copy.deepcopy(scan)
        duplicate["table"] = "customers"
        doc["nodes"].append(duplicate)
        with self.assertRaises(NodeConflictError):
            from_document(doc, self.catalog)

    def test_same_id_identical_content_is_deduplicated(self):
        doc = copy.deepcopy(self.document)
        scan = self._node(doc, "so")
        doc["nodes"].append(copy.deepcopy(scan))
        plan = from_document(doc, self.catalog)
        self.assertEqual(len(plan.nodes), len(self.document["nodes"]))

    def test_unknown_table_rejected_at_bind_time(self):
        scan = ScanNode(id="s", table="missing")
        with self.assertRaises(UnknownTableError):
            build_plan("s", [scan], self.catalog)

    def test_unknown_json_field_rejected(self):
        doc = copy.deepcopy(self.document)
        self._node(doc, "so")["surprise"] = 1
        with self.assertRaises(DocumentFormatError):
            from_document(doc, self.catalog)

    def test_invalid_json_rejected(self):
        with self.assertRaises(DocumentFormatError):
            from_json("{not json", self.catalog)

    def test_bad_inputs_type_rejected(self):
        doc = copy.deepcopy(self.document)
        self._node(doc, "f")["inputs"] = [123]
        with self.assertRaises(DocumentFormatError):
            from_document(doc, self.catalog)

    @staticmethod
    def _node(document, node_id):
        return next(node for node in document["nodes"] if node["id"] == node_id)


class SharedNodeTests(unittest.TestCase):
    def setUp(self):
        self.catalog = base_catalog()

    def test_shared_subtree_evaluated_once_after_roundtrip(self):
        scan = ScanNode(id="so", table="orders")
        filtered = FilterNode(
            id="f", inputs=["so"], predicate=binop(">=", col("amount"), lit(1))
        )
        count_by_region = AggregateNode(
            id="a1",
            inputs=["f"],
            group_keys=("region",),
            aggregates=({"func": "count", "alias": "n", "expr": {"e": "star"}},),
        )
        total_by_region = AggregateNode(
            id="a2",
            inputs=["f"],
            group_keys=("region",),
            aggregates=(
                {
                    "func": "sum",
                    "alias": "t",
                    "expr": {"e": "col", "name": "amount"},
                },
            ),
        )
        join = HashJoinNode(
            id="j",
            inputs=["a1", "a2"],
            left_keys=("region",),
            right_keys=("region",),
        )
        plan = build_plan("j", [scan, filtered, count_by_region, total_by_region, join], self.catalog)
        restored = from_json(plan.to_json(), self.catalog)
        self.assertEqual(restored.nodes["a1"].inputs[0], "f")
        self.assertEqual(restored.nodes["a2"].inputs[0], "f")
        relation, stats = execute(restored, self.catalog)
        self.assertEqual(stats.evaluations["so"], 1)
        self.assertEqual(stats.evaluations["f"], 1)
        rows = sorted(relation.rows)
        self.assertEqual(rows, [("east", 1, 100), ("west", 2, 100)])
        self.assertEqual(stats.total_evaluations, len(restored.order))
