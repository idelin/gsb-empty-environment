"""Computes the union closure of features and packages from local JSON inputs.

Activation is the least fixed point reached by forward propagation from root
seeds (forced features, default features, unconditional dependencies). A set
of features or packages that can only enable each other -- a self-supporting
cycle with no root seed -- therefore never activates; such cycles are
detected and reported as diagnostics.
"""

from __future__ import annotations

from dataclasses import dataclass

from . import expr
from .errors import ConflictError, ResolutionError
from .model import Dependency, Package, Registry, RootRequest
from .version import Requirement

_MAX_ROUNDS = 1000


@dataclass(frozen=True)
class Reason:
    """Provenance of an activated feature, as a linked chain to a root seed."""

    kind: str
    detail: str
    parent: "Reason | None" = None

    def chain(self) -> list[str]:
        steps = []
        node: Reason | None = self
        while node is not None:
            steps.append(node.detail)
            node = node.parent
        return steps


@dataclass(frozen=True)
class _Seed:
    features: tuple[str, ...]
    disable: tuple[str, ...]
    default_features: bool
    source: str


class _Node:
    def __init__(self, package: Package):
        self.package = package
        self.seeds: list[_Seed] = []
        self.forced: dict[str, Reason] = {}
        self.disabled: dict[str, str] = {}
        self.default_features = False
        self.active: dict[str, Reason] = {}
        self.deps_done: set[int] = set()


@dataclass(frozen=True)
class ResolvedPackage:
    name: str
    version: str
    features: tuple[str, ...]
    forced: tuple[str, ...]
    defaulted: tuple[str, ...]
    implied: tuple[str, ...]
    disabled: tuple[str, ...]

    def to_dict(self) -> dict:
        return {
            "name": self.name,
            "version": self.version,
            "features": list(self.features),
            "origins": {
                "forced": list(self.forced),
                "default": list(self.defaulted),
                "implied": list(self.implied),
            },
            "disabled": list(self.disabled),
        }


@dataclass(frozen=True)
class Diagnostic:
    level: str
    kind: str
    message: str
    cycle: tuple[str, ...]

    def to_dict(self) -> dict:
        return {
            "level": self.level,
            "kind": self.kind,
            "message": self.message,
            "cycle": list(self.cycle),
        }


@dataclass(frozen=True)
class Resolution:
    root: str
    packages: tuple[ResolvedPackage, ...]
    diagnostics: tuple[Diagnostic, ...]

    def to_dict(self) -> dict:
        return {
            "root": self.root,
            "packages": [package.to_dict() for package in self.packages],
            "diagnostics": [diag.to_dict() for diag in self.diagnostics],
        }


def _strongly_connected_components(graph: dict) -> list[list]:
    index_of: dict = {}
    lowlink: dict = {}
    stack: list = []
    on_stack: set = set()
    components: list[list] = []
    counter = 0
    for start in graph:
        if start in index_of:
            continue
        index_of[start] = lowlink[start] = counter
        counter += 1
        stack.append(start)
        on_stack.add(start)
        work = [(start, iter(sorted(graph.get(start, ()), key=repr)))]
        while work:
            node, iterator = work[-1]
            descended = False
            for succ in iterator:
                if succ not in index_of:
                    index_of[succ] = lowlink[succ] = counter
                    counter += 1
                    stack.append(succ)
                    on_stack.add(succ)
                    work.append((succ, iter(sorted(graph.get(succ, ()), key=repr))))
                    descended = True
                    break
                if succ in on_stack:
                    lowlink[node] = min(lowlink[node], index_of[succ])
            if descended:
                continue
            work.pop()
            if work:
                parent = work[-1][0]
                lowlink[parent] = min(lowlink[parent], lowlink[node])
            if lowlink[node] == index_of[node]:
                component = []
                while True:
                    member = stack.pop()
                    on_stack.discard(member)
                    component.append(member)
                    if member == node:
                        break
                components.append(component)
    return components


def _reachable(graph: dict, seeds: set) -> set:
    seen = set(seeds)
    pending = list(seeds)
    while pending:
        node = pending.pop()
        for succ in graph.get(node, ()):
            if succ not in seen:
                seen.add(succ)
                pending.append(succ)
    return seen


def _label(key: tuple) -> str:
    if key[0] == "package":
        return f"package {key[1]}"
    return f"feature {key[1]}/{key[2]}"


