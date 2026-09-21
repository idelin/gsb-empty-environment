"""Compatibility diffing and call-graph propagation.

Rules implemented here:

* Visibility: only ``public`` symbols take part in the public compatibility
  verdict. Changes to ``private`` symbols are recorded as ignored.
* Input position (parameters) is contravariant: *widening* a parameter type
  is compatible, *narrowing* it (or moving to an unrelated type) breaks
  callers.
* Output position (return type) is covariant: *narrowing* the return type is
  compatible, *widening* it (or moving to an unrelated type) breaks callers.
* Behavior markers act as guarantees: removing one is breaking, adding one is
  a compatible enhancement.
* A removed public symbol only impacts a root when a call chain from that
  root actually reaches it.
"""
from __future__ import annotations

from dataclasses import dataclass, field

from .semver import Version, bump_level, bump_satisfies

INPUT = "input"
OUTPUT = "output"


# ---------------------------------------------------------------------------
# Model
# ---------------------------------------------------------------------------

class TypeEnv:
    """Type hierarchy used to decide widening vs narrowing.

    ``hierarchy`` maps a type name to the iterable of its direct supertypes,
    e.g. ``{"int": ["num"], "num": ["object"]}``.
    """

    def __init__(self, hierarchy: dict | None = None):
        self._supers = {name: set(parents) for name, parents in (hierarchy or {}).items()}

    def is_subtype(self, sub: str, sup: str) -> bool:
        """True when ``sub`` is a (transitive) subtype of ``sup``."""
        if sub == sup:
            return True
        seen, stack = set(), [sub]
        while stack:
            current = stack.pop()
            for parent in self._supers.get(current, ()):
                if parent == sup:
                    return True
                if parent not in seen:
                    seen.add(parent)
                    stack.append(parent)
        return False

    def relation(self, old: str, new: str) -> str:
        """Relation of ``new`` to ``old``: same / widened / narrowed / unrelated."""
        if old == new:
            return "same"
        if self.is_subtype(old, new):
            return "widened"
        if self.is_subtype(new, old):
            return "narrowed"
        return "unrelated"


@dataclass(frozen=True)
class Param:
    name: str
    type: str
    optional: bool = False

    @classmethod
    def from_dict(cls, data: dict) -> "Param":
        return cls(name=data["name"], type=data["type"], optional=bool(data.get("optional", False)))


@dataclass(frozen=True)
class Symbol:
    name: str
    visibility: str = "public"
    params: tuple = ()
    returns: str | None = None
    behaviors: frozenset = frozenset()

    @classmethod
    def from_dict(cls, data: dict) -> "Symbol":
        return cls(
            name=data["name"],
            visibility=data.get("visibility", "public"),
            params=tuple(Param.from_dict(p) for p in data.get("params", [])),
            returns=data.get("returns"),
            behaviors=frozenset(data.get("behaviors", [])),
        )

    @property
    def is_public(self) -> bool:
        return self.visibility == "public"


@dataclass(frozen=True)
class Edge:
    caller: str
    callee: str
    site: str | None = None

    @classmethod
    def from_raw(cls, raw) -> "Edge":
        if isinstance(raw, (list, tuple)):
            if len(raw) == 2:
                return cls(caller=raw[0], callee=raw[1])
            if len(raw) == 3:
                return cls(caller=raw[0], callee=raw[1], site=raw[2])
            raise ValueError(f"edge list must have 2 or 3 items: {raw!r}")
        return cls(caller=raw["caller"], callee=raw["callee"], site=raw.get("site"))


@dataclass(frozen=True)
class Change:
    symbol: str
    kind: str
    breaking: bool
    detail: str
    position: str | None = None

    def to_dict(self) -> dict:
        return {
            "symbol": self.symbol,
            "kind": self.kind,
            "breaking": self.breaking,
            "position": self.position,
            "detail": self.detail,
        }


# ---------------------------------------------------------------------------
# Symbol diffing
# ---------------------------------------------------------------------------

