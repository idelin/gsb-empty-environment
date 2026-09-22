"""Serialize/deserialize typed, versioned plan DAG documents.

Document format (version 1)::

    {
      "doc_type": "plan-dag",
      "version": 1,
      "root": "<node-id>",
      "nodes": [ { "id": ..., "op": ..., "version": 1, "inputs": [...], ... }, ... ]
    }

Deserialization is strict and runs every structural and type check *before*
execution:

* unknown ``doc_type``/``version``/``op``/``version`` -> error
* an edge pointing at a missing node -> MissingNodeError
* one id used with two distinct payloads -> NodeConflictError
* a directed cycle -> CycleError
* unknown fields, missing fields, unknown columns, ambiguous columns,
  wrong column types -> DocumentFormatError / BindingError

Only the static :data:`plan_dag.nodes.NODE_REGISTRY` can construct operators;
deserializing a document never executes embedded code.
"""

from __future__ import annotations

import json
from dataclasses import dataclass
from typing import Any

from .errors import (
    CycleError,
    DocumentFormatError,
    MissingNodeError,
    NodeConflictError,
    UnknownOperatorError,
    UnsupportedVersionError,
)
from .nodes import NODE_REGISTRY, NODE_VERSION, PlanNode
from .types import Schema
from .util import as_str, parse_object

DOC_TYPE = "plan-dag"
DOC_VERSION = 1


@dataclass
class Plan:
    root: str
    nodes: dict[str, PlanNode]
    order: list[str]

    def to_document(self) -> dict[str, Any]:
        ordered_ids = _reachable_order(self)
        seen = set(ordered_ids)
        ordered_ids.extend(
            node_id for node_id in self.order if node_id not in seen
        )
        return {
            "doc_type": DOC_TYPE,
            "version": DOC_VERSION,
            "root": self.root,
            "nodes": [
                _node_to_dict(self.nodes[node_id]) for node_id in ordered_ids
            ],
        }

    def to_json(self, *, indent: int = 2) -> str:
        return json.dumps(
            self.to_document(), indent=indent, ensure_ascii=False, sort_keys=False
        )


def _node_to_dict(node: PlanNode) -> dict[str, Any]:
    data = node.to_dict()
    data["id"] = node.id
    return {"id": node.id, **{key: data[key] for key in data if key != "id"}}


def _reachable_order(plan: Plan) -> list[str]:
    visited: set[str] = set()
    order: list[str] = []

    def visit(node_id: str) -> None:
        if node_id in visited:
            return
        visited.add(node_id)
        for child in plan.nodes[node_id].inputs:
            visit(child)
        order.append(node_id)

    visit(plan.root)
    return order


def build_plan(root: str, nodes, catalog) -> Plan:
    """Bind an in-memory node set; used by builders and by deserialization."""

    node_map: dict[str, PlanNode] = {}
    if isinstance(nodes, dict):
        node_items = list(nodes.items())
    else:
        node_items = [(node.id, node) for node in nodes]
    for node_id, node in node_items:
        if node_id != node.id:
            raise DocumentFormatError(
                f"node key {node_id!r} does not match node id {node.id!r}"
            )
        if node_id in node_map:
            raise NodeConflictError(f"duplicate node id {node_id!r}")
        node_map[node_id] = node
    if root not in node_map:
        raise MissingNodeError(f"root {root!r} is not present in the node set")
    return _validate_and_bind(root, node_map, catalog)