class Resolver:
    def __init__(self, registry: Registry):
        self.registry = registry
        self.constraints: dict[str, list[tuple[Requirement, str]]] = {}
        self.nodes: dict[str, _Node] = {}
        self.root_name = ""

    def resolve(self, request: RootRequest) -> Resolution:
        self.root_name = request.package
        self._add_constraint(request.package, request.req, "root request")
        self._apply_seed(
            request.package,
            _Seed(
                features=request.features,
                disable=request.disable,
                default_features=request.default_features,
                source="root request",
            ),
        )
        self._run()
        return self._build_result()

    def _add_constraint(self, name: str, req: Requirement, source: str) -> None:
        self.constraints.setdefault(name, []).append((req, source))
        self._ensure_node(name)

    def _ensure_node(self, name: str) -> None:
        reqs = self.constraints[name]
        if not self.registry.known(name):
            sources = "; ".join(f"{req} required by {src}" for req, src in reqs)
            raise ResolutionError(f"unknown package {name!r}: {sources}")
        package = self.registry.find(name, [req for req, _ in reqs])
        if package is None:
            sources = "; ".join(f"{req} required by {src}" for req, src in reqs)
            available = ", ".join(str(p.version) for p in self.registry.packages[name])
            raise ResolutionError(
                f"no version of {name!r} satisfies all constraints ({sources});"
                f" available: {available}"
            )
        node = self.nodes.get(name)
        if node is None:
            self.nodes[name] = _Node(package)
        elif node.package is not package:
            rebuilt = _Node(package)
            rebuilt.seeds = node.seeds
            self.nodes[name] = rebuilt
            for seed in rebuilt.seeds:
                self._apply_seed(name, seed, record=False)

    def _apply_seed(self, name: str, seed: _Seed, record: bool = True) -> None:
        node = self.nodes[name]
        if record:
            node.seeds.append(seed)
        if seed.default_features:
            node.default_features = True
        for feat in seed.disable:
            self._check_known(node, feat, seed.source)
            node.disabled.setdefault(feat, seed.source)
        for feat in seed.features:
            self._check_known(node, feat, seed.source)
            node.forced.setdefault(
                feat, Reason("forced", f"feature {feat!r} required by {seed.source}")
            )
        for feat in seed.disable:
            if feat in node.forced:
                raise ConflictError(self._conflict_message(node, feat, node.forced[feat]))
            if feat in node.active:
                raise ConflictError(self._conflict_message(node, feat, node.active[feat]))

    @staticmethod
    def _check_known(node: _Node, feat: str, source: str) -> None:
        if feat not in node.package.features:
            raise ResolutionError(
                f"{source}: unknown feature {feat!r} of package"
                f" {node.package.name!r} {node.package.version}"
            )

    def _conflict_message(self, node: _Node, feat: str, reason: Reason) -> str:
        lines = [
            f"feature conflict in package {node.package.name!r} {node.package.version}:",
            f"  feature {feat!r} is explicitly disabled by {node.disabled[feat]}",
            "  but it is transitively required:",
        ]
        lines.extend(f"    - {step}" for step in reason.chain())
        return "\n".join(lines)

    def _run(self) -> None:
        for _ in range(_MAX_ROUNDS):
            changed = False
            for name in list(self.nodes):
                changed |= self._propagate(self.nodes[name])
            for name in list(self.nodes):
                changed |= self._activate_dependencies(self.nodes[name])
            if not changed:
                return
        raise ResolutionError("resolution did not converge")

    def _activate(self, node: _Node, feat: str, reason: Reason) -> bool:
        if feat in node.disabled:
            raise ConflictError(self._conflict_message(node, feat, reason))
        if feat not in node.active:
            node.active[feat] = reason
            return True
        return False

    def _propagate(self, node: _Node) -> bool:
        changed = False
        for feat, reason in node.forced.items():
            changed |= self._activate(node, feat, reason)
        if node.default_features and "default" in node.package.features:
            changed |= self._activate(
                node,
                "default",
                Reason("default", f"default feature of package {node.package.name!r}"),
            )
        queue = list(node.active)
        index = 0
        while index < len(queue):
            feat = queue[index]
            index += 1
            for implied in node.package.features.get(feat, ()):
                reason = Reason(
                    "implied",
                    f"feature {implied!r} implied by {feat!r} of package"
                    f" {node.package.name!r}",
                    node.active[feat],
                )
                if self._activate(node, implied, reason):
                    queue.append(implied)
                    changed = True
        return changed

    def _dependency_active(self, node: _Node, dep: Dependency) -> bool:
        if not dep.optional:
            return True
        if dep.when is not None:
            return expr.evaluate(dep.when, lambda name: name in node.active)
        return dep.name in node.active

    def _activate_dependencies(self, node: _Node) -> bool:
        changed = False
        for position, dep in enumerate(node.package.dependencies):
            if position in node.deps_done:
                continue
            if not self._dependency_active(node, dep):
                continue
            node.deps_done.add(position)
            source = self._dependency_source(node, dep)
            self._add_constraint(dep.name, dep.req, source)
            self._apply_seed(
                dep.name,
                _Seed(dep.features, dep.disable, dep.default_features, source),
            )
            changed = True
        return changed

    @staticmethod
    def _dependency_source(node: _Node, dep: Dependency) -> str:
        base = f"dependency {dep.name!r} of package {node.package.name!r}"
        if dep.when_text:
            return f"{base} (enabled when {dep.when_text!r})"
        if dep.optional:
            return f"{base} (enabled by feature {dep.name!r})"
        return f"{base} (unconditional)"

    def _build_result(self) -> Resolution:
        packages = []
        for name in sorted(self.nodes):
            node = self.nodes[name]
            forced = sorted(f for f, r in node.active.items() if r.kind == "forced")
            defaulted = sorted(f for f, r in node.active.items() if r.kind == "default")
            implied = sorted(f for f, r in node.active.items() if r.kind == "implied")
            packages.append(
                ResolvedPackage(
                    name=name,
                    version=str(node.package.version),
                    features=tuple(sorted(node.active)),
                    forced=tuple(forced),
                    defaulted=tuple(defaulted),
                    implied=tuple(implied),
                    disabled=tuple(sorted(node.disabled)),
                )
            )
        return Resolution(
            root=self.root_name,
            packages=tuple(packages),
            diagnostics=tuple(self._detect_cycles()),
        )

    def _cycle_graph(self) -> tuple[dict, set]:
        graph: dict[tuple, set] = {}

        def add(source: tuple, target: tuple) -> None:
            graph.setdefault(source, set()).add(target)
            graph.setdefault(target, set())

        for name, node in self.nodes.items():
            package_key = ("package", name)
            graph.setdefault(package_key, set())
            if node.default_features and "default" in node.package.features:
                add(package_key, ("feature", name, "default"))
            for feat, implied in node.package.features.items():
                feature_key = ("feature", name, feat)
                graph.setdefault(feature_key, set())
                for target in implied:
                    add(feature_key, ("feature", name, target))
            for dep in node.package.dependencies:
                targets = [("package", dep.name)] + [
                    ("feature", dep.name, feat) for feat in dep.features
                ]
                if not dep.optional:
                    for target in targets:
                        add(package_key, target)
                else:
                    for trigger in dep.trigger_features():
                        for target in targets:
                            add(("feature", name, trigger), target)
        seeds = {("package", self.root_name)}
        root = self.nodes.get(self.root_name)
        if root is not None:
            for feat in root.forced:
                seeds.add(("feature", self.root_name, feat))
        return graph, seeds

    def _detect_cycles(self) -> list[Diagnostic]:
        graph, seeds = self._cycle_graph()
        grounded = _reachable(graph, seeds)
        diagnostics = []
        for component in _strongly_connected_components(graph):
            cyclic = len(component) > 1 or (
                len(component) == 1
                and component[0] in graph.get(component[0], ())
            )
            if not cyclic:
                continue
            labels = tuple(sorted(_label(key) for key in component))
            if any(key in grounded for key in component):
                diagnostics.append(
                    Diagnostic(
                        level="info",
                        kind="feature-cycle",
                        message="cyclic derivation grounded by a root seed: "
                        + " -> ".join(labels),
                        cycle=labels,
                    )
                )
            else:
                diagnostics.append(
                    Diagnostic(
                        level="warning",
                        kind="feature-cycle",
                        message="self-supporting cycle without a root seed;"
                        " it cannot activate: " + " -> ".join(labels),
                        cycle=labels,
                    )
                )
        diagnostics.sort(key=lambda diag: (diag.level != "warning", diag.cycle))
        return diagnostics