def _diff_params(old: Symbol, new: Symbol, tenv: TypeEnv) -> list:
    changes = []
    shared = min(len(old.params), len(new.params))
    for index in range(shared):
        old_param, new_param = old.params[index], new.params[index]
        if old_param.type == new_param.type:
            continue
        relation = tenv.relation(old_param.type, new_param.type)
        # Input position: contravariant. Widening is compatible.
        breaking = relation != "widened"
        changes.append(Change(
            symbol=old.name,
            kind="param_type",
            breaking=breaking,
            position=INPUT,
            detail=(
                f"parameter {new_param.name!r} type {relation}: "
                f"{old_param.type} -> {new_param.type} "
                f"(input position: {'compatible widening' if not breaking else 'breaking'})"
            ),
        ))
    for param in old.params[shared:]:
        changes.append(Change(
            symbol=old.name,
            kind="param_removed",
            breaking=True,
            position=INPUT,
            detail=f"parameter {param.name!r} ({param.type}) removed",
        ))
    for param in new.params[shared:]:
        breaking = not param.optional
        changes.append(Change(
            symbol=old.name,
            kind="param_added",
            breaking=breaking,
            position=INPUT,
            detail=(
                f"parameter {param.name!r} ({param.type}) added"
                + ("" if breaking else " as optional")
            ),
        ))
    return changes


def _diff_returns(old: Symbol, new: Symbol, tenv: TypeEnv) -> list:
    if old.returns is None or new.returns is None or old.returns == new.returns:
        return []
    relation = tenv.relation(old.returns, new.returns)
    # Output position: covariant. Narrowing is compatible.
    breaking = relation != "narrowed"
    return [Change(
        symbol=old.name,
        kind="return_type",
        breaking=breaking,
        position=OUTPUT,
        detail=(
            f"return type {relation}: {old.returns} -> {new.returns} "
            f"(output position: {'compatible narrowing' if not breaking else 'breaking'})"
        ),
    )]


def _diff_behaviors(old: Symbol, new: Symbol) -> list:
    changes = []
    for marker in sorted(old.behaviors - new.behaviors):
        changes.append(Change(
            symbol=old.name,
            kind="behavior_removed",
            breaking=True,
            detail=f"behavior guarantee {marker!r} removed",
        ))
    for marker in sorted(new.behaviors - old.behaviors):
        changes.append(Change(
            symbol=old.name,
            kind="behavior_added",
            breaking=False,
            detail=f"behavior guarantee {marker!r} added",
        ))
    return changes


def diff_symbols(old: dict, new: dict, tenv: TypeEnv) -> tuple:
    """Diff two ``{name: Symbol}`` maps.

    Returns ``(public_changes, private_changes)``. Private symbols never
    contribute to the public compatibility verdict.
    """
    public_changes, private_changes = [], []
    for name in sorted(set(old) | set(new)):
        old_sym, new_sym = old.get(name), new.get(name)
        target = public_changes if (old_sym or new_sym).is_public else private_changes
        if old_sym is None:
            target.append(Change(name, "added", False, "symbol added"))
            continue
        if new_sym is None:
            target.append(Change(name, "removed", True, "symbol removed"))
            continue
        if old_sym.visibility != new_sym.visibility:
            if new_sym.visibility == "private":
                public_changes.append(Change(name, "removed", True, "symbol made private"))
                continue
            public_changes.append(Change(name, "added", False, "symbol made public"))
        target.extend(_diff_params(old_sym, new_sym, tenv))
        target.extend(_diff_returns(old_sym, new_sym, tenv))
        target.extend(_diff_behaviors(old_sym, new_sym))
    return public_changes, private_changes


# ---------------------------------------------------------------------------
# Propagation along call edges
# ---------------------------------------------------------------------------

@dataclass
class BreakingPath:
    root: str
    nodes: list          # root-first, ends at the breaking symbol
    changes: list        # Change objects applying to the final symbol
    sites: list          # call-site labels along the path

    def to_dict(self) -> dict:
        return {
            "root": self.root,
            "path": list(self.nodes),
            "changes": [c.to_dict() for c in self.changes],
            "call_sites": list(self.sites),
        }


