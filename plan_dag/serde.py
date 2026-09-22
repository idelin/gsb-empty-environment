"""Typed, versioned document (de)serialization.

Document format (JSON)::

    {
      "format_version": "1.0",
      "nodes": [
        {"id": "n1", "op": {"@type": "scan", "table": "t"}, "inputs": []},
        ...
      ],
      "roots": ["n5"]
    }

Deserialization is strict:

* unknown top-level keys, node keys, operator/expression tags are rejected;
* ``@type`` tags resolve through explicit registries -- there is no dynamic
  import, ``eval``, ``exec``, or any other code execution path;
* structural checks (missing inputs, cycles, arity) happen on load; semantic
  field/type binding happens in :meth:`PlanDag.bind` before execution.
"""

from __future__ import annotations

import json
from typing import Any

from plan_dag.errors import (
    DuplicateNodeError,
    FormatVersionError,
    InvalidDocumentError,
)
from plan_dag.plan import Node, PlanDag
from plan_dag.operators import spec_from_data

SUPPORTED_FORMAT_VERSION = "1.0"
_FORMAT_MAJOR = 1
_TOP_KEYS = {"format_version", "nodes", "roots"}
_NODE_KEYS = {"id", "op", "inputs"}


def _parse_version(version: Any) -> tuple[int, int]:
    if not isinstance(version, str):
        raise FormatVersionError("'format_version' must be a string like '1.0'")
    parts = version.split(".")
    if len(parts) != 2 or not all(p.isdigit() for p in parts):
        raise FormatVersionError(f"malformed 'format_version' {version!r}; expected MAJOR.MINOR")
    return int(parts[0]), int(parts[1])


def _node_from_data(data: object) -> Node:
    if not isinstance(data, dict):
        raise InvalidDocumentError("each node must be an object")
    extra = set(data) - _NODE_KEYS
    if extra:
        raise InvalidDocumentError(f"node has unknown keys {sorted(extra)}")
    if "id" not in data or "op" not in data or "inputs" not in data:
        raise InvalidDocumentError("node must contain 'id', 'op', and 'inputs'")
    node_id = data["id"]
    if not isinstance(node_id, str) or not node_id:
        raise InvalidDocumentError("node 'id' must be a non-empty string")
    inputs = data["inputs"]
    if not isinstance(inputs, list) or not all(isinstance(i, str) and i for i in inputs):
        raise InvalidDocumentError(f"node {node_id!r}: 'inputs' must be a list of strings")
    spec = spec_from_data(data["op"])
    spec._check_arity(len(inputs), node_id)
    return Node(id=node_id, spec=spec, inputs=tuple(inputs))


def from_document(doc: object, *, strict_version: bool = False) -> PlanDag:
    """Build a :class:`PlanDag` from a plain document object."""
    if not isinstance(doc, dict):
        raise InvalidDocumentError("document must be an object")
    extra = set(doc) - _TOP_KEYS
    if extra:
        raise InvalidDocumentError(f"document has unknown top-level keys {sorted(extra)}")
    if "format_version" not in doc:
        raise FormatVersionError("document is missing 'format_version'")
    major, minor = _parse_version(doc["format_version"])
    if major != _FORMAT_MAJOR:
        raise FormatVersionError(
            f"unsupported format major version {major}; this build supports {_FORMAT_MAJOR}.x"
        )
    if strict_version and doc["format_version"] != SUPPORTED_FORMAT_VERSION:
        raise FormatVersionError(
            f"exact version {SUPPORTED_FORMAT_VERSION} required, got {doc['format_version']}"
        )
    nodes_data = doc.get("nodes")
    if not isinstance(nodes_data, list) or not nodes_data:
        raise InvalidDocumentError("document 'nodes' must be a non-empty list")
    nodes: list[Node] = []
    seen: set[str] = set()
    for raw in nodes_data:
        node = _node_from_data(raw)
        if node.id in seen:
            raise DuplicateNodeError(
                f"node id {node.id!r} appears multiple times in one document"
            )
        seen.add(node.id)
        nodes.append(node)
    roots = doc.get("roots")
    if roots is not None and (
        not isinstance(roots, list) or not all(isinstance(r, str) for r in roots)
    ):
        raise InvalidDocumentError("document 'roots' must be a list of strings")
    return PlanDag(nodes=nodes, roots=roots)