def from_document(document: Any, catalog) -> Plan:
    obj = parse_object(
        document,
        "document",
        required=("doc_type", "version", "root", "nodes"),
    )
    doc_type = as_str(obj["doc_type"], "document.doc_type")
    if doc_type != DOC_TYPE:
        raise UnsupportedVersionError(
            f"unsupported doc_type {doc_type!r}; expected {DOC_TYPE!r}"
        )
    version = obj["version"]
    if version != DOC_VERSION:
        raise UnsupportedVersionError(
            f"unsupported document version {version!r}; supported: {DOC_VERSION}"
        )
    root = as_str(obj["root"], "document.root")
    raw_nodes = obj["nodes"]
    if not isinstance(raw_nodes, list) or not raw_nodes:
        raise DocumentFormatError("document.nodes: expected a non-empty list")

    node_map: dict[str, PlanNode] = {}
    payloads: dict[str, dict[str, Any]] = {}
    for index, raw in enumerate(raw_nodes):
        where = f"document.nodes[{index}]"
        node = _decode_node(raw, where)
        if node.id in node_map:
            existing_payload = payloads[node.id]
            if existing_payload != node.to_dict_with_id():
                raise NodeConflictError(
                    f"node id {node.id!r} appears multiple times with "
                    f"different content"
                )
            continue
        node_map[node.id] = node
        payloads[node.id] = node.to_dict_with_id()

    if root not in node_map:
        raise MissingNodeError(f"root {root!r} is not defined in the document")

    for node in node_map.values():
        for child in node.inputs:
            if child not in node_map:
                raise MissingNodeError(
                    f"node {node.id!r} references missing input {child!r}"
                )

    return _validate_and_bind(root, node_map, catalog)


def from_json(text: str, catalog) -> Plan:
    try:
        document = json.loads(text)
    except json.JSONDecodeError as exc:
        raise DocumentFormatError(f"invalid JSON: {exc}") from exc
    return from_document(document, catalog)


def _decode_node(raw: Any, where: str) -> PlanNode:
    if not isinstance(raw, dict):
        raise DocumentFormatError(f"{where}: expected an object")
    node_id = as_str(raw.get("id"), f"{where}.id")
    op = as_str(raw.get("op"), f"{where}.op")
    cls = NODE_REGISTRY.get(op)
    if cls is None:
        raise UnknownOperatorError(
            f"{where}: unknown operator {op!r}; allowed: "
            f"{sorted(NODE_REGISTRY)}"
        )
    version = raw.get("version")
    if version != cls.version:
        raise UnsupportedVersionError(
            f"{where}: operator {op!r} version {version!r} is not supported "
            f"(supported: {cls.version})"
        )
    inputs = raw.get("inputs", [])
    if not isinstance(inputs, list) or not all(isinstance(item, str) for item in inputs):
        raise DocumentFormatError(f"{where}.inputs: expected a list of node id strings")
    args = {key: value for key, value in raw.items() if key not in {"id", "op", "version", "inputs"}}
    return cls.build(node_id, inputs, args, where)


def _topological_order(root: str, node_map: dict[str, PlanNode]) -> list[str]:
    indegree = {node_id: 0 for node_id in node_map}
    children: dict[str, list[str]] = {node_id: [] for node_id in node_map}
    for node_id, node in node_map.items():
        for child in node.inputs:
            children[child].append(node_id)
            indegree[node_id] += 1

    queue = sorted(node_id for node_id, degree in indegree.items() if degree == 0)
    order: list[str] = []
    while queue:
        node_id = queue.pop(0)
        order.append(node_id)
        for parent in children[node_id]:
            indegree[parent] -= 1
            if indegree[parent] == 0:
                queue.append(parent)
        queue.sort()

    if len(order) != len(node_map):
        remaining = sorted(set(node_map) - set(order))
        raise CycleError(f"cycle detected involving nodes {remaining}")

    reachable = set()

    def collect(node_id: str) -> None:
        if node_id in reachable:
            return
        reachable.add(node_id)
        for child in node_map[node_id].inputs:
            collect(child)

    collect(root)
    return [node_id for node_id in order if node_id in reachable]


class _SchemaResolver:
    def __init__(self, node_map: dict[str, PlanNode], catalog):
        self.node_map = node_map
        self.catalog = catalog
        self.schemas: dict[str, Schema] = {}

    def bind_all(self, order: list[str]) -> None:
        from .nodes import BindContext

        resolver = self
        node_map = self.node_map

        class Context(BindContext):
            def schema_of(self, node_id: str) -> Schema:
                return resolver.schemas[node_id]

        context = Context(self.catalog)
        for node_id in order:
            node = node_map[node_id]
            bound = node.bind(context)
            node.bound = bound
            self.schemas[node_id] = bound.output_schema


def _validate_and_bind(root: str, node_map: dict[str, PlanNode], catalog) -> Plan:
    order = _topological_order(root, node_map)
    _SchemaResolver(node_map, catalog).bind_all(order)
    return Plan(root=root, nodes=node_map, order=order)
