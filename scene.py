"""Dynamic bounding-volume hierarchy over editable segment groups.

Design notes
------------
* The tree is *persistent*: every mutation path-copies only the nodes on
  the affected root-to-leaf path (plus rotation nodes), so a local move
  repairs only the relevant nodes and every untouched subtree is shared
  with the previous version.
* Queries never read the live scene; they read an immutable ``Version``
  (an explicit snapshot or one captured implicitly).  Rebalancing creates
  new nodes instead of mutating old ones, therefore an in-flight query
  can never observe a half-rotated tree, and a node is reclaimed by the
  garbage collector only after the last version/query referencing it is
  released.  ``Node.live`` / ``Node.allocated`` are resource counters
  used by the test-suite to prove old versions are freed.
* All distances are exact squared rationals; comparisons never use
  epsilon tolerances.
"""

from __future__ import annotations

import heapq
import itertools
from fractions import Fraction
from typing import NamedTuple, Optional

from geometry import frac, point, seg_seg_dist2


class QueryCancelled(Exception):
    """Raised by ``Query.step`` after ``Query.cancel`` was requested."""


# ---------------------------------------------------------------------------
# Bounding boxes (exact, axis aligned)

def _bbox_of(p0, p1):
    return (min(p0[0], p1[0]), min(p0[1], p1[1]),
            max(p0[0], p1[0]), max(p0[1], p1[1]))


def _bbox_union(a, b):
    return (min(a[0], b[0]), min(a[1], b[1]),
            max(a[2], b[2]), max(a[3], b[3]))


def _bbox_area(b):
    return (b[2] - b[0]) * (b[3] - b[1])


def _bbox_dist2(a, b):
    dx = max(a[0] - b[2], b[0] - a[2], 0)
    dy = max(a[1] - b[3], b[1] - a[3], 0)
    return dx * dx + dy * dy


# ---------------------------------------------------------------------------
# Persistent AVL-shaped bounding tree

class Node:
    """Immutable tree node.  Leaf nodes carry one segment."""

    __slots__ = ('bbox', 'left', 'right', 'seg_id', 'p0', 'p1',
                 'height', 'size')

    live = 0        # currently reachable (not yet gc'd) nodes
    allocated = 0   # total nodes ever allocated

    def __init__(self, bbox, left, right, seg_id, p0, p1, height, size):
        self.bbox = bbox
        self.left = left
        self.right = right
        self.seg_id = seg_id
        self.p0 = p0
        self.p1 = p1
        self.height = height
        self.size = size
        Node.live += 1
        Node.allocated += 1

    def __del__(self):
        Node.live -= 1

    @property
    def is_leaf(self):
        return self.seg_id is not None


def _leaf(seg_id, p0, p1):
    return Node(_bbox_of(p0, p1), None, None, seg_id, p0, p1, 1, 1)


def _mk(left, right):
    return Node(_bbox_union(left.bbox, right.bbox), left, right, None,
                None, None,
                1 + max(left.height, right.height),
                left.size + right.size)


def _rebalance(left, right):
    """AVL rotations; builds only new nodes, never mutates inputs."""
    if left.height > right.height + 1:
        if left.left.height >= left.right.height:
            return _mk(left.left, _mk(left.right, right))
        mid = left.right
        return _mk(_mk(left.left, mid.left), _mk(mid.right, right))
    if right.height > left.height + 1:
        if right.right.height >= right.left.height:
            return _mk(_mk(left, right.left), right.right)
        mid = right.left
        return _mk(_mk(left, mid.left), _mk(mid.right, right.right))
    return _mk(left, right)


def _insert(node, leaf):
    if node is None:
        return leaf
    if node.is_leaf:
        return _mk(node, leaf)
    grow_l = _bbox_area(_bbox_union(node.left.bbox, leaf.bbox)) \
        - _bbox_area(node.left.bbox)
    grow_r = _bbox_area(_bbox_union(node.right.bbox, leaf.bbox)) \
        - _bbox_area(node.right.bbox)
    if grow_l < grow_r or (grow_l == grow_r
                           and node.left.size <= node.right.size):
        return _rebalance(_insert(node.left, leaf), node.right)
    return _rebalance(node.left, _insert(node.right, leaf))


def _has(node, seg_id):
    if node.is_leaf:
        return node.seg_id == seg_id
    return _has(node.left, seg_id) or _has(node.right, seg_id)


def _delete(node, seg_id):
    if node.is_leaf:
        if node.seg_id != seg_id:
            raise KeyError(seg_id)
        return None
    if _has(node.left, seg_id):
        new_left = _delete(node.left, seg_id)
        if new_left is None:
            return node.right
        return _rebalance(new_left, node.right)
    new_right = _delete(node.right, seg_id)
    if new_right is None:
        return node.left
    return _rebalance(node.left, new_right)


