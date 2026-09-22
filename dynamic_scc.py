"""Incrementally maintained strongly connected component (SCC) index.

The index keeps a directed multigraph (parallel edges and isolated nodes are
allowed) and maintains, per committed graph version:

  * the partition of nodes into strongly connected components, where the
    component ID is the lexicographically smallest member string ID;
  * the condensation graph (DAG of components) with edge multiplicities.

Updates are applied in atomic batches.  Each batch returns a ``BatchDiff``
describing component merges / splits / births / deaths and the condensation
edge delta, so downstream consumers can replay the diff stream instead of
re-querying full state.

Maintenance is change-scoped: for a batch we compute the affected region
``R`` = nodes reachable (in either direction, on the *old* graph) from any
touched endpoint, re-run Tarjan only on the subgraph induced by ``R``, and
keep every component disjoint from ``R`` untouched.  Because a component ID
is derived only from its members, components outside the changed region keep
a stable identity regardless of traversal order or edits elsewhere.

Worst case a single edge flip can affect the whole graph (e.g. one edge in a
long cycle), so a batch costs O(|V_R| + |E_R|) which degrades to a full
O(V + E) recomputation; see README.md for details.
"""

from __future__ import annotations

from collections import Counter
from dataclasses import dataclass, field

ADD_NODE = "add_node"
REMOVE_NODE = "remove_node"
ADD_EDGE = "add_edge"
REMOVE_EDGE = "remove_edge"

_OPS = {ADD_NODE, REMOVE_NODE, ADD_EDGE, REMOVE_EDGE}


@dataclass
class BatchDiff:
    """Self-contained delta produced by one committed batch.

    ``added_components`` / ``removed_components`` (id -> members) are the
    replayable source of truth; ``merges`` / ``splits`` / ``formed`` /
    ``dissolved`` are derived views.  Condensation deltas map an edge
    ``(src_comp, dst_comp)`` to its new (added / count_changed) or old
    (removed / count_changed) multiplicity.
    """

    version: int
    added_components: dict = field(default_factory=dict)
    removed_components: dict = field(default_factory=dict)
    merges: list = field(default_factory=list)          # (new_id, (old_id, ...))
    splits: list = field(default_factory=list)          # (old_id, (new_id, ...))
    formed: dict = field(default_factory=dict)          # id -> members (all-new nodes)
    dissolved: dict = field(default_factory=dict)       # id -> members (all removed)
    cond_edges_added: dict = field(default_factory=dict)
    cond_edges_removed: dict = field(default_factory=dict)
    cond_edges_count_changed: dict = field(default_factory=dict)


@dataclass
class _Snapshot:
    components: dict
    node_to_comp: dict
    cond_edges: dict


def _tarjan(nodes, succ_of):
    """Iterative Tarjan SCC.  ``succ_of(u)`` yields successors of ``u``.

    Returns a list of components, each a list of nodes.  Iteration order of
    ``nodes`` / successors is caller-controlled; pass sorted iterables for
    deterministic output.
    """
    index = {}
    lowlink = {}
    on_stack = set()
    stack = []
    components = []
    counter = 0
    for root in nodes:
        if root in index:
            continue
        index[root] = lowlink[root] = counter
        counter += 1
        stack.append(root)
        on_stack.add(root)
        work = [(root, iter(succ_of(root)))]
        while work:
            node, it = work[-1]
            descended = False
            for nxt in it:
                if nxt not in index:
                    index[nxt] = lowlink[nxt] = counter
                    counter += 1
                    stack.append(nxt)
                    on_stack.add(nxt)
                    work.append((nxt, iter(succ_of(nxt))))
                    descended = True
                    break
                if nxt in on_stack:
                    lowlink[node] = min(lowlink[node], index[nxt])
            if descended:
                continue
            work.pop()
            if work:
                parent = work[-1][0]
                lowlink[parent] = min(lowlink[parent], lowlink[node])
            if lowlink[node] == index[node]:
                comp = []
                while True:
                    member = stack.pop()
                    on_stack.discard(member)
                    comp.append(member)
                    if member == node:
                        break
                components.append(comp)
    return components


