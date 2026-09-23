"""Nearest-source ownership assignment on weighted graphs.

Vertices and edges carry string IDs. Edge weights are non-negative
rationals (exact ``fractions.Fraction`` arithmetic; floats are rejected).
Parallel edges (multiedges) and isolated vertices are preserved.

Each source is active on half-open time intervals [start, end).
``assign(t)`` computes, for every vertex, the set of active sources at
minimum weighted distance (ties yield multiple owners) together with a
verifiable distance proof (a shortest-path witness per owner).

Zero-weight connected components share a single distance, so tied
ownership is propagated to every member of the component.

``deactivate_and_diff`` recomputes ownership after a source is
deactivated and reports the ownership-change boundary (edges where the
new owner sets differ across the edge and at least one endpoint
changed) plus distance proofs for the changed vertices.
"""

from __future__ import annotations

import heapq
from collections import deque
from dataclasses import dataclass
from fractions import Fraction


def to_rational(value) -> Fraction:
    """Coerce an int / Fraction / rational string to ``Fraction``.

    Floats are rejected so all arithmetic stays exact.
    """
    if isinstance(value, bool):
        raise TypeError("bool is not accepted as a rational")
    if isinstance(value, Fraction):
        return value
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, str):
        try:
            return Fraction(value)
        except ValueError as exc:
            raise ValueError(f"invalid rational literal: {value!r}") from exc
    raise TypeError(
        f"expected int, str or Fraction, got {type(value).__name__}; "
        "floats are rejected to keep arithmetic exact"
    )


@dataclass(frozen=True)
class Edge:
    id: str
    u: str
    v: str
    weight: Fraction


@dataclass(frozen=True)
class ProofHop:
    """One hop of a shortest-path witness, in source-to-vertex order."""

    edge_id: str
    frm: str
    to: str
    weight: Fraction
    cumulative: Fraction  # distance from the source after this hop


@dataclass(frozen=True)
class OwnershipChange:
    vertex: str
    before: frozenset
    after: frozenset


class Assignment:
    """Ownership snapshot of a network at one point in time."""

    def __init__(self, time, dist, owners, pred, source_vertices):
        self.time = time
        self.owners = owners  # vertex -> frozenset of source IDs (may be empty)
        self._dist = dist  # vertex -> Fraction or None (unreachable)
        self._pred = pred  # vertex -> owner -> (prev_vertex, edge_id, weight)
        self._source_vertices = source_vertices  # active source ID -> vertex

    def owners_of(self, vertex):
        return self.owners[vertex]

    def distance(self, vertex):
        """Weighted distance to the nearest source(s), None if unreachable."""
        return self._dist[vertex]

    def proof(self, vertex, owner):
        """Shortest-path witness for ``owner`` at ``vertex``.

        Returns hops ordered from the source to ``vertex``; the hop
        weights sum to ``distance(vertex)``. Empty list means ``vertex``
        itself hosts the source.
        """
        if vertex not in self.owners:
            raise KeyError(f"unknown vertex: {vertex!r}")
        if owner not in self.owners[vertex]:
            raise KeyError(f"{owner!r} does not own {vertex!r}")
        chain = []
        cur = vertex
        while True:
            entry = self._pred.get(cur, {}).get(owner)
            if entry is None:
                break
            prev, edge_id, weight = entry
            chain.append((prev, cur, edge_id, weight))
            cur = prev
        if self._source_vertices.get(owner) != cur:
            raise AssertionError("corrupt predecessor chain")
        chain.reverse()
        return [
            ProofHop(edge_id, prev, nxt, weight, self._dist[nxt])
            for prev, nxt, edge_id, weight in chain
        ]

    def verify_proof(self, vertex, owner):
        """Independently check the witness: endpoints, cumulatives, total."""
        dist = self._dist.get(vertex)
        if dist is None or owner not in self.owners.get(vertex, ()):
            return False
        hops = self.proof(vertex, owner)
        if not hops:
            return dist == 0 and self._source_vertices.get(owner) == vertex
        if self._source_vertices.get(owner) != hops[0].frm:
            return False
        total = Fraction(0)
        for hop in hops:
            total += hop.weight
            if hop.cumulative != total:
                return False
        return hops[-1].to == vertex and total == dist


