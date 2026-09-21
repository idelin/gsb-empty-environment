"""Feature/dependency closure resolution.

Semantics:
- A feature becomes active only when seeded by a root request, a default
  feature set, or an implication from an already-active feature (least fixed
  point). Purely self-supporting cycles with no external seed stay inactive.
- Explicitly disabled features can never be activated; a transitive
  requirement on a disabled feature is a conflict reported with its source
  chain.
- Repeated requests for the same feature are idempotent (activations live in
  sets and the fixed point is order independent).
"""

from __future__ import annotations

from collections import defaultdict

from .expr import evaluate
from .model import RootRequest
from .version import VersionReq


class ResolutionError(ValueError):
    pass


class Source:
    __slots__ = ("kind", "pkg", "feature")

    def __init__(self, kind, pkg=None, feature=None):
        self.kind = kind  # 'root' | 'default' | 'feature' | 'dep'
        self.pkg = pkg
        self.feature = feature


class Instance:
    def __init__(self, pkg):
        self.pkg = pkg
        self.key = pkg.key
        self.active = set()
        self.disabled = set()
        self.defaults_applied = set()
        self.sources = defaultdict(list)         # feature -> [Source]
        self.disable_sources = defaultdict(list)  # feature -> [Source]


class Resolution:
    def __init__(self, instances, conflicts, cycles):
        self.instances = instances
        self.conflicts = conflicts
        self.cycles = cycles

    @property
    def ok(self):
        return not self.conflicts

    def to_dict(self):
        return {
            "ok": self.ok,
            "packages": [
                {
                    "name": inst.pkg.name,
                    "version": str(inst.pkg.version),
                    "features": sorted(inst.active),
                    "disabled": sorted(inst.disabled),
                    "default_features_applied": sorted(inst.defaults_applied),
                }
                for inst in sorted(
                    self.instances.values(), key=lambda i: (i.pkg.name, i.pkg.version)
                )
            ],
            "cycles": self.cycles,
            "conflicts": self.conflicts,
        }


class Resolver:
    def __init__(self, packages):
        self._by_name = defaultdict(list)
        for p in packages:
            self._by_name[p.name].append(p)
        for lst in self._by_name.values():
            lst.sort(key=lambda p: p.version)

    def select(self, name, req):
        """Pick the highest locally available version satisfying the requirement."""
        if isinstance(req, str):
            req = VersionReq.parse(req)
        candidates = [p for p in self._by_name.get(name, []) if req.matches(p.version)]
        if not candidates:
            raise ResolutionError(
                f"no local package {name!r} matches requirement {req!s}"
            )
        return candidates[-1]

    def resolve(self, roots):
        instances = {}
        conflicts = []
        edges = set()  # (src_node, dst_node), nodes are "name@version:feature"

        def get_instance(pkgdef):
            inst = instances.get(pkgdef.key)
            if inst is None:
                inst = Instance(pkgdef)
                instances[pkgdef.key] = inst
            return inst

        def activate(inst, feat, source):
            if feat not in inst.pkg.features:
                raise ResolutionError(
                    f"{inst.key}: request for unknown feature {feat!r}"
                )
            if feat in inst.disabled:
                inst.sources[feat].append(source)  # keep provenance for the chain
                conflicts.append(_conflict_msg(instances, inst, feat))
                return False
            if feat not in inst.active:
                inst.active.add(feat)
                inst.sources[feat].append(source)
                return True
            inst.sources[feat].append(source)
            return False

        def apply_disable(inst, feat, source):
            if feat not in inst.pkg.features:
                raise ResolutionError(
                    f"{inst.key}: request to disable unknown feature {feat!r}"
                )
            if feat not in inst.disabled:
                inst.disabled.add(feat)
            inst.disable_sources[feat].append(source)
            if feat in inst.active:
                conflicts.append(_conflict_msg(instances, inst, feat))

        def apply_defaults(inst, src_node):
            changed = False
            for f in inst.pkg.default_features:
                if f in inst.disabled:
                    continue  # explicit disable of a default feature is legitimate
                inst.defaults_applied.add(f)
                if activate(inst, f, Source("default")):
                    changed = True
                if src_node is not None:
                    edges.add((src_node, f"{inst.key}:{f}"))
            return changed

        def apply_dep(src_inst, src_feat, dep):
            pkgdef = self.select(dep.package, dep.req)
            inst = get_instance(pkgdef)
            src_node = f"{src_inst.key}:{src_feat}"
            changed = False
            if dep.default_features:
                if apply_defaults(inst, src_node):
                    changed = True
            for f in dep.features:
                if activate(inst, f, Source("dep", src_inst.key, src_feat)):
                    changed = True
                edges.add((src_node, f"{inst.key}:{f}"))
            for f in dep.disables:
                apply_disable(inst, f, Source("dep", src_inst.key, src_feat))
            return changed

        # Seed from root requests.
        for root in roots:
            if not isinstance(root, RootRequest):
                root = RootRequest.from_json(root)
            pkgdef = self.select(root.package, root.req)
            inst = get_instance(pkgdef)
            for f in root.disable:
                apply_disable(inst, f, Source("root"))
            if root.default_features:
                apply_defaults(inst, None)
            for f in root.enable:
                activate(inst, f, Source("root"))

        # Least fixed point: propagate implications until nothing changes.
        changed = True
        while changed:
            changed = False
            for inst in list(instances.values()):
                for feat in sorted(inst.active):
                    fdef = inst.pkg.features.get(feat)
                    if fdef is None:
                        continue
                    node = f"{inst.key}:{feat}"
                    for target in fdef.enables:
                        if activate(inst, target, Source("feature", inst.key, feat)):
                            changed = True
                        edges.add((node, f"{inst.key}:{target}"))
                    for cond in fdef.conditionals:
                        if evaluate(cond.ast, inst.active):
                            for target in cond.enables:
                                if activate(
                                    inst, target, Source("feature", inst.key, feat)
                                ):
                                    changed = True
                                edges.add((node, f"{inst.key}:{target}"))
                            for dep in cond.deps:
                                if apply_dep(inst, feat, dep):
                                    changed = True
                    for dep in fdef.deps:
                        if apply_dep(inst, feat, dep):
                            changed = True

        # Late-arriving disables may clash with already-active features.
        for inst in instances.values():
            for feat in sorted(inst.active & inst.disabled):
                msg = _conflict_msg(instances, inst, feat)
                if msg not in conflicts:
                    conflicts.append(msg)

        cycles = _find_cycles(instances, edges)
        return Resolution(instances, conflicts, cycles)


