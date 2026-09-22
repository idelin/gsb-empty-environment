from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Callable, Mapping


TaskFunction = Callable[..., Any]


class CycleDetected(Exception):
    def __init__(self, cycle: list[str]):
        self.cycle = list(cycle)
        super().__init__("cycle detected: " + " -> ".join(self.cycle))


@dataclass(frozen=True)
class NodeSpec:
    name: str
    task: TaskFunction
    deps: tuple[str, ...] = ()
    output: str | None = None
    tool: str = "dbk-task"
    args: tuple[Any, ...] = ()
    env_whitelist: tuple[str, ...] = ("PATH",)
    sort_rule: str = "name"

    def output_path(self) -> str:
        return self.output or f"out/{self.name}.txt"


@dataclass
class DependencyGraph:
    nodes: dict[str, NodeSpec]
    runtime_edges: dict[str, set[str]] = field(default_factory=dict)

    @classmethod
    def from_specs(cls, specs: Mapping[str, NodeSpec] | list[NodeSpec]) -> "DependencyGraph":
        if isinstance(specs, Mapping):
            mapping = dict(specs)
        else:
            mapping = {spec.name: spec for spec in specs}
        graph = cls(nodes=mapping)
        for node in mapping:
            graph.runtime_edges[node] = set()
        graph.validate_nodes()
        cycle = graph.find_cycle(graph.static_dependencies)
        if cycle:
            raise CycleDetected(cycle)
        return graph

    def validate_nodes(self) -> None:
        for name, spec in self.nodes.items():
            if spec.name != name:
                raise ValueError(f"node key {name!r} does not match spec name {spec.name!r}")
            for dependency in spec.deps:
                if dependency not in self.nodes:
                    raise KeyError(f"node {name!r} depends on unknown node {dependency!r}")

    def static_dependencies(self, node: str) -> set[str]:
        return set(self.nodes[node].deps)

    def dynamic_dependencies(self, node: str) -> set[str]:
        return set(self.runtime_edges.get(node, ()))

    def dependencies(self, node: str) -> set[str]:
        return self.static_dependencies(node) | self.dynamic_dependencies(node)

    def closure(self, roots: list[str] | set[str] | tuple[str, ...]) -> set[str]:
        pending = list(roots)
        result: set[str] = set()
        while pending:
            node = pending.pop()
            if node in result:
                continue
            if node not in self.nodes:
                raise KeyError(f"unknown node {node!r}")
            result.add(node)
            pending.extend(sorted(self.dependencies(node)))
        return result

    def topo_order(self, roots: list[str] | set[str] | tuple[str, ...]) -> list[str]:
        closure = self.closure(roots)
        indegree = {node: 0 for node in closure}
        dependents: dict[str, list[str]] = {node: [] for node in closure}
        for node in closure:
            for dependency in self.dependencies(node):
                if dependency in closure:
                    indegree[node] += 1
                    dependents[dependency].append(node)
        ready = sorted(node for node, count in indegree.items() if count == 0)
        ordered: list[str] = []
        while ready:
            node = ready.pop(0)
            ordered.append(node)
            for dependent in sorted(dependents[node]):
                indegree[dependent] -= 1
                if indegree[dependent] == 0:
                    ready.append(dependent)
                    ready.sort()
        if len(ordered) != len(closure):
            cycle = self.find_cycle()
            raise CycleDetected(cycle)
        return ordered

    def candidate_cycle(self, node: str, edges: set[str]) -> list[str]:
        def lookup(candidate: str) -> set[str]:
            if candidate == node:
                return self.static_dependencies(candidate) | set(edges)
            return self.dependencies(candidate)

        return self.find_cycle(lookup)

    def commit_dynamic_edges(self, node: str, edges: set[str]) -> None:
        self.runtime_edges[node] = set(edges)

    def find_cycle(
        self,
        dependencies: Callable[[str], set[str]] | None = None,
    ) -> list[str]:
        lookup = dependencies or self.dependencies
        color: dict[str, str] = {node: "white" for node in sorted(self.nodes)}
        stack: list[str] = []

        def visit(node: str) -> list[str] | None:
            color[node] = "gray"
            stack.append(node)
            for dependency in sorted(lookup(node)):
                if color[dependency] == "gray":
                    start = stack.index(dependency)
                    return stack[start:] + [dependency]
                if color[dependency] == "white":
                    found = visit(dependency)
                    if found:
                        return found
            stack.pop()
            color[node] = "black"
            return None

        for node in sorted(self.nodes):
            if color[node] == "white":
                found = visit(node)
                if found:
                    return found
        return []

    def to_json(self) -> dict[str, Any]:
        return {
            "runtime_edges": {
                node: sorted(edges) for node, edges in sorted(self.runtime_edges.items()) if edges
            },
        }

    @classmethod
    def from_json(cls, specs: Mapping[str, NodeSpec], payload: dict[str, Any]) -> "DependencyGraph":
        graph = cls.from_specs(specs)
        graph.runtime_edges = {node: set() for node in specs}
        for node, edges in payload.get("runtime_edges", {}).items():
            graph.runtime_edges[node] = {edge for edge in edges if edge in specs}
        cycle = graph.find_cycle(graph.dependencies)
        if cycle:
            raise CycleDetected(cycle)
        return graph