class DynamicSCC:
    """Versioned, incrementally maintained SCC index over a string graph."""

    def __init__(self):
        self._nodes = set()
        self._out = {}  # u -> {v: multiplicity}
        self._in = {}   # v -> {u: multiplicity}
        self._components = {}     # comp_id -> frozenset(members)
        self._node_to_comp = {}   # node -> comp_id
        self._cond = {}           # (src_comp, dst_comp) -> multiplicity
        self._version = 0
        self._history = {0: self._snapshot()}

    # ------------------------------------------------------------------ #
    # queries
    # ------------------------------------------------------------------ #
    @property
    def version(self):
        return self._version

    def _snapshot(self):
        return _Snapshot(dict(self._components), dict(self._node_to_comp),
                         dict(self._cond))

    def _snap(self, version):
        if version is None:
            version = self._version
        if version not in self._history:
            raise KeyError(f"unknown graph version: {version}")
        return self._history[version]

    def components(self, version=None):
        """comp_id -> frozenset(members) at the given (pinned) version."""
        return dict(self._snap(version).components)

    def condensation_edges(self, version=None):
        """(src_comp, dst_comp) -> multiplicity at the given version."""
        return dict(self._snap(version).cond_edges)

    def component_of(self, node, version=None):
        """Component ID containing ``node`` at the given version, or None."""
        return self._snap(version).node_to_comp.get(node)

    # ------------------------------------------------------------------ #
    # batch update
    # ------------------------------------------------------------------ #
    def apply_batch(self, ops):
        """Validate then atomically apply a batch of ops; return BatchDiff.

        Ops are tuples: ("add_node", n), ("remove_node", n),
        ("add_edge", u, v), ("remove_edge", u, v).  ``add_edge`` auto-creates
        missing endpoints.  ``remove_node`` also removes all incident edges.
        Any invalid op aborts the whole batch with no side effects.
        """
        ops = [tuple(op) for op in ops]
        self._validate(ops)

        changed = set()
        for op in ops:
            if op[0] in (ADD_EDGE, REMOVE_EDGE):
                changed.add(op[1])
                changed.add(op[2])
            else:
                changed.add(op[1])

        # Affected region on the OLD graph, before mutating anything.
        region = self._reach(changed)
        old_region_comps = {}
        for node in region:
            cid = self._node_to_comp.get(node)
            if cid is not None and cid not in old_region_comps:
                old_region_comps[cid] = self._components[cid]
        old_cond = self._cond

        self._apply_ops(ops)

        # Recompute SCCs only inside the region.
        alive = sorted(n for n in region if n in self._nodes)
        region_set = set(alive)
        new_region_comps = {}
        for members in _tarjan(alive, lambda u: sorted(
                v for v in self._out.get(u, ()) if v in region_set)):
            fs = frozenset(members)
            new_region_comps[min(fs)] = fs

        # Components identical before/after are not reported as changed.
        added = dict(new_region_comps)
        removed = dict(old_region_comps)
        for cid in list(added):
            if cid in removed and removed[cid] == added[cid]:
                del added[cid]
                del removed[cid]

        # Assemble new global state; components outside the region keep
        # their identity (and hence their ID) untouched.
        old_region_ids = set(old_region_comps)
        self._components = {cid: m for cid, m in self._components.items()
                            if cid not in old_region_ids}
        self._components.update(new_region_comps)
        self._node_to_comp = {
            n: cid for n, cid in self._node_to_comp.items()
            if cid not in old_region_ids and n in self._nodes}
        for cid, members in new_region_comps.items():
            for n in members:
                self._node_to_comp[n] = cid

        self._cond, region_cond = self._rebuild_condensation(
            old_cond, old_region_ids, region_set)

        diff = BatchDiff(version=self._version + 1)
        diff.added_components = added
        diff.removed_components = removed
        self._classify_events(diff)
        self._condensation_delta(diff, old_cond, self._cond,
                                 old_region_ids, region_cond)

        self._version += 1
        self._history[self._version] = self._snapshot()
        return diff

    # ------------------------------------------------------------------ #
    # internals
    # ------------------------------------------------------------------ #
    def _validate(self, ops):
        added_nodes = set()
        removed_nodes = set()
        edge_delta = Counter()

        def live(n):
            return (n in self._nodes or n in added_nodes) \
                and n not in removed_nodes

        for op in ops:
            if not op or op[0] not in _OPS:
                raise ValueError(f"unknown op: {op!r}")
            kind = op[0]
            if kind in (ADD_NODE, REMOVE_NODE):
                if len(op) != 2:
                    raise ValueError(f"bad arity: {op!r}")
                node = op[1]
                if kind == ADD_NODE:
                    if live(node):
                        raise ValueError(f"node already exists: {node!r}")
                    added_nodes.add(node)
                    removed_nodes.discard(node)
                else:
                    if not live(node):
                        raise ValueError(f"no such node: {node!r}")
                    removed_nodes.add(node)
                    added_nodes.discard(node)
            else:
                if len(op) != 3:
                    raise ValueError(f"bad arity: {op!r}")
                u, v = op[1], op[2]
                if kind == ADD_EDGE:
                    for x in (u, v):
                        if not live(x):
                            added_nodes.add(x)
                            removed_nodes.discard(x)
                    edge_delta[(u, v)] += 1
                else:
                    if u in removed_nodes or v in removed_nodes:
                        raise ValueError(
                            f"endpoint removed earlier in batch: {op!r}")
                    current = self._out.get(u, {}).get(v, 0)
                    if current + edge_delta[(u, v)] <= 0:
                        raise ValueError(f"no such edge: {u!r} -> {v!r}")
                    edge_delta[(u, v)] -= 1

    def _apply_ops(self, ops):
        for op in ops:
            kind = op[0]
            if kind == ADD_NODE:
                self._nodes.add(op[1])
            elif kind == REMOVE_NODE:
                self._remove_node(op[1])
            elif kind == ADD_EDGE:
                self._add_edge(op[1], op[2])
            else:
                self._remove_edge(op[1], op[2])

    def _add_edge(self, u, v):
        self._nodes.add(u)
        self._nodes.add(v)
        self._out.setdefault(u, Counter())[v] += 1
        self._in.setdefault(v, Counter())[u] += 1

    def _remove_edge(self, u, v):
        out_u = self._out[u]
        out_u[v] -= 1
        if out_u[v] == 0:
            del out_u[v]
        in_v = self._in[v]
        in_v[u] -= 1
        if in_v[u] == 0:
            del in_v[u]

    def _remove_node(self, n):
        for v in list(self._out.get(n, ())):
            del self._in[v][n]
        for u in list(self._in.get(n, ())):
            del self._out[u][n]
        self._out.pop(n, None)
        self._in.pop(n, None)
        self._nodes.discard(n)

    def _reach(self, seeds):
        """Nodes reachable from ``seeds`` in either direction (old graph)."""
        region = set(seeds)
        for adj in (self._out, self._in):
            seen = {s for s in seeds if s in self._nodes}
            stack = list(seen)
            while stack:
                u = stack.pop()
                for w in adj.get(u, ()):
                    if w not in seen:
                        seen.add(w)
                        stack.append(w)
            region |= seen
        return region

    def _rebuild_condensation(self, old_cond, old_region_ids, region_set):
        """Keep edges between untouched components; rescan edges of region
        nodes to rebuild every condensation edge incident to the region."""
        kept = {k: c for k, c in old_cond.items()
                if k[0] not in old_region_ids and k[1] not in old_region_ids}
        region_cond = Counter()
        for u in region_set:
            cu = self._node_to_comp[u]
            for v, c in self._out.get(u, {}).items():
                cv = self._node_to_comp[v]
                if cu != cv:
                    region_cond[(cu, cv)] += c
            for w, c in self._in.get(u, {}).items():
                if w in region_set:
                    continue  # already counted via w's out-scan
                cw = self._node_to_comp[w]
                if cw != cu:
                    region_cond[(cw, cu)] += c
        new_cond = dict(kept)
        for k, c in region_cond.items():
            new_cond[k] = new_cond.get(k, 0) + c
        return new_cond, region_cond

    def _classify_events(self, diff):
        member_old = {}
        for cid, members in diff.removed_components.items():
            for m in members:
                member_old[m] = cid
        member_new = {}
        for cid, members in diff.added_components.items():
            for m in members:
                member_new[m] = cid
        for new_id, members in sorted(diff.added_components.items()):
            olds = tuple(sorted({member_old[m] for m in members
                                 if m in member_old}))
            if len(olds) >= 2:
                diff.merges.append((new_id, olds))
            elif not olds:
                diff.formed[new_id] = members
        for old_id, members in sorted(diff.removed_components.items()):
            news = tuple(sorted({member_new[m] for m in members
                                 if m in member_new}))
            if len(news) >= 2:
                diff.splits.append((old_id, news))
            elif not news:
                diff.dissolved[old_id] = members

    def _condensation_delta(self, diff, old_cond, new_cond,
                            old_region_ids, region_cond):
        changed_keys = {k for k in old_cond
                        if k[0] in old_region_ids or k[1] in old_region_ids}
        changed_keys |= set(region_cond)
        for key in sorted(changed_keys):
            old_c = old_cond.get(key, 0)
            new_c = new_cond.get(key, 0)
            if old_c == new_c:
                continue
            if old_c == 0:
                diff.cond_edges_added[key] = new_c
            elif new_c == 0:
                diff.cond_edges_removed[key] = old_c
            else:
                diff.cond_edges_count_changed[key] = (old_c, new_c)