def iter_leaves(node):
    """Yield ``(seg_id, p0, p1)`` for every leaf below ``node``."""
    if node is None:
        return
    stack = [node]
    while stack:
        cur = stack.pop()
        if cur.is_leaf:
            yield cur.seg_id, cur.p0, cur.p1
        else:
            stack.append(cur.left)
            stack.append(cur.right)


def count_nodes(*roots):
    """Number of distinct nodes reachable from the given roots."""
    seen = set()
    stack = [r for r in roots if r is not None]
    while stack:
        cur = stack.pop()
        if id(cur) in seen:
            continue
        seen.add(id(cur))
        if not cur.is_leaf:
            stack.append(cur.left)
            stack.append(cur.right)
    return len(seen)


# ---------------------------------------------------------------------------
# Results

class Evidence(NamedTuple):
    seg_a: object
    seg_b: object
    t_a: Fraction
    t_b: Fraction
    point_a: tuple
    point_b: tuple
    dist2: Fraction


class Result(NamedTuple):
    dist2: Optional[Fraction]   # None when no admissible pair exists
    evidence: tuple             # tuple[Evidence, ...]


# ---------------------------------------------------------------------------
# Scene, versions, groups

class _Group:
    __slots__ = ('name', 'segments', 'root', 'exclusions')

    def __init__(self, name):
        self.name = name
        self.segments = {}      # seg_id -> (p0, p1)
        self.root = None
        self.exclusions = set()  # set[frozenset({id_a, id_b})]


class Version:
    """Immutable snapshot of every group's tree root."""

    __slots__ = ('epoch', 'roots')

    def __init__(self, epoch, roots):
        self.epoch = epoch
        self.roots = roots      # dict group_name -> Node | None

    def segments(self, group):
        """Segment table of this version, read back from the tree."""
        return {sid: (p0, p1)
                for sid, p0, p1 in iter_leaves(self.roots.get(group))}


class Scene:
    def __init__(self):
        self._groups = {}
        self.exclusions = set()  # scene-wide exclusions
        self.epoch = 0

    # -- structure ------------------------------------------------------

    def add_group(self, name):
        if name in self._groups:
            raise ValueError(f'duplicate group {name!r}')
        self._groups[name] = _Group(name)

    def _group(self, name):
        try:
            return self._groups[name]
        except KeyError:
            raise KeyError(f'unknown group {name!r}') from None

    def add_exclusion(self, group, id_a, id_b):
        """Exclude the pair {id_a, id_b}; group=None means scene-wide."""
        target = self.exclusions if group is None \
            else self._group(group).exclusions
        target.add(frozenset((id_a, id_b)))

    # -- mutations (each produces a new persistent version) --------------

    def add_segment(self, group, seg_id, p0, p1):
        g = self._group(group)
        if seg_id in g.segments:
            raise ValueError(f'duplicate segment id {seg_id!r}')
        p0, p1 = point(*p0), point(*p1)
        g.segments[seg_id] = (p0, p1)
        g.root = _insert(g.root, _leaf(seg_id, p0, p1))
        self.epoch += 1

    def move_segment(self, group, seg_id, p0, p1):
        """Local move: only the affected root-to-leaf paths are rebuilt."""
        g = self._group(group)
        if seg_id not in g.segments:
            raise KeyError(seg_id)
        p0, p1 = point(*p0), point(*p1)
        g.segments[seg_id] = (p0, p1)
        g.root = _insert(_delete(g.root, seg_id), _leaf(seg_id, p0, p1))
        self.epoch += 1

    def remove_segment(self, group, seg_id):
        g = self._group(group)
        if seg_id not in g.segments:
            raise KeyError(seg_id)
        del g.segments[seg_id]
        g.root = _delete(g.root, seg_id)
        self.epoch += 1

    # -- versioning -------------------------------------------------------

    def snapshot(self):
        """Capture an immutable version of all group trees (O(1))."""
        return Version(self.epoch,
                       {name: g.root for name, g in self._groups.items()})

    # -- queries ------------------------------------------------------------

    def query(self, group_a, group_b=None, *, radius=None, mode='all',
              version=None, cancel_token=None, step_budget=256):
        """Run a closest-pair query to completion; returns ``Result``."""
        q = Query(self, group_a, group_b, radius=radius, mode=mode,
                  version=version, cancel_token=cancel_token)
        while not q.done:
            q.step(step_budget)
        return q.result()

    def excluded(self, group_a, group_b, id_a, id_b):
        key = frozenset((id_a, id_b))
        if key in self.exclusions:
            return True
        for name in (group_a, group_b):
            if name is not None and key in self._groups[name].exclusions:
                return True
        return False