def to_document(dag: PlanDag, *, version: str = SUPPORTED_FORMAT_VERSION) -> dict:
    """Serialize a :class:`PlanDag`; children are emitted before their parents."""
    _parse_version(version)
    nodes_out: list[dict] = []
    for nid in dag.order:
        n = dag.nodes[nid]
        nodes_out.append(
            {"id": n.id, "op": n.spec.to_data(), "inputs": list(n.inputs)}
        )
    return {
        "format_version": version,
        "nodes": nodes_out,
        "roots": list(dag.roots),
    }


def dumps(dag: PlanDag, *, indent: int = 2, version: str = SUPPORTED_FORMAT_VERSION) -> str:
    return json.dumps(to_document(dag, version=version), indent=indent, sort_keys=False)


def loads(text: str | bytes, *, strict_version: bool = False) -> PlanDag:
    if not isinstance(text, (str, bytes)):
        raise InvalidDocumentError("loads expects a str or bytes")
    try:
        doc = json.loads(text)
    except json.JSONDecodeError as exc:
        raise InvalidDocumentError(f"document is not valid JSON: {exc}") from None
    return from_document(doc, strict_version=strict_version)


def node_signature(node_data: object) -> str:
    """Stable fingerprint of a serialized node's content (everything but the id)."""
    if not isinstance(node_data, dict):
        raise InvalidDocumentError("node signature input must be an object")
    content = {"op": node_data.get("op"), "inputs": sorted(node_data.get("inputs", []))}
    return json.dumps(content, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


def _index_nodes(doc: dict) -> dict[str, dict]:
    return {n["id"]: n for n in doc["nodes"]}


def find_id_conflicts(doc_a: object, doc_b: object) -> list[str]:
    """Return descriptions of same-id nodes whose content differs."""
    if not isinstance(doc_a, dict) or not isinstance(doc_b, dict):
        raise InvalidDocumentError("find_id_conflicts expects two document objects")
    a = _index_nodes(doc_a)
    b = _index_nodes(doc_b)
    conflicts: list[str] = []
    for nid in sorted(set(a) & set(b)):
        if node_signature(a[nid]) != node_signature(b[nid]):
            conflicts.append(
                f"node {nid!r}: same id but different content "
                f"(a={a[nid].get('op')} vs b={b[nid].get('op')})"
            )
    return conflicts


def merge_documents(*docs: object, roots: list[str] | None = None) -> dict:
    """Merge documents sharing node ids (e.g. recovering a shared sub-DAG).

    Nodes with the same id must be byte-for-byte equivalent in content; a
    mismatch raises :class:`DuplicateNodeError`. Shared ids are emitted once,
    so the merged DAG keeps the sharing (two parents, one child node).
    """
    if not docs:
        raise InvalidDocumentError("merge_documents requires at least one document")
    merged: dict[str, dict] = {}
    all_roots: list[str] = []
    version = SUPPORTED_FORMAT_VERSION
    for doc in docs:
        if not isinstance(doc, dict):
            raise InvalidDocumentError("each merged value must be a document object")
        if "format_version" in doc:
            version = doc["format_version"]
        for raw in doc.get("nodes", []):
            nid = raw["id"]
            if nid in merged:
                if node_signature(merged[nid]) != node_signature(raw):
                    raise DuplicateNodeError(
                        f"cannot merge: node {nid!r} exists with different content"
                    )
            else:
                merged[nid] = raw
        all_roots.extend(doc.get("roots") or [])
    if roots is None:
        roots = list(dict.fromkeys(all_roots))
    referenced = {i for n in merged.values() for i in n["inputs"]}
    if not roots:
        roots = [nid for nid in merged if nid not in referenced]
    return {
        "format_version": version,
        "nodes": [merged[nid] for nid in sorted(merged)],
        "roots": roots,
    }
