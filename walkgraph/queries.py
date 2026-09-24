"""Length-interval walk counting and lexicographic k-th walk queries.

Semantics
---------
- Length intervals are left-closed, right-open: ``[lo, hi)``.
  ``hi=None`` selects the unbounded (infinite length) mode.
- A walk signature is the flattened tuple ``(v0, e0, v1, e1, ..., vL)``;
  lexicographic order compares vertex/edge IDs position by position.
- Counting supports arbitrary-precision integers (default) and an
  optional modulus.  The k-th walk query ALWAYS recomputes exact
  (unmodded) DP counts for selection: results of ``count_walks`` --
  modded or not -- are never reused for ranking, so the two kinds of
  results can never be mixed up in the sort/select pipeline.
"""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass
from enum import Enum

DEFAULT_MAX_LENGTH = 4096


class KthStatus(Enum):
    OK = "ok"
    NO_PATH = "no_path"
    K_OUT_OF_RANGE = "k_out_of_range"
    BUDGET_EXCEEDED = "budget_exceeded"


@dataclass(frozen=True)
class CycleEvidence:
    """Witness that infinitely many s->t walks exist.

    ``cycle``/``cycle_edges`` form a directed cycle that is reachable
    from ``s`` (via ``prefix``) and can still reach ``t`` (via
    ``suffix``).
    """

    cycle: tuple        # vertex ids [c0, c1, ...], edges close c_last -> c0
    cycle_edges: tuple  # edge ids, cycle_edges[i] leaves cycle[i]
    prefix: tuple       # vertex ids from s to cycle[0], inclusive
    suffix: tuple       # vertex ids from cycle[0] to t, inclusive


@dataclass(frozen=True)
class CountResult:
    interval: tuple          # (lo, hi); hi is None in infinite mode
    count: object            # int, or None iff infinite
    infinite: bool
    evidence: object         # CycleEvidence or None
    reachable_slice: frozenset  # vertices on some s->t walk region
    mod: object


@dataclass(frozen=True)
class KthResult:
    status: KthStatus
    length: int
    k: int
    vertices: object         # tuple of vertex ids, or None unless OK
    edges: object            # tuple of edge ids, or None unless OK
    total: object            # exact total walks of this length, if computed
    reachable_slice: frozenset


# ---------------------------------------------------------------------------
# Sparse exact/modular matrix helpers (dict-of-dict over vertex indices)
# ---------------------------------------------------------------------------

def _mat_mul(a, b, mod=None):
    if not a or not b:
        return {}
    out = {}
    for i, arow in a.items():
        orow = None
        for k, aik in arow.items():
            brow = b.get(k)
            if not brow:
                continue
            if orow is None:
                orow = out.setdefault(i, {})
            for j, bkj in brow.items():
                orow[j] = orow.get(j, 0) + aik * bkj
        if mod is not None and orow:
            for j in list(orow):
                v = orow[j] % mod
                if v:
                    orow[j] = v
                else:
                    del orow[j]
    if mod is not None:
        out = {i: r for i, r in out.items() if r}
    return out


def _mat_add(a, b, mod=None):
    out = {i: dict(r) for i, r in a.items()}
    for i, row in b.items():
        orow = out.setdefault(i, {})
        for j, v in row.items():
            nv = orow.get(j, 0) + v
            if mod is not None:
                nv %= mod
            if nv:
                orow[j] = nv
            elif j in orow:
                del orow[j]
    return {i: r for i, r in out.items() if r}


