from __future__ import annotations

from dataclasses import dataclass, field
from pathlib import Path
from typing import Mapping

from .graph import CycleDetected, NodeSpec
from .kernel import BuildKernel, TaskCanceled, TaskContext
from .vfs import VFS


@dataclass
class ReferenceResult:
    outputs: dict[str, bytes]
    executed: set[str]
    failed_nodes: dict[str, Exception] = field(default_factory=dict)
    cycles: dict[str, list[str]] = field(default_factory=dict)

    def output(self, node: str) -> bytes:
        return self.outputs[node]


class ReferenceExecutor:
    """Rebuild requested roots from the snapshot without reading old cache state."""

    def __init__(self, vfs: VFS, specs: Mapping[str, NodeSpec] | list[NodeSpec]):
        self.source = vfs
        if isinstance(specs, Mapping):
            self.specs = dict(specs)
        else:
            self.specs = {spec.name: spec for spec in specs}

    def run(self, roots: list[str] | tuple[str, ...], workspace: str | Path) -> ReferenceResult:
        copied = self.source.copy_tree(workspace)
        for spec in self.specs.values():
            copied.remove(spec.output_path())
        kernel = BuildKernel.reference(workspace, list(self.specs.values()))
        kernel.store.wal_path.unlink(missing_ok=True)
        kernel.store.event_path.unlink(missing_ok=True)
        request = kernel.request_build("reference", roots)
        executed: set[str] = set()
        iterations = 0
        while True:
            iterations += 1
            if iterations > 1000:
                raise RuntimeError("reference executor did not reach a fixed point")
            ready = self._ready_nodes(kernel, roots, executed)
            if ready:
                node = ready[0]
                self._execute_once(kernel, node, executed)
                continue
            closure = kernel.graph.closure(roots)
            all_present = all(
                kernel.vfs.exists(kernel.graph.nodes[node].output_path()) for node in closure
            )
            has_failure = any(node in kernel.failed_nodes for node in closure)
            if all_present or has_failure:
                break
            if not kernel.active_by_node:
                raise RuntimeError("reference executor is stuck")
        result = ReferenceResult(outputs={}, executed=executed)
        for name, spec in self.specs.items():
            if name in kernel.failed_nodes:
                error = kernel.failed_nodes[name]
                result.failed_nodes[name] = error
                if isinstance(error, CycleDetected):
                    result.cycles[name] = list(error.cycle)
            elif copied.exists(spec.output_path()):
                result.outputs[name] = copied.read(spec.output_path())
        kernel.close()
        return result

    def _ready_nodes(
        self,
        kernel: BuildKernel,
        roots: list[str] | tuple[str, ...],
        executed: set[str],
    ) -> list[str]:
        closure = kernel.graph.closure(roots)
        result: list[str] = []
        try:
            ordered = kernel.graph.topo_order(closure)
        except CycleDetected as cycle:
            node = cycle.cycle[-2]
            kernel.failed_nodes[node] = CycleDetected(cycle.cycle)
            return
        for node in ordered:
            if node in kernel.failed_nodes:
                continue
            dependencies = kernel.graph.dependencies(node)
            output_path = kernel.graph.nodes[node].output_path()
            deps_present = all(kernel.vfs.exists(kernel.graph.nodes[dep].output_path()) for dep in dependencies)
            if deps_present and not kernel.vfs.exists(output_path):
                result.append(node)
        return sorted(set(result) - executed)

    def _execute_once(self, kernel: BuildKernel, node: str, executed: set[str]) -> None:
        from .kernel import _Pause

        pause = _Pause()
        context = TaskContext(node, kernel.vfs, kernel, pause)
        context.reference_mode = True
        try:
            value = kernel.graph.nodes[node].task(context)
        except TaskCanceled:
            return
        except Exception as exc:
            kernel.failed_nodes[node] = exc
            executed.add(node)
            return
        executed.add(node)
        edges = kernel.graph.static_dependencies(node) | set(context.node_dependencies)
        cycle = kernel.graph.candidate_cycle(node, edges)
        if cycle:
            kernel.failed_nodes[node] = CycleDetected(cycle)
            return
        dependencies = kernel._observed_dependencies(context)
        if not kernel.versions_match(dependencies):
            raise RuntimeError("reference snapshot changed during full rebuild")
        changed = edges != kernel.graph.dependencies(node)
        kernel.graph.commit_dynamic_edges(node, edges - kernel.graph.static_dependencies(node))
        if changed:
            missing_dependencies = [
                dep for dep in edges if not kernel.vfs.exists(kernel.graph.nodes[dep].output_path())
            ]
            if missing_dependencies:
                executed.discard(node)
                return
        data = kernel.encode_output(value)
        kernel.vfs.write(kernel.graph.nodes[node].output_path(), data)