def propagate(breaking: dict, edges: list, roots: list) -> list:
    """Propagate breaking changes backwards along call edges to the roots.

    ``breaking`` maps a symbol name to its list of breaking Change objects.
    Returns one BreakingPath per (root, breaking symbol, chain) combination.
    Symbols with no chain to any root produce no paths, which is exactly why
    an uncalled removed symbol does not affect the root.
    """
    callers = {}
    for edge in edges:
        callers.setdefault(edge.callee, []).append(edge)
    root_set = set(roots)
    paths = []
    for symbol in sorted(breaking):
        # Stack entries: (node, nodes-from-symbol, edges-from-symbol)
        stack = [(symbol, [symbol], [])]
        while stack:
            node, node_path, edge_path = stack.pop()
            if node in root_set:
                paths.append(BreakingPath(
                    root=node,
                    nodes=list(reversed(node_path)),
                    changes=list(breaking[symbol]),
                    sites=[e.site for e in reversed(edge_path) if e.site],
                ))
            for edge in callers.get(node, ()):
                if edge.caller in node_path:  # cycle guard
                    continue
                stack.append((edge.caller, node_path + [edge.caller], edge_path + [edge]))
    paths.sort(key=lambda p: (p.root, p.nodes[-1], p.nodes))
    return paths


# ---------------------------------------------------------------------------
# Top-level analysis
# ---------------------------------------------------------------------------

def required_bump(public_changes: list) -> str:
    if any(c.breaking for c in public_changes):
        return "major"
    if public_changes:
        return "minor"
    return "patch"


def check_version(old: Version, new: Version, required: str) -> dict:
    actual = bump_level(old, new)
    messages = []
    if new <= old:
        messages.append(f"version did not increase: {old} -> {new}")
    if new.is_prerelease:
        messages.append(f"{new} is a pre-release; breakage may still land before the stable release")
    effective = "patch" if actual == "prerelease" else actual
    ok = bump_satisfies(effective, required)
    if not ok and required == "major" and old.major == 0 and effective == "minor":
        ok = True
        messages.append("0.x package: minor bump accepted for breaking changes (semver spec item 4)")
    if not ok:
        messages.append(f"breaking/compatible changes require a {required} bump, got {actual}")
    return {"old": str(old), "new": str(new), "actual_bump": actual,
            "required_bump": required, "ok": ok, "messages": messages}


@dataclass
class Report:
    package: str
    version_check: dict
    public_changes: list
    private_changes: list
    breaking_paths: list
    affected_roots: list
    affected_sites: list
    unreachable_breaking: list

    @property
    def package_impact(self) -> str:
        if any(c.breaking for c in self.public_changes):
            return "breaking"
        return "compatible" if self.public_changes else "none"

    @property
    def root_impact(self) -> str:
        return "breaking" if self.breaking_paths else "none"

    def to_dict(self) -> dict:
        return {
            "package": self.package,
            "package_impact": self.package_impact,
            "root_impact": self.root_impact,
            "version": self.version_check,
            "public_changes": [c.to_dict() for c in self.public_changes],
            "private_changes_ignored": [c.to_dict() for c in self.private_changes],
            "affected_roots": self.affected_roots,
            "affected_call_sites": self.affected_sites,
            "breaking_paths": [p.to_dict() for p in self.breaking_paths],
            "unreachable_breaking_changes": [c.to_dict() for c in self.unreachable_breaking],
        }


def analyze(payload: dict) -> Report:
    """Run the full analysis on a parsed JSON payload."""
    tenv = TypeEnv(payload.get("type_hierarchy"))
    old_syms = {s.name: s for s in map(Symbol.from_dict, payload["old"].get("symbols", []))}
    new_syms = {s.name: s for s in map(Symbol.from_dict, payload["new"].get("symbols", []))}
    public_changes, private_changes = diff_symbols(old_syms, new_syms, tenv)

    callgraph = payload.get("callgraph", {})
    edges = [Edge.from_raw(e) for e in callgraph.get("edges", [])]
    roots = list(callgraph.get("roots", []))

    breaking = {}
    for change in public_changes:
        if change.breaking:
            breaking.setdefault(change.symbol, []).append(change)
    paths = propagate(breaking, edges, roots)

    reached = {p.nodes[-1] for p in paths}
    unreachable = [c for sym, cs in sorted(breaking.items()) if sym not in reached for c in cs]
    affected_roots = sorted({p.root for p in paths})
    affected_sites = sorted({site for p in paths for site in p.sites})

    version_check = check_version(
        Version.parse(payload["old_version"]),
        Version.parse(payload["new_version"]),
        required_bump(public_changes),
    )
    return Report(
        package=payload.get("package", "<unnamed>"),
        version_check=version_check,
        public_changes=public_changes,
        private_changes=private_changes,
        breaking_paths=paths,
        affected_roots=affected_roots,
        affected_sites=affected_sites,
        unreachable_breaking=unreachable,
    )
