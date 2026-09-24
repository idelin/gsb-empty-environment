"""Versioned directed multigraph with string vertex and edge IDs."""

from __future__ import annotations

from contextlib import contextmanager
from dataclasses import dataclass


@dataclass(frozen=True)
class Snapshot:
    """Immutable structural view of one committed graph version."""

    vertices: frozenset
    edges: dict  # edge id -> (source vertex id, target vertex id)

    def adjacency(self):
        """src -> list of (edge id, dst), sorted by (edge id, dst).

        The sort order defines the lexicographic candidate order used by
        the k-th walk query: a walk signature is the flattened tuple
        ``(v0, e0, v1, e1, ..., vL)``, so at each step candidates are
        ordered by edge id first, then by target vertex id.
        """
        adj = {v: [] for v in self.vertices}
        for eid, (src, dst) in self.edges.items():
            adj[src].append((eid, dst))
        for entries in adj.values():
            entries.sort()
        return adj


class VersionedGraph:
    """A directed multigraph where every mutation commits a new version.

    - Vertices and edges carry string IDs; isolated vertices and
      multi-edges (parallel edges with distinct IDs) are preserved.
    - Old versions stay queryable via :meth:`at`.
    - Matrix-power / DP blocks are cached per version.  A mutation
      invalidates exactly the blocks of the new (derived) version while
      blocks of older versions are retained, so historical queries never
      recompute and never observe stale data.
    """

    def __init__(self):
        self._vertices = set()
        self._edges = {}
        self._version = 0
        self._batch_depth = 0
        self._dirty = False
        self._snapshots = {0: self._take_snapshot()}
        self._block_cache = {0: {}}

    @property
    def version(self):
        """Current (latest) committed version number."""
        return self._version

    @property
    def vertices(self):
        return frozenset(self._vertices)

    @property
    def edges(self):
        return dict(self._edges)

    def versions(self):
        return sorted(self._snapshots)

    def _take_snapshot(self):
        return Snapshot(frozenset(self._vertices), dict(self._edges))

    def _touch(self):
        self._dirty = True
        if self._batch_depth == 0:
            self._commit()

    def _commit(self):
        if not self._dirty:
            return
        self._version += 1
        self._snapshots[self._version] = self._take_snapshot()
        # Incremental invalidation: blocks are keyed by version id, so the
        # new version starts with an empty (invalidated) cache while every
        # older version keeps its already computed matrix powers / DP
        # blocks untouched.
        self._block_cache[self._version] = {}
        self._dirty = False

    @contextmanager
    def batch(self):
        """Group several mutations into a single version commit."""
        self._batch_depth += 1
        try:
            yield self
        finally:
            self._batch_depth -= 1
            if self._batch_depth == 0:
                self._commit()

    def add_vertex(self, vid):
        vid = str(vid)
        if vid in self._vertices:
            return self._version
        self._vertices.add(vid)
        self._touch()
        return self._version

    def add_edge(self, eid, src, dst):
        eid, src, dst = str(eid), str(src), str(dst)
        if eid in self._edges:
            raise ValueError(f"duplicate edge id: {eid!r}")
        self._vertices.update((src, dst))
        self._edges[eid] = (src, dst)
        self._touch()
        return self._version

    def remove_edge(self, eid):
        if eid not in self._edges:
            raise KeyError(f"unknown edge id: {eid!r}")
        del self._edges[eid]
        self._touch()
        return self._version

    def remove_vertex(self, vid):
        if vid not in self._vertices:
            raise KeyError(f"unknown vertex id: {vid!r}")
        self._vertices.discard(vid)
        self._edges = {
            e: (s, d) for e, (s, d) in self._edges.items()
            if s != vid and d != vid
        }
        self._touch()
        return self._version

    def at(self, version=None):
        """Return a query view pinned at ``version`` (default: latest)."""
        from .queries import GraphView

        if version is None:
            version = self._version
        return GraphView(self, version)
