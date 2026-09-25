"""Per-key bitemporal rectangle index.

Fragments of one key are organised in a centered interval tree over their
*version* intervals; every tree node keeps its fragments sorted by business
(valid) start so the valid dimension can be bisected.  A rectangle query
(observation-version interval x business-time interval) therefore only
visits rectangles that can intersect the query box -- it never scans the
append-only history log.
"""

from __future__ import annotations

from bisect import bisect_left

from .model import Fragment, from_key, to_key

INF_VERSION = 1 << 62  # stands for "version interval still open"


class QueryStats:
    """Instrumentation proving queries do not scan all history."""

    def __init__(self) -> None:
        self.examined = 0
        self.matched = 0

    def __repr__(self) -> str:
        return f"QueryStats(examined={self.examined}, matched={self.matched})"


def _vend(frag: Fragment) -> int:
    return frag.ver_to if frag.ver_to is not None else INF_VERSION


class _Node:
    __slots__ = ("center", "frags", "from_keys", "left", "right")

    def __init__(self, center: int) -> None:
        self.center = center
        self.frags: list[Fragment] = []
        self.from_keys: list[tuple[int, int]] = []
        self.left: _Node | None = None
        self.right: _Node | None = None


def _build(frags: list[Fragment], endpoints: list[int]) -> _Node | None:
    if not frags:
        return None
    center = endpoints[len(endpoints) // 2]
    node = _Node(center)
    left, right = [], []
    for frag in frags:
        if frag.ver_from <= center < _vend(frag):
            node.frags.append(frag)
        elif _vend(frag) <= center:
            left.append(frag)
        else:
            right.append(frag)
    node.frags.sort(key=lambda f: from_key(f.valid_from))
    node.from_keys = [from_key(f.valid_from) for f in node.frags]
    node.left = _build(left, [e for e in endpoints if e < center])
    node.right = _build(right, [e for e in endpoints if e > center])
    return node


def _scan(node: _Node, lo_key, hi_key, version_ok, out: list, stats: QueryStats) -> None:
    stop = bisect_left(node.from_keys, hi_key)
    for frag in node.frags[:stop]:
        stats.examined += 1
        if to_key(frag.valid_to) > lo_key and version_ok(frag):
            stats.matched += 1
            out.append(frag)


def _query(node: _Node | None, v_lo: int, v_hi: int, lo_key, hi_key,
           out: list, stats: QueryStats) -> None:
    if node is None:
        return
    if v_hi <= node.center:
        # Node fragments end after the query box: they must start early enough.
        _scan(node, lo_key, hi_key, lambda f: f.ver_from < v_hi, out, stats)
        _query(node.left, v_lo, v_hi, lo_key, hi_key, out, stats)
    elif v_lo > node.center:
        # Node fragments start before the query box: they must end late enough.
        _scan(node, lo_key, hi_key, lambda f: _vend(f) > v_lo, out, stats)
        _query(node.right, v_lo, v_hi, lo_key, hi_key, out, stats)
    else:
        # The center lies inside the query box: every node fragment matches
        # on the version axis, only the valid axis remains.
        _scan(node, lo_key, hi_key, lambda f: True, out, stats)
        _query(node.left, v_lo, v_hi, lo_key, hi_key, out, stats)
        _query(node.right, v_lo, v_hi, lo_key, hi_key, out, stats)


class KeyIndex:
    """Rectangle index over all fragments (open and closed) of one key."""

    def __init__(self) -> None:
        self.fragments: list[Fragment] = []
        self._tree: _Node | None = None
        self._dirty = False

    def frontier(self) -> list[Fragment]:
        return [f for f in self.fragments if f.ver_to is None]

    def apply(self, closes: list[Fragment], adds: list[Fragment], version: int) -> None:
        for frag in closes:
            assert frag.ver_to is None, "a closed rectangle is immutable"
            frag.ver_to = version
        self.fragments.extend(adds)
        self._dirty = True

    def _ensure_tree(self) -> None:
        if not self._dirty:
            return
        endpoints = sorted(
            {f.ver_from for f in self.fragments}
            | {f.ver_to for f in self.fragments if f.ver_to is not None}
        )
        self._tree = _build(self.fragments, endpoints) if endpoints else None
        self._dirty = False

    def query(self, v_lo: int, v_hi: int, valid_from, valid_to,
              stats: QueryStats | None = None) -> list[Fragment]:
        self._ensure_tree()
        out: list[Fragment] = []
        _query(self._tree, v_lo, v_hi, from_key(valid_from), to_key(valid_to),
               out, stats if stats is not None else QueryStats())
        out.sort(key=lambda f: (f.ver_from, from_key(f.valid_from)))
        return out
