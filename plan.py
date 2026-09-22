"""Logical plan DAG nodes with structural identity for subplan sharing.

Two subplans are shareable iff their structural ``key()`` is equal, i.e.
same operator, same parameters, same children.  A ``Filter`` node is part
of the key, so two consumers with *different* filters share the subplan
*below* the filters, never the filtered results.
"""
from __future__ import annotations

from dataclasses import dataclass, field

# Predicate registry: plans reference predicates by name so that the
# structural key stays serializable and comparable.
PREDICATES = {}


def register_predicate(name, fn):
    PREDICATES[name] = fn
    return fn


class Node:
    def children(self):
        return ()

    def key(self):
        raise NotImplementedError


@dataclass(frozen=True)
class Scan(Node):
    table_name: str

    def key(self):
        return ("scan", self.table_name)


@dataclass(frozen=True)
class Filter(Node):
    pred_name: str
    child: Node
    selectivity: float = 0.5  # estimation hint only

    def children(self):
        return (self.child,)

    def key(self):
        return ("filter", self.pred_name, self.selectivity, self.child.key())


@dataclass(frozen=True)
class Project(Node):
    cols: tuple
    child: Node

    def children(self):
        return (self.child,)

    def key(self):
        return ("project", tuple(self.cols), self.child.key())


@dataclass(frozen=True)
class Join(Node):
    left: Node
    right: Node
    left_keys: tuple
    right_keys: tuple

    def children(self):
        return (self.left, self.right)

    def key(self):
        return (
            "join",
            tuple(self.left_keys),
            tuple(self.right_keys),
            self.left.key(),
            self.right.key(),
        )


def schema_of(node, tables):
    """Output schema of a plan node.

    Joins concatenate left and right schemas; a name collision on the
    right side is disambiguated with a ``#r`` suffix.
    """
    if isinstance(node, Scan):
        return tables[node.table_name].schema
    if isinstance(node, Filter):
        return schema_of(node.child, tables)
    if isinstance(node, Project):
        return tuple(node.cols)
    if isinstance(node, Join):
        left = schema_of(node.left, tables)
        right = schema_of(node.right, tables)
        out = list(left)
        for col in right:
            out.append(col if col not in left else f"{col}#r")
        return tuple(out)
    raise TypeError(node)
