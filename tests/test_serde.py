import json
import unittest

from plan_dag import (
    DuplicateNodeError,
    FormatVersionError,
    InvalidDocumentError,
    UnknownOperatorError,
    dumps,
    from_document,
    loads,
    to_document,
)
from plan_dag.builders import build_salary_report_plan
from plan_dag.sample_data import build_catalog
from plan_dag.serde import find_id_conflicts, merge_documents


BASE_DOC = {
    "format_version": "1.0",
    "roots": ["f"],
    "nodes": [
        {"id": "t", "op": {"@type": "scan", "table": "employees"}, "inputs": []},
        {"id": "f", "op": {"@type": "limit", "count": 2}, "inputs": ["t"]},
    ],
}


class SerdeTests(unittest.TestCase):
    def test_roundtrip_preserves_document(self):
        dag = build_salary_report_plan()
        text = dumps(dag)
        restored = loads(text)
        self.assertEqual(to_document(restored), json.loads(text))
        self.assertEqual(restored.roots, dag.roots)

    def test_roundtrip_binds_and_executes(self):
        dag = loads(dumps(build_salary_report_plan()))
        bound = dag.bind(build_catalog())
        self.assertEqual(
            [f.name for f in bound.schema_of("payroll_by_dept").fields],
            ["dname", "headcount", "total_salary", "avg_salary"],
        )

    def test_invalid_json_rejected(self):
        with self.assertRaises(InvalidDocumentError):
            loads("{not json")

    def test_unknown_operator_tag_rejected(self):
        doc = json.loads(json.dumps(BASE_DOC))
        doc["nodes"][0]["op"] = {"@type": "os.system", "cmd": "echo pwned"}
        with self.assertRaises(UnknownOperatorError):
            from_document(doc)

    def test_unknown_expression_tag_rejected(self):
        doc = json.loads(json.dumps(BASE_DOC))
        doc["nodes"][1] = {
            "id": "f",
            "op": {"@type": "filter",
                   "predicate": {"@type": "eval", "code": "__import__('os')"}},
            "inputs": ["t"],
        }
        with self.assertRaises(UnknownOperatorError):
            from_document(doc)

    def test_tags_must_be_strings(self):
        doc = json.loads(json.dumps(BASE_DOC))
        doc["nodes"][0]["op"] = {"@type": 123}
        with self.assertRaises(InvalidDocumentError):
            from_document(doc)

    def test_unknown_keys_rejected(self):
        for mutate in (
            lambda d: d.update(extra=1),
            lambda d: d["nodes"][0].update(extra=1),
            lambda d: d["nodes"][0]["op"].update(extra=1),
        ):
            doc = json.loads(json.dumps(BASE_DOC))
            mutate(doc)
            with self.assertRaises(InvalidDocumentError):
                from_document(doc)

    def test_versions(self):
        with self.assertRaises(FormatVersionError):
            from_document({"nodes": [], "roots": []})
        doc = json.loads(json.dumps(BASE_DOC))
        doc["format_version"] = "9.1"
        with self.assertRaises(FormatVersionError):
            from_document(doc)
        doc["format_version"] = "1.5"  # same major, newer minor -> accepted
        from_document(doc)
        doc["format_version"] = "banana"
        with self.assertRaises(FormatVersionError):
            from_document(doc)

    def test_arity_checked_on_load(self):
        doc = json.loads(json.dumps(BASE_DOC))
        doc["nodes"][0]["inputs"] = ["f"]  # scan takes zero inputs
        with self.assertRaises(InvalidDocumentError):
            from_document(doc)

    def test_duplicate_id_in_one_document(self):
        doc = json.loads(json.dumps(BASE_DOC))
        doc["nodes"].append(
            {"id": "t", "op": {"@type": "scan", "table": "x"}, "inputs": []}
        )
        with self.assertRaises(DuplicateNodeError):
            from_document(doc)

    def test_bool_not_accepted_as_integer(self):
        doc = json.loads(json.dumps(BASE_DOC))
        doc["nodes"][1]["op"]["count"] = True
        with self.assertRaises(InvalidDocumentError):
            from_document(doc)

    def test_count_star_takes_no_arg(self):
        doc = {
            "format_version": "1.0",
            "roots": ["a"],
            "nodes": [
                {"id": "t", "op": {"@type": "scan", "table": "employees"}, "inputs": []},
                {"id": "a",
                 "op": {"@type": "aggregate", "group_keys": [],
                        "aggs": [{"func": "count_star", "alias": "c",
                                  "arg": {"@type": "column_ref", "name": "id"}}]},
                 "inputs": ["t"]},
            ],
        }
        with self.assertRaises(InvalidDocumentError):
            from_document(doc)

    def test_id_conflict_detection_and_merge(self):
        dag = build_salary_report_plan()
        doc_a = to_document(dag)
        doc_b = json.loads(json.dumps(doc_a))
        for node in doc_b["nodes"]:
            if node["id"] == "emp_scan":
                node["op"] = {"@type": "scan", "table": "departments"}
        conflicts = find_id_conflicts(doc_a, doc_b)
        self.assertEqual(len(conflicts), 1)
        self.assertIn("emp_scan", conflicts[0])
        with self.assertRaises(DuplicateNodeError):
            merge_documents(doc_a, doc_b)

    def test_merge_keeps_shared_child_once(self):
        dag = build_salary_report_plan()
        merged = merge_documents(to_document(dag), to_document(dag),
                                 roots=list(dag.roots))
        restored = from_document(merged)
        self.assertEqual(len(restored.nodes), len(dag.nodes))
        bound = restored.bind(build_catalog())
        ex = __import__("plan_dag").Executor(build_catalog())
        ex.execute_plan(bound)
        self.assertEqual(ex.last_context.scan_count, 2)


if __name__ == "__main__":
    unittest.main()


class LocalJsonOnlyTests(unittest.TestCase):
    def test_same_arity_node_twice_is_allowed_but_id_duplicate_is_not(self):
        doc = {
            "format_version": "1.0",
            "roots": ["j"],
            "nodes": [
                {"id": "l", "op": {"@type": "scan", "table": "employees"}, "inputs": []},
                {"id": "r", "op": {"@type": "scan", "table": "departments"}, "inputs": []},
                {"id": "j",
                 "op": {"@type": "join", "join_type": "inner",
                        "left_keys": ["dept"], "right_keys": ["dept"]},
                 "inputs": ["l", "r"]},
            ],
        }
        dag = from_document(doc)
        self.assertEqual(dag.nodes["j"].inputs, ("l", "r"))

    def test_node_must_be_object(self):
        doc = json.loads(json.dumps(BASE_DOC))
        doc["nodes"] = ["scalar"]
        with self.assertRaises(InvalidDocumentError):
            from_document(doc)

    def test_inputs_must_be_strings(self):
        doc = json.loads(json.dumps(BASE_DOC))
        doc["nodes"][0]["inputs"] = [1]
        with self.assertRaises(InvalidDocumentError):
            from_document(doc)