class CancelToken:
    """Cooperative cancellation flag checked between query steps."""

    __slots__ = ('cancelled',)

    def __init__(self):
        self.cancelled = False

    def cancel(self):
        self.cancelled = True


class Query:
    """Incremental best-first closest-pair search over two tree versions.

    The traversal holds its ``Version`` for its whole lifetime, so every
    node it may still visit stays reachable; nodes are released only
    after the query (and the version) is dropped.
    """

    def __init__(self, scene, group_a, group_b=None, *, radius=None,
                 mode='all', version=None, cancel_token=None):
        if mode not in ('all', 'representative'):
            raise ValueError("mode must be 'all' or 'representative'")
        self._scene = scene
        self._group_a = group_a
        self._group_b = group_b if group_b is not None else group_a
        self._self_pair = self._group_b == group_a
        self._mode = mode
        self._cancel_token = cancel_token
        self._version = version if version is not None else scene.snapshot()
        self._best = frac(radius) ** 2 if radius is not None else None
        self._evidence = {}
        self._heap = []
        self._counter = itertools.count()
        self._cancelled = False
        self.done = False
        root_a = self._version.roots.get(self._group_a)
        root_b = root_a if self._self_pair \
            else self._version.roots.get(self._group_b)
        if root_a is not None and root_b is not None:
            self._push(root_a, root_b)

    # -- traversal -------------------------------------------------------

    def _push(self, na, nb):
        if self._best is not None and _bbox_dist2(na.bbox, nb.bbox) > self._best:
            return
        heapq.heappush(self._heap,
                       (_bbox_dist2(na.bbox, nb.bbox), next(self._counter),
                        na, nb))

    def cancel(self):
        self._cancelled = True

    def step(self, budget=1):
        """Process up to ``budget`` heap entries.  Returns ``self.done``."""
        if self._cancelled or (self._cancel_token is not None
                               and self._cancel_token.cancelled):
            self._cancelled = True
            raise QueryCancelled()
        steps = 0
        while steps < budget and self._heap and not self.done:
            lb, _, na, nb = heapq.heappop(self._heap)
            if self._best is not None and lb > self._best:
                self.done = True  # heap is ordered; nothing can tie/beat
                break
            self._expand(na, nb)
            steps += 1
        if not self._heap:
            self.done = True
        return self.done

    def _expand(self, na, nb):
        if na.is_leaf and nb.is_leaf:
            self._evaluate(na, nb)
            return
        if na is nb:
            # unordered self-pair: (l,l), (l,r), (r,r) covers each pair once
            self._push(na.left, na.left)
            self._push(na.left, na.right)
            self._push(na.right, na.right)
        elif na.is_leaf or (not nb.is_leaf and nb.size > na.size):
            self._push(na, nb.left)
            self._push(na, nb.right)
        else:
            self._push(na.left, nb)
            self._push(na.right, nb)

    def _evaluate(self, la, lb):
        id_a, id_b = la.seg_id, lb.seg_id
        if self._self_pair:
            if id_a == id_b:
                return
            if id_b < id_a:
                id_a, id_b = id_b, id_a
                la, lb = lb, la
        if self._scene.excluded(self._group_a, self._group_b, id_a, id_b):
            return
        d2, t_a, t_b = seg_seg_dist2(la.p0, la.p1, lb.p0, lb.p1)
        if self._best is not None and d2 > self._best:
            return
        key = (id_a, id_b)
        if self._best is None or d2 < self._best:
            self._best = d2
            self._evidence = {key: self._make_evidence(
                key, la, lb, t_a, t_b, d2)}
        elif d2 == self._best and key not in self._evidence:
            self._evidence[key] = self._make_evidence(
                key, la, lb, t_a, t_b, d2)

    @staticmethod
    def _make_evidence(key, la, lb, t_a, t_b, d2):
        pa = (la.p0[0] + t_a * (la.p1[0] - la.p0[0]),
              la.p0[1] + t_a * (la.p1[1] - la.p0[1]))
        pb = (lb.p0[0] + t_b * (lb.p1[0] - lb.p0[0]),
              lb.p0[1] + t_b * (lb.p1[1] - lb.p0[1]))
        return Evidence(key[0], key[1], t_a, t_b, pa, pb, d2)

    # -- outcome -----------------------------------------------------------

    def result(self):
        if not self.done:
            raise RuntimeError('query not finished')
        if not self._evidence:
            return Result(None, ())
        ordered = tuple(self._evidence[k] for k in sorted(self._evidence))
        if self._mode == 'representative':
            # stable representative: canonical smallest (seg_a, seg_b)
            return Result(ordered[0].dist2, ordered[:1])
        return Result(ordered[0].dist2, ordered)