def _describe_disable_source(inst, feat):
    parts = []
    for s in inst.disable_sources.get(feat, []):
        if s.kind == "root":
            parts.append("root request")
        else:
            parts.append(f"dependency requested by {s.pkg}:{s.feature}")
    return " and ".join(parts) if parts else "unknown source"


def _activation_chain(instances, inst, feat):
    chain = [f"{inst.key}:{feat}"]
    seen = {(inst.key, feat)}
    cur_inst, cur_feat = inst, feat
    while True:
        srcs = cur_inst.sources.get(cur_feat)
        if not srcs:
            break
        s = srcs[0]
        if s.kind == "root":
            chain.append("root request")
            break
        if s.kind == "default":
            chain.append(f"default features of {cur_inst.key}")
            break
        chain.append(f"{s.pkg}:{s.feature}")
        if (s.pkg, s.feature) in seen:
            chain.append("(cycle)")
            break
        seen.add((s.pkg, s.feature))
        cur_inst = instances[s.pkg]
        cur_feat = s.feature
    return " <- ".join(chain)


def _conflict_msg(instances, inst, feat):
    return (
        f"conflict: {inst.key}:{feat} is explicitly disabled by "
        f"{_describe_disable_source(inst, feat)}, but required via "
        f"{_activation_chain(instances, inst, feat)}"
    )


def _find_cycles(instances, edges):
    """Tarjan SCC over the active-feature implication graph."""
    nodes = set()
    for inst in instances.values():
        for feat in inst.active:
            nodes.add(f"{inst.key}:{feat}")
    adj = defaultdict(set)
    for a, b in edges:
        if a in nodes and b in nodes:
            adj[a].add(b)

    index_of = {}
    lowlink = {}
    on_stack = set()
    stack = []
    counter = [0]
    cycles = []

    def strongconnect(v):
        # Iterative Tarjan to avoid recursion limits.
        work = [(v, iter(sorted(adj[v])))]
        index_of[v] = lowlink[v] = counter[0]
        counter[0] += 1
        stack.append(v)
        on_stack.add(v)
        while work:
            node, it = work[-1]
            advanced = False
            for w in it:
                if w not in index_of:
                    index_of[w] = lowlink[w] = counter[0]
                    counter[0] += 1
                    stack.append(w)
                    on_stack.add(w)
                    work.append((w, iter(sorted(adj[w]))))
                    advanced = True
                    break
                elif w in on_stack:
                    lowlink[node] = min(lowlink[node], index_of[w])
            if advanced:
                continue
            work.pop()
            if work:
                parent = work[-1][0]
                lowlink[parent] = min(lowlink[parent], lowlink[node])
            if lowlink[node] == index_of[node]:
                scc = []
                while True:
                    w = stack.pop()
                    on_stack.discard(w)
                    scc.append(w)
                    if w == node:
                        break
                if len(scc) > 1 or (len(scc) == 1 and scc[0] in adj[scc[0]]):
                    cycles.append(sorted(scc))

    for v in sorted(nodes):
        if v not in index_of:
            strongconnect(v)
    cycles.sort()
    return cycles