def reference_index(nodes, edges):
    """Full-recomputation reference (Tarjan over the whole graph).

    ``nodes`` is an iterable of node IDs, ``edges`` an iterable of (u, v)
    pairs (duplicates = parallel edges).  Returns
    ``(components, condensation_edges)`` in the same shape as DynamicSCC.
    """
    node_set = set(nodes)
    out = {}
    for u, v in edges:
        node_set.add(u)
        node_set.add(v)
        out.setdefault(u, Counter())[v] += 1
    components = {}
    node_to_comp = {}
    for members in _tarjan(sorted(node_set),
                           lambda u: sorted(out.get(u, ()))):
        fs = frozenset(members)
        cid = min(fs)
        components[cid] = fs
        for m in members:
            node_to_comp[m] = cid
    cond = Counter()
    for u in node_set:
        for v, c in out.get(u, {}).items():
            cu, cv = node_to_comp[u], node_to_comp[v]
            if cu != cv:
                cond[(cu, cv)] += c
    return components, dict(cond)


def apply_diff(components, cond_edges, diff):
    """Replay a BatchDiff onto mutable (components, cond_edges) dicts."""
    for cid in diff.removed_components:
        components.pop(cid, None)
    components.update(diff.added_components)
    for key in diff.cond_edges_removed:
        cond_edges.pop(key, None)
    for key, (_, new_c) in diff.cond_edges_count_changed.items():
        cond_edges[key] = new_c
    for key, c in diff.cond_edges_added.items():
        cond_edges[key] = c