class OwnershipDiff:
    """Ownership change between two assignments of the same network."""

    def __init__(self, time, before, after, edges):
        self.time = time
        self.before = before
        self.after = after
        self.changes = [
            OwnershipChange(v, before.owners[v], after.owners[v])
            for v in after.owners
            if before.owners[v] != after.owners[v]
        ]
        self.changed_vertices = {c.vertex for c in self.changes}
        # Boundary of the changed region: edges with exactly one endpoint
        # inside the changed vertex set.
        self.boundary_edges = [
            e
            for e in edges
            if (e.u in self.changed_vertices) != (e.v in self.changed_vertices)
        ]
        # Borders between ownership regions in the new assignment: edges
        # whose endpoints now have different owner sets.
        self.owner_borders = [
            e for e in edges if after.owners[e.u] != after.owners[e.v]
        ]
        self.proofs = {
            c.vertex: {sid: after.proof(c.vertex, sid) for sid in sorted(c.after)}
            for c in self.changes
            if c.after
        }


@dataclass
class _Source:
    id: str
    vertex: str
    intervals: list  # list of (start: Fraction, end: Fraction | None), [start, end)


class Network:
    """Undirected weighted multigraph with time-windowed sources."""

    def __init__(self):
        self.vertices = {}  # vertex ID -> None (insertion-ordered set)
        self.edges = {}  # edge ID -> Edge
        self._adj = {}  # vertex -> [(edge_id, neighbor)]
        self._sources = {}  # source ID -> _Source

    # ----------------------------------------------------------- build
    def add_vertex(self, vertex_id):
        if vertex_id in self.vertices:
            raise ValueError(f"duplicate vertex: {vertex_id!r}")
        self.vertices[vertex_id] = None
        self._adj[vertex_id] = []

    def add_edge(self, edge_id, u, v, weight):
        """Add an undirected edge; endpoints are auto-created.

        Parallel edges between the same pair are allowed as long as the
        edge ID is unique. Self-loops are allowed.
        """
        weight = to_rational(weight)
        if weight < 0:
            raise ValueError(f"edge weight must be non-negative, got {weight}")
        if edge_id in self.edges:
            raise ValueError(f"duplicate edge: {edge_id!r}")
        for vertex in (u, v):
            if vertex not in self.vertices:
                self.add_vertex(vertex)
        self.edges[edge_id] = Edge(edge_id, u, v, weight)
        self._adj[u].append((edge_id, v))
        self._adj[v].append((edge_id, u))

    def add_source(self, source_id, vertex, intervals=None):
        """Register a source; ``intervals`` are [start, end) pairs.

        ``end=None`` means open-ended. Defaults to a single interval
        [0, infinity).
        """
        if source_id in self._sources:
            raise ValueError(f"duplicate source: {source_id!r}")
        if vertex not in self.vertices:
            self.add_vertex(vertex)
        if intervals is None:
            intervals = [(Fraction(0), None)]
        parsed = []
        for start, end in intervals:
            start = to_rational(start)
            end = None if end is None else to_rational(end)
            if end is not None and not start < end:
                raise ValueError(f"empty interval [{start}, {end})")
            parsed.append((start, end))
        self._sources[source_id] = _Source(source_id, vertex, parsed)

    # -------------------------------------------------------- intervals
    @staticmethod
    def _active(source, t):
        return any(start <= t and (end is None or t < end)
                   for start, end in source.intervals)

    def active_sources(self, t):
        t = to_rational(t)
        return [s for s in self._sources.values() if self._active(s, t)]

    def deactivate(self, source_id, at):
        """Deactivate a source from time ``at`` onward.

        The interval containing ``at`` is truncated to end at ``at``
        (right-open, so the source is inactive at ``at`` itself);
        intervals starting at or after ``at`` are dropped.
        """
        at = to_rational(at)
        source = self._sources[source_id]
        kept = []
        for start, end in source.intervals:
            if at <= start:
                continue
            if end is None or at < end:
                kept.append((start, at))
            else:
                kept.append((start, end))
        source.intervals = kept

    # -------------------------------------------------------- assignment
    def assign(self, t):
        """Compute nearest-source ownership at time ``t``."""
        t = to_rational(t)
        active = self.active_sources(t)
        dist, owners, pred = self._compute(active)
        source_vertices = {s.id: s.vertex for s in active}
        return Assignment(t, dist, owners, pred, source_vertices)

    def _compute(self, active):
        dist = {v: None for v in self.vertices}
        pq = []
        for source in active:
            if dist[source.vertex] is None:
                dist[source.vertex] = Fraction(0)
                heapq.heappush(pq, (Fraction(0), source.vertex))
        while pq:
            d, u = heapq.heappop(pq)
            if d > dist[u]:
                continue
            for edge_id, nbr in self._adj[u]:
                nd = d + self.edges[edge_id].weight
                if dist[nbr] is None or nd < dist[nbr]:
                    dist[nbr] = nd
                    heapq.heappush(pq, (nd, nbr))

        # Zero-weight edges always join equal-distance vertices, so each
        # zero-weight connected component shares one distance and one
        # owner set: ties propagate to the whole component.
        comp_of = {}
        components = []
        for root in self.vertices:
            if root in comp_of:
                continue
            members = []
            stack = [root]
            comp_of[root] = root
            while stack:
                x = stack.pop()
                members.append(x)
                for edge_id, nbr in self._adj[x]:
                    if self.edges[edge_id].weight == 0 and nbr not in comp_of:
                        comp_of[nbr] = root
                        stack.append(nbr)
            components.append(members)

        def comp_key(members):
            d = dist[members[0]]
            return (1, Fraction(0)) if d is None else (0, d)

        components.sort(key=comp_key)

        src_at = {}
        for source in active:
            src_at.setdefault(source.vertex, []).append(source.id)

        owners = {}
        pred = {}
        for members in components:
            d = dist[members[0]]
            if d is None:
                for v in members:
                    owners[v] = frozenset()
                continue
            member_set = set(members)
            comp_owners = set()
            seeds = {}  # owner -> [(member, prev_vertex, edge_id, weight)]
            for v in members:
                for sid in src_at.get(v, ()):
                    if sid not in comp_owners:
                        comp_owners.add(sid)
                        seeds.setdefault(sid, []).append((v, None, None, None))
                for edge_id, nbr in self._adj[v]:
                    edge = self.edges[edge_id]
                    if edge.weight == 0:
                        continue
                    du = dist[nbr]
                    if du is not None and du + edge.weight == d:
                        for sid in owners[nbr]:
                            if sid not in comp_owners:
                                comp_owners.add(sid)
                                seeds.setdefault(sid, []).append(
                                    (v, nbr, edge_id, edge.weight)
                                )
            for v in members:
                owners[v] = frozenset(comp_owners)
                pred.setdefault(v, {})
            # Spread each owner over the zero-weight component so every
            # member gets a complete witness chain.
            for sid, seed_list in seeds.items():
                visited = set()
                queue = deque()
                for member, prev, edge_id, weight in seed_list:
                    if member in visited:
                        continue
                    visited.add(member)
                    if prev is not None:
                        pred[member][sid] = (prev, edge_id, weight)
                    queue.append(member)
                while queue:
                    x = queue.popleft()
                    for edge_id, nbr in self._adj[x]:
                        if (
                            self.edges[edge_id].weight == 0
                            and nbr in member_set
                            and nbr not in visited
                        ):
                            visited.add(nbr)
                            pred[nbr][sid] = (x, edge_id, Fraction(0))
                            queue.append(nbr)
        return dist, owners, pred

    # ------------------------------------------------------------- diff
    def diff(self, before, after):
        """Ownership change between two assignments of this network."""
        return OwnershipDiff(after.time, before, after, self.edges.values())

    def deactivate_and_diff(self, source_id, at):
        """Deactivate ``source_id`` at ``at`` and diff old vs new ownership."""
        at = to_rational(at)
        before = self.assign(at)
        self.deactivate(source_id, at)
        after = self.assign(at)
        return self.diff(before, after)