class GraphView:
    """Query view pinned at one graph version."""

    def __init__(self, graph, version):
        if version not in graph._snapshots:
            raise KeyError(f"unknown version: {version!r}")
        self._graph = graph
        self.version = version
        snap = graph._snapshots[version]
        self._snap = snap
        self._adj = snap.adjacency()
        radj = {v: [] for v in snap.vertices}
        for eid, (src, dst) in snap.edges.items():
            radj[dst].append((eid, src))
        self._radj = radj
        self._index = {v: i for i, v in enumerate(sorted(snap.vertices))}

    @property
    def vertices(self):
        return self._snap.vertices

    @property
    def edges(self):
        return dict(self._snap.edges)

    def adjacency(self):
        return {v: list(entries) for v, entries in self._adj.items()}

    # -- reachability ----------------------------------------------------

    def _check_vertex(self, v):
        if v not in self._snap.vertices:
            raise KeyError(f"unknown vertex id: {v!r}")

    @staticmethod
    def _bfs(start, adj):
        seen = {start}
        dq = deque([start])
        while dq:
            u = dq.popleft()
            for _, w in adj.get(u, ()):
                if w not in seen:
                    seen.add(w)
                    dq.append(w)
        return seen

    def reachable_slice(self, s, t):
        """Vertices reachable from ``s`` that can also reach ``t``."""
        self._check_vertex(s)
        self._check_vertex(t)
        fwd = self._bfs(s, self._adj)
        bwd = self._bfs(t, self._radj)
        return frozenset(fwd & bwd)

    # -- matrix power / DP blocks (cached per version, per modulus) ------

    def _full_matrix(self, mod=None):
        m = {}
        idx = self._index
        for src, dst in self._snap.edges.values():
            row = m.setdefault(idx[src], {})
            j = idx[dst]
            row[j] = row.get(j, 0) + 1
        if mod is not None:
            m = {i: {j: v % mod for j, v in r.items()} for i, r in m.items()}
            m = {i: {j: v for j, v in r.items() if v} for i, r in m.items()}
            m = {i: r for i, r in m.items() if r}
        return m

    def _identity(self, mod=None):
        one = 1 if mod is None else 1 % mod
        if not one:
            return {}
        return {i: {i: one} for i in range(len(self._index))}

    def _block(self, i, mod):
        """Return (M^(2^i), sum_{j<2^i} M^j), cached per (version, mod)."""
        cache = self._graph._block_cache[self.version].setdefault(mod, {})
        if i not in cache:
            if i == 0:
                cache[i] = (self._full_matrix(mod), self._identity(mod))
            else:
                b_prev, s_prev = self._block(i - 1, mod)
                cache[i] = (
                    _mat_mul(b_prev, b_prev, mod),
                    _mat_add(s_prev, _mat_mul(b_prev, s_prev, mod), mod),
                )
        return cache[i]

    def _prefix(self, n, mod):
        """Return sum_{j=0}^{n-1} M^j via cached doubling blocks."""
        s_acc = {}
        p_acc = self._identity(mod)
        i = 0
        while n:
            if n & 1:
                b_i, s_i = self._block(i, mod)
                s_acc = _mat_add(s_acc, _mat_mul(p_acc, s_i, mod), mod)
                p_acc = _mat_mul(p_acc, b_i, mod)
            n >>= 1
            i += 1
        return s_acc

    def _entry(self, m, s, t):
        row = m.get(self._index[s])
        return row.get(self._index[t], 0) if row else 0

    # -- public queries --------------------------------------------------

    def count_walks(self, s, t, lo=0, hi=None, mod=None):
        """Number of s->t walks with length in [lo, hi).

        ``hi=None`` is the infinite-length mode: if a cycle is reachable
        from ``s`` and can reach ``t``, the result is ``infinite=True``
        with a :class:`CycleEvidence`; otherwise the exact finite total
        over all lengths is returned.  ``mod`` reduces counts modulo a
        positive integer; without it counts are exact big integers.
        """
        self._check_vertex(s)
        self._check_vertex(t)
        if lo < 0:
            raise ValueError("lo must be >= 0")
        if hi is not None and hi < lo:
            raise ValueError("hi must be >= lo")
        if mod is not None and mod <= 0:
            raise ValueError("mod must be a positive integer")
        slice_ = self.reachable_slice(s, t)
        interval = (lo, hi)
        if hi is not None and hi == lo:
            return CountResult(interval, 0, False, None, slice_, mod)
        if hi is None:
            evidence = self.infinite_walks(s, t)
            if evidence is not None:
                return CountResult(interval, None, True, evidence, slice_, mod)
            # No cycle in the s-t slice: it is a DAG, so walk lengths are
            # bounded by len(slice_) - 1 and this prefix covers them all.
            hi_eff = len(slice_) + 1
        else:
            hi_eff = hi
        total = self._entry(self._prefix(hi_eff, mod), s, t)
        skipped = self._entry(self._prefix(lo, mod), s, t)
        count = total - skipped
        if mod is not None:
            count %= mod
        return CountResult(interval, count, False, None, slice_, mod)

    def infinite_walks(self, s, t):
        """Cycle evidence iff infinitely many s->t walks exist, else None."""
        self._check_vertex(s)
        self._check_vertex(t)
        slice_ = self.reachable_slice(s, t)
        found = self._find_cycle(slice_)
        if found is None:
            return None
        cyc_v, cyc_e = found
        entry = cyc_v[0]
        return CycleEvidence(
            cycle=cyc_v,
            cycle_edges=cyc_e,
            prefix=self._path_vertices(s, entry, slice_),
            suffix=self._path_vertices(entry, t, slice_),
        )

    def kth_walk(self, s, t, length, k, max_length=DEFAULT_MAX_LENGTH):
        """The k-th (0-based) lexicographic s->t walk of exactly ``length``.

        Selection uses exact big-integer DP computed here -- never the
        (possibly modded) counting results.  ``max_length`` caps the DP
        depth and output trajectory length; exceeding it yields
        ``BUDGET_EXCEEDED`` rather than a misleading ``NO_PATH``.
        """
        self._check_vertex(s)
        self._check_vertex(t)
        if length < 0:
            raise ValueError("length must be >= 0")
        if k < 0:
            raise ValueError("k must be >= 0")
        if max_length < 0:
            raise ValueError("max_length must be >= 0")
        slice_ = self.reachable_slice(s, t)
        if length > max_length:
            return KthResult(KthStatus.BUDGET_EXCEEDED, length, k,
                             None, None, None, slice_)
        if length == 0:
            if s != t:
                return KthResult(KthStatus.NO_PATH, length, k,
                                 None, None, 0, slice_)
            if k == 0:
                return KthResult(KthStatus.OK, length, k,
                                 (s,), (), 1, slice_)
            return KthResult(KthStatus.K_OUT_OF_RANGE, length, k,
                             None, None, 1, slice_)
        # Exact DP layers: layers[l][v] = # walks of length l from v to t.
        layers = [{t: 1}]
        for _ in range(length):
            prev = layers[-1]
            cur = {}
            for v in slice_:
                total = 0
                for _, w in self._adj.get(v, ()):
                    if w in slice_:
                        total += prev.get(w, 0)
                if total:
                    cur[v] = total
            layers.append(cur)
        total = layers[length].get(s, 0)
        if total == 0:
            return KthResult(KthStatus.NO_PATH, length, k,
                             None, None, 0, slice_)
        if k >= total:
            return KthResult(KthStatus.K_OUT_OF_RANGE, length, k,
                             None, None, total, slice_)
        v = s
        rem = k
        traj = [s]
        epath = []
        for l in range(length, 0, -1):
            nxt = layers[l - 1]
            for eid, w in self._adj.get(v, ()):
                if w not in slice_:
                    continue
                c = nxt.get(w, 0)
                if c == 0:
                    continue
                if rem < c:
                    epath.append(eid)
                    traj.append(w)
                    v = w
                    break
                rem -= c
            else:  # pragma: no cover - defensive, DP guarantees a hit
                raise AssertionError("k-th selection fell through")
        return KthResult(KthStatus.OK, length, k,
                         tuple(traj), tuple(epath), total, slice_)

    # -- internals ---------------------------------------------------------

    def _find_cycle(self, slice_):
        color = {}
        for start in sorted(slice_):
            if color.get(start):
                continue
            color[start] = 1
            path = [start]
            pedges = [None]  # pedges[i] is the edge entering path[i]
            stack = [iter(self._adj.get(start, ()))]
            while stack:
                advanced = False
                for eid, dst in stack[-1]:
                    if dst not in slice_:
                        continue
                    state = color.get(dst, 0)
                    if state == 1:
                        idx = path.index(dst)
                        return (tuple(path[idx:]),
                                tuple(pedges[idx + 1:] + [eid]))
                    if state == 0:
                        color[dst] = 1
                        path.append(dst)
                        pedges.append(eid)
                        stack.append(iter(self._adj.get(dst, ())))
                        advanced = True
                        break
                if not advanced:
                    color[path.pop()] = 2
                    pedges.pop()
                    stack.pop()
        return None

    def _path_vertices(self, src, dst, slice_):
        prev = {src: None}
        dq = deque([src])
        while dq:
            u = dq.popleft()
            if u == dst:
                break
            for _, w in self._adj.get(u, ()):
                if w in slice_ and w not in prev:
                    prev[w] = u
                    dq.append(w)
        path = []
        node = dst
        while node is not None:
            path.append(node)
            node = prev[node]
        return tuple(reversed(path))
