"""End-to-end runnable sample.

Usage::

    python3 demo.py

Shows: typed/versioned serialization, strict deserialization, cycle / missing
node / type-mismatch / id-conflict detection, bag + NULL semantics, and that a
recovered shared child stays shared (one scan, one memoized result).
"""

from __future__ import annotations

import json

from plan_dag import (
    AmbiguousColumnError,
    BindingError,
    CycleError,
    DocumentError,
    DuplicateNodeError,
    Executor,
    FormatVersionError,
    MissingNodeError,
    UnknownOperatorError,
    dumps,
    from_document,
)
from plan_dag.builders import (
    build_salary_over_60_plan,
    build_salary_report_plan,
    build_type_mismatch_plan,
)
from plan_dag.sample_data import build_catalog
from plan_dag.serde import find_id_conflicts, merge_documents, to_document


def banner(title: str) -> None:
    print()
    print("=" * 72)
    print(title)
    print("=" * 72)


def show_rows(catalog, plan, node_id: str) -> None:
    bound = plan.bind(catalog)
    fields = [f.name for f in bound.schema_of(node_id).fields]
    rows = Executor(catalog).execute(bound, node_id)
    print(f"root {node_id!r} columns={fields} rows={len(rows)}")
    for row in rows:
        print("   ", row)


def main() -> None:
    catalog = build_catalog()
    print(f"local catalog: {catalog.names()} (no SQL engine involved)")

    banner("1. Serialize to a typed + versioned document")
    plan = build_salary_report_plan()
    doc = to_document(plan)
    print(dumps(plan))

    banner("2. Round-trip deserialization (strict allow-list tags)")
    restored = from_document(json.loads(json.dumps(doc)))
    print("operators:", [n.spec.TAG for n in restored.nodes.values()])
    print("roots:", restored.roots)

    banner("3. Execute: bag semantics, NULL semantics, LEFT JOIN + aggregation")
    bound = restored.bind(catalog)
    executor = Executor(catalog)
    results = executor.execute_plan(bound)
    for root in restored.roots:
        fields = [f.name for f in bound.schema_of(root).fields]
        print(f"root {root!r} columns={fields}")
        for row in results[root]:
            print("   ", row)
    print(f"base-table scans actually performed: {executor.last_context.scan_count} (two roots, one shared plan -> 2 scans)")

    banner("4. Shared child stays shared after recovery (memoized once)")
    # Fragment A: payroll report. Fragment B: a second consumer that reuses the
    # SAME node ids (emp_scan/dept_scan/emp_dept/...). Merging keeps one copy.
    from plan_dag.builders import project as pj, scan as sc
    from plan_dag.expr import ColumnRef as CR
    extra = pj(
        "eng_names", "emp_dept",
        [("dname", CR("dname")), ("name", CR("name"))],
    )
    fragment_b_nodes = [n for n in plan.nodes.values()] + [extra]
    from plan_dag.plan import PlanDag
    doc_b = to_document(PlanDag(fragment_b_nodes, roots=["eng_names"]))
    merged_doc = merge_documents(doc, doc_b, roots=["payroll_by_dept", "eng_names"])
    merged = from_document(merged_doc)
    mbound = merged.bind(catalog)
    mex = Executor(catalog)
    mresults = mex.execute_plan(mbound)
    consumers = {
        nid: [p for p, n in merged.nodes.items() if nid in n.inputs]
        for nid in ("emp_scan", "dept_scan", "emp_dept")
    }
    print(f"merged node count={len(merged.nodes)} (shared ids emitted once)")
    print("consumers of shared nodes:", consumers)
    print(f"scan_count for both roots = {mex.last_context.scan_count} "
          "(shared base scans evaluated once)")
    print("eng_names rows:", len(mresults["eng_names"]))

    banner("5. NULL / three-valued logic: salary > 60")
    vl = build_salary_over_60_plan()
    show_rows(catalog, vl, "s_proj")
    print("(Fay's NULL salary is not > 60 and is not false -> row removed; "
          "duplicate Bob is retained)")

    banner("6. Wrong column types found BEFORE execution")
    bad = build_type_mismatch_plan()
    try:
        bad.bind(catalog)
    except BindingError as exc:
        print(f"BindingError: {exc}")

    banner("7. Invalid documents are rejected (no arbitrary code runs)")
    cases = {
        "unknown operator tag":
            {"format_version": "1.0", "roots": ["x"],
             "nodes": [{"id": "x", "op": {"@type": "os.system"}, "inputs": []}]},
        "unknown expression tag":
            {"format_version": "1.0", "roots": ["f"],
             "nodes": [
                 {"id": "t", "op": {"@type": "scan", "table": "employees"}, "inputs": []},
                 {"id": "f",
                  "op": {"@type": "filter",
                         "predicate": {"@type": "__import__", "name": "os"}},
                  "inputs": ["t"]},
             ]},
        "missing node":
            {"format_version": "1.0", "roots": ["f"],
             "nodes": [{"id": "f", "op": {"@type": "limit", "count": 1},
                        "inputs": ["ghost"]}]},
        "cycle":
            {"format_version": "1.0", "roots": ["a"],
             "nodes": [
                 {"id": "a", "op": {"@type": "limit", "count": 1}, "inputs": ["b"]},
                 {"id": "b", "op": {"@type": "limit", "count": 1}, "inputs": ["a"]},
             ]},
        "unsupported major version":
            {"format_version": "9.0", "roots": [], "nodes": []},
    }
    for label, bad_doc in cases.items():
        try:
            from_document(bad_doc)
            print(f"  {label}: NOT REJECTED (bug!)")
        except (DocumentError, CycleError, MissingNodeError, UnknownOperatorError) as exc:
            print(f"  {label}: {type(exc).__name__}: {exc}")

    banner("8. Same id with different node content conflicts")
    doc_b = json.loads(json.dumps(doc))
    # mutate the emp_scan node while keeping its id -> same id, new content
    for node in doc_b["nodes"]:
        if node["id"] == "emp_scan":
            node["op"] = {"@type": "scan", "table": "departments"}
            break
    conflicts = find_id_conflicts(doc, doc_b)
    for c in conflicts:
        print(" ", c)
    try:
        merge_documents(doc, doc_b)
    except DuplicateNodeError as exc:
        print(f"  merge rejected: {exc}")



if __name__ == "__main__":
    main()