# ---------------------------------------------------------------- demo
def _fmt_dist(d):
    return "unreachable" if d is None else str(d)


def _fmt_proof(hops):
    if not hops:
        return "(source vertex itself, distance 0)"
    parts = [hops[0].frm]
    for hop in hops:
        parts.append(f"--{hop.edge_id}(w={hop.weight})--> {hop.to}")
    total = hops[-1].cumulative
    return " ".join(parts) + f"   [total distance = {total}]"


def _print_assignment(title, assignment):
    print(title)
    for vertex in assignment.owners:
        owners = sorted(assignment.owners[vertex])
        who = ", ".join(owners) if owners else "(no source)"
        print(f"  {vertex:>4}  owners={{{who}}}  dist={_fmt_dist(assignment.distance(vertex))}")


def main():
    print("=== Scenario 1: chain A--X--B with equal edge weights ===")
    net = Network()
    net.add_edge("e1", "A", "X", 2)
    net.add_edge("e2", "X", "B", 2)
    net.add_source("SA", "A")
    net.add_source("SB", "B")

    before = net.assign(10)
    _print_assignment("t=10, both sources active:", before)
    for sid in sorted(before.owners_of("X")):
        print(f"  proof X <- {sid}: {_fmt_proof(before.proof('X', sid))}")

    print()
    print("--- deactivate SA at t=10 ---")
    diff = net.deactivate_and_diff("SA", 10)
    _print_assignment("t=10, SA deactivated:", diff.after)
    print("ownership changes:")
    for change in diff.changes:
        before_set = "{" + ", ".join(sorted(change.before)) + "}"
        after_set = "{" + ", ".join(sorted(change.after)) + "}"
        print(f"  {change.vertex}: {before_set} -> {after_set}")
    print("ownership-change boundary edges (changed region <-> stable region):")
    for edge in diff.boundary_edges:
        print(f"  {edge.id}: {edge.u} -- {edge.v} (w={edge.weight})")
    print("ownership region borders in the new assignment:")
    for edge in diff.owner_borders:
        left = "{" + ", ".join(sorted(diff.after.owners[edge.u])) + "}"
        right = "{" + ", ".join(sorted(diff.after.owners[edge.v])) + "}"
        print(f"  {edge.id}: {edge.u} {left} -- {edge.v} {right} (w={edge.weight})")
    print("distance proofs for changed vertices:")
    for vertex, per_source in diff.proofs.items():
        for sid, hops in per_source.items():
            print(f"  {vertex} <- {sid}: {_fmt_proof(hops)}")

    print()
    print("=== Scenario 2: three sources on a chain, deactivate the middle ===")
    net3 = Network()
    net3.add_edge("f1", "P", "Q", 1)
    net3.add_edge("f2", "Q", "R", 1)
    net3.add_edge("f3", "R", "S", 1)
    net3.add_edge("f4", "S", "T", 1)
    net3.add_source("SP", "P")
    net3.add_source("SR", "R")
    net3.add_source("ST", "T")
    diff3 = net3.deactivate_and_diff("SR", 1)
    _print_assignment("t=1, SR deactivated:", diff3.after)
    print("ownership changes:")
    for change in diff3.changes:
        before_set = "{" + ", ".join(sorted(change.before)) + "}"
        after_set = "{" + ", ".join(sorted(change.after)) + "}"
        print(f"  {change.vertex}: {before_set} -> {after_set}")
    print("ownership-change boundary edges:")
    for edge in diff3.boundary_edges:
        print(f"  {edge.id}: {edge.u} -- {edge.v} (w={edge.weight})")
    print("ownership region borders in the new assignment:")
    for edge in diff3.owner_borders:
        left = "{" + ", ".join(sorted(diff3.after.owners[edge.u])) + "}"
        right = "{" + ", ".join(sorted(diff3.after.owners[edge.v])) + "}"
        print(f"  {edge.id}: {edge.u} {left} -- {edge.v} {right} (w={edge.weight})")

    print()
    print("=== Scenario 3: tie propagation inside a zero-weight component ===")
    net2 = Network()
    net2.add_edge("z1", "S1v", "c1", 3)
    net2.add_edge("z0", "c1", "c2", 0)  # zero-weight edge inside the component
    net2.add_edge("z2", "c2", "S2v", 3)
    net2.add_source("S1", "S1v")
    net2.add_source("S2", "S2v")
    assignment = net2.assign(0)
    _print_assignment("t=0:", assignment)
    for vertex in ("c1", "c2"):
        for sid in sorted(assignment.owners_of(vertex)):
            print(f"  proof {vertex} <- {sid}: {_fmt_proof(assignment.proof(vertex, sid))}")


if __name__ == "__main__":
    main()
