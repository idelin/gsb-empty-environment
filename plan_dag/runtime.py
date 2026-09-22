"""Low-level runtime types shared by plans, operators, and the executor."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, TYPE_CHECKING

from plan_dag.datatypes import Schema

if TYPE_CHECKING:
    from plan_dag.catalog import Catalog

Relation = list[list[Any]]


class BoundOp:
    """A bound operator: output schema plus an execute function."""

    schema: Schema

    def execute(self, inputs: list[Relation], context: Any) -> Relation:
        raise NotImplementedError


@dataclass
class BoundNode:
    id: str
    inputs: tuple[str, ...]
    op: BoundOp

    @property
    def schema(self) -> Schema:
        return self.op.schema


class BoundPlan:
    def __init__(
        self,
        nodes: dict[str, BoundNode],
        order: list[str],
        roots: tuple[str, ...],
        catalog: "Catalog",
    ):
        self.nodes = nodes
        self.order = order
        self.roots = roots
        self.catalog = catalog

    def schema_of(self, node_id: str) -> Schema:
        return self.nodes[node_id].schema
