"""Plan DAG structure: nodes, structural validation, and binding driver."""

from __future__ import annotations

from dataclasses import dataclass

from plan_dag.catalog import Catalog
from plan_dag.errors import (
    CycleError,
    InvalidDocumentError,
    MissingNodeError,
    PlanValidationError,
)
from plan_dag.operators import OperatorSpec

from plan_dag.runtime import BoundOp, BoundNode, BoundPlan, Relation  # noqa: F401


@dataclass(frozen=True)
class Node:
    id: str
    spec: OperatorSpec
    inputs: tuple[str, ...]

    def __post_init__(self):
        if not isinstance(self.id, str) or not self.id:
            raise InvalidDocumentError("node id must be a non-empty string")
        if not isinstance(self.spec, OperatorSpec):
            raise InvalidDocumentError(f"node {self.id!r} has no valid operator spec")
        if not isinstance(self.inputs, (tuple, list)) or not all(
            isinstance(i, str) and i for i in self.inputs
        ):
            raise InvalidDocumentError(f"node {self.id!r} inputs must be non-empty strings")
        object.__setattr__(self, "inputs", tuple(self.inputs))


class PlanDag:
    def __init__(
        self,
        nodes: list[Node],
        roots: list[str] | tuple[str, ...] | None = None,
    ):
        self.nodes: dict[str, Node] = {}
        for n in nodes:
            if n.id in self.nodes:
                raise InvalidDocumentError(f"duplicate node id {n.id!r} in one DAG")
            self.nodes[n.id] = n
        if roots is None:
            referenced = {i for n in nodes for i in n.inputs}
            self.roots = tuple(nid for nid in self.nodes if nid not in referenced)
            if not self.roots and self.nodes:
                raise PlanValidationError("DAG has cycles and no root (every node is referenced)")
        else:
            for r in roots:
                if r not in self.nodes:
                    raise MissingNodeError(f"root {r!r} is not a node")
            self.roots = tuple(roots)
        self.order = self._toposort()

    def _check_references(self) -> None:
        for nid, n in self.nodes.items():
            for ref in n.inputs:
                if ref not in self.nodes:
                    raise MissingNodeError(
                        f"node {nid!r} references missing input node {ref!r}"
                    )
                if ref == nid:
                    raise CycleError(f"node {nid!r} references itself")

    def _toposort(self) -> list[str]:
        """DFS post-order; raises MissingNodeError / CycleError."""
        self._check_references()
        WHITE, GRAY, BLACK = 0, 1, 2
        color = {nid: WHITE for nid in self.nodes}
        order: list[str] = []

        def visit(nid: str, stack: list[str]) -> None:
            if color[nid] == BLACK:
                return
            if color[nid] == GRAY:
                start = stack.index(nid)
                cycle = stack[start:] + [nid]
                raise CycleError("cycle detected: " + " -> ".join(cycle))
            color[nid] = GRAY
            stack.append(nid)
            for ref in self.nodes[nid].inputs:
                visit(ref, stack)
            stack.pop()
            color[nid] = BLACK
            order.append(nid)

        for nid in sorted(self.nodes):
            visit(nid, [])
        return order

    def bind(self, catalog: Catalog) -> BoundPlan:
        """Bind every node in topological order. Fails before execution."""
        bound: dict[str, BoundNode] = {}
        for nid in self.order:
            node = self.nodes[nid]
            child_nodes = [bound[i] for i in node.inputs]
            op = node.spec.bind(child_nodes, catalog, nid)
            bound[nid] = BoundNode(id=nid, inputs=node.inputs, op=op)
        return BoundPlan(nodes=bound, order=list(self.order), roots=self.roots, catalog=catalog)
