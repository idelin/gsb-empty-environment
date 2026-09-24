"""Dynamic bounding volume hierarchy (AABB tree) with exact arithmetic.

Coordinates may be ``int`` or ``fractions.Fraction`` (or any exact,
totally-ordered number type).  All topological predicates (intersection,
containment, distance comparison) are evaluated with exact arithmetic --
no epsilon tolerances are used anywhere.

Conventions
-----------
* Boxes are *closed* axis-aligned boxes: two boxes that merely touch on a
  face/edge/corner are considered intersecting.
* Degenerate boxes (zero extent in one or more dimensions, e.g. points)
  are fully supported.
* Object ids must be hashable and mutually orderable; ties in nearest
  queries are broken by ascending id, and intersection results are
  returned sorted by id, so all query results are deterministic.
* Nearest queries report *squared* distances so results stay exact
  (``int``/``Fraction``) and never involve square roots.
"""

from __future__ import annotations

import heapq
import itertools
from bisect import insort
from typing import Any, Hashable, Iterable, Iterator, Sequence

__all__ = ["AABB", "DynamicBVH"]


class AABB:
    """Closed axis-aligned bounding box in any number of dimensions."""

    __slots__ = ("lo", "hi")

    def __init__(self, lo: Sequence, hi: Sequence):
        lo = tuple(lo)
        hi = tuple(hi)
        if len(lo) != len(hi):
            raise ValueError("lo and hi must have the same dimension")
        if not lo:
            raise ValueError("box must have at least one dimension")
        for a, b in zip(lo, hi):
            if a > b:
                raise ValueError(f"lo must be <= hi componentwise, got {lo} vs {hi}")
        self.lo = lo
        self.hi = hi

    @property
    def dim(self) -> int:
        return len(self.lo)

    def intersects(self, other: "AABB") -> bool:
        """Closed-box intersection: touching boxes intersect."""
        return all(
            a <= d and c <= b
            for a, b, c, d in zip(self.lo, self.hi, other.lo, other.hi)
        )

    def contains(self, other: "AABB") -> bool:
        return all(a <= c and d <= b for a, b, c, d in zip(self.lo, self.hi, other.lo, other.hi))

    def union(self, other: "AABB") -> "AABB":
        return AABB(
            tuple(min(a, c) for a, c in zip(self.lo, other.lo)),
            tuple(max(b, d) for b, d in zip(self.hi, other.hi)),
        )

    def perimeter(self):
        """Sum of extents; used as the tree cost heuristic (exact)."""
        return sum(h - l for l, h in zip(self.lo, self.hi))

    def distance_sq_to_point(self, point: Sequence):
        """Exact squared distance from a point to this (closed) box."""
        total = 0
        for p, l, h in zip(point, self.lo, self.hi):
            if p < l:
                d = l - p
                total += d * d
            elif p > h:
                d = p - h
                total += d * d
        return total

    def __eq__(self, other):
        return isinstance(other, AABB) and self.lo == other.lo and self.hi == other.hi

    def __repr__(self):
        return f"AABB(lo={self.lo!r}, hi={self.hi!r})"


class _Node:
    __slots__ = ("box", "parent", "c1", "c2", "height", "obj")

    def __init__(self):
        self.box = None
        self.parent = None
        self.c1 = None
        self.c2 = None
        self.height = 0  # leaves have height 0
        self.obj = None

    @property
    def is_leaf(self) -> bool:
        return self.c1 is None


class DynamicBVH:
    """Dynamic AABB tree supporting insert/remove/move and exact queries."""

    def __init__(self):
        self._root: _Node | None = None
        self._leaves: dict[Hashable, _Node] = {}
        self._counter = itertools.count()  # deterministic heap tie-break

    # ------------------------------------------------------------------ API

    def __len__(self) -> int:
        return len(self._leaves)

    def __contains__(self, obj: Hashable) -> bool:
        return obj in self._leaves

    def __iter__(self) -> Iterator[Hashable]:
        return iter(sorted(self._leaves))

    def get(self, obj: Hashable) -> AABB:
        """Return the stored box for *obj* (raises KeyError if absent)."""
        return self._leaves[obj].box

    def insert(self, obj: Hashable, box: AABB | tuple[Sequence, Sequence]) -> None:
        """Insert *obj* with the given box. Raises ValueError if present."""
        box = self._coerce_box(box)
        if obj in self._leaves:
            raise ValueError(f"object already present: {obj!r}")
        self._check_dim(box)
        leaf = _Node()
        leaf.box = box
        leaf.obj = obj
        self._leaves[obj] = leaf
        self._insert_leaf(leaf)

    def remove(self, obj: Hashable) -> None:
        """Remove *obj*. Raises KeyError if absent."""
        leaf = self._leaves.pop(obj)  # KeyError propagates, as documented
        self._remove_leaf(leaf)

    def move(self, obj: Hashable, new_box: AABB | tuple[Sequence, Sequence]) -> None:
        """Move *obj* to *new_box*.

        Implemented as remove + insert, so after a move the object is
        guaranteed to be found only in its new region, never the old one.
        Raises KeyError if *obj* is absent (tree left unchanged).
        """
        box = self._coerce_box(new_box)
        self._check_dim(box)
        leaf = self._leaves.pop(obj)  # KeyError before any mutation
        self._remove_leaf(leaf)
        try:
            self.insert(obj, box)
        except Exception:
            self._leaves[obj] = leaf
            self._insert_leaf(leaf)  # restore on unexpected failure
            raise

    def query(self, box: AABB | tuple[Sequence, Sequence]) -> list:
        """Return ids whose boxes intersect *box*, sorted by id.

        Empty tree -> empty list.  Intersection is the closed-box test.
        """
        box = self._coerce_box(box)
        self._check_dim(box)
        hits = []
        stack = [self._root] if self._root is not None else []
        while stack:
            node = stack.pop()
            if not node.box.intersects(box):
                continue
            if node.is_leaf:
                hits.append(node.obj)
            else:
                stack.append(node.c1)
                stack.append(node.c2)
        hits.sort()
        return hits

    def nearest(self, point: Sequence, k: int = 1) -> list[tuple[Hashable, Any]]:
        """Return up to *k* nearest objects to *point*.

        Returns a list of ``(obj_id, squared_distance)`` sorted by
        ``(squared_distance, obj_id)`` -- exact arithmetic, deterministic
        tie-breaking by id.  Empty tree or ``k <= 0`` -> empty list.
        """
        point = tuple(point)
        if k <= 0 or self._root is None:
            return []
        if self._root is not None and len(point) != self._root.box.dim:
            raise ValueError("point dimension does not match tree dimension")

        results: list[tuple[Any, Hashable]] = []  # kept sorted by (dist, id)
        bound = None  # worst (largest) distance currently in a full result set
        heap = [(self._root.box.distance_sq_to_point(point), next(self._counter), self._root)]
        while heap:
            dist, _, node = heapq.heappop(heap)
            if bound is not None and dist > bound:
                break  # heap is ordered; nothing closer can appear
            if node.is_leaf:
                insort(results, (dist, node.obj))
                if len(results) > k:
                    results.pop()
                if len(results) == k:
                    bound = results[-1][0]
            else:
                for child in (node.c1, node.c2):
                    d = child.box.distance_sq_to_point(point)
                    if bound is None or d <= bound:
                        heapq.heappush(heap, (d, next(self._counter), child))
        return [(obj, dist) for dist, obj in results]

    # ------------------------------------------------------- verification

    def check_invariants(self) -> None:
        """Assert the structural and bounding invariants of the tree.

        * every internal node's box equals the union of its children's boxes
        * heights are consistent; parent pointers are consistent
        * the set of leaves matches the object map exactly
        """
        seen = set()

        def walk(node, parent):
            if node.parent is not parent:
                raise AssertionError("broken parent pointer")
            if node.is_leaf:
                if node.height != 0 or node.obj is None:
                    raise AssertionError("malformed leaf")
                seen.add(node.obj)
                return node.box, 0
            if node.c2 is None or node.obj is not None:
                raise AssertionError("malformed internal node")
            b1, h1 = walk(node.c1, node)
            b2, h2 = walk(node.c2, node)
            expected = b1.union(b2)
            if node.box != expected:
                raise AssertionError(f"bounding invariant violated: {node.box} != {expected}")
            height = 1 + max(h1, h2)
            if node.height != height:
                raise AssertionError("height invariant violated")
            return node.box, height

        if self._root is not None:
            walk(self._root, None)
        if seen != set(self._leaves):
            raise AssertionError("leaf set does not match object map")

    # ---------------------------------------------------------- internals

    @staticmethod
    def _coerce_box(box) -> AABB:
        if isinstance(box, AABB):
            return box
        lo, hi = box
        return AABB(lo, hi)

    def _check_dim(self, box: AABB) -> None:
        if self._root is not None and box.dim != self._root.box.dim:
            raise ValueError(
                f"dimension mismatch: tree is {self._root.box.dim}D, box is {box.dim}D"
            )

    def _insert_leaf(self, leaf: _Node) -> None:
        if self._root is None:
            self._root = leaf
            return

        leaf_box = leaf.box
        # Greedy best-sibling descent (Box2D-style surface-area heuristic).
        node = self._root
        while not node.is_leaf:
            area = node.box.perimeter()
            combined_area = node.box.union(leaf_box).perimeter()
            cost = 2 * combined_area
            inheritance = 2 * (combined_area - area)

            c1, c2 = node.c1, node.c2
            cost1 = c1.box.union(leaf_box).perimeter() - (0 if c1.is_leaf else c1.box.perimeter()) + inheritance
            cost2 = c2.box.union(leaf_box).perimeter() - (0 if c2.is_leaf else c2.box.perimeter()) + inheritance
            if cost < cost1 and cost < cost2:
                break
            node = c1 if cost1 <= cost2 else c2  # ties -> c1 (deterministic)

        sibling = node
        old_parent = sibling.parent
        new_parent = _Node()
        new_parent.box = sibling.box.union(leaf_box)
        new_parent.parent = old_parent
        new_parent.c1 = sibling
        new_parent.c2 = leaf
        new_parent.height = sibling.height + 1
        sibling.parent = new_parent
        leaf.parent = new_parent
        if old_parent is None:
            self._root = new_parent
        elif old_parent.c1 is sibling:
            old_parent.c1 = new_parent
        else:
            old_parent.c2 = new_parent

        self._fix_upward(new_parent.parent)

    def _remove_leaf(self, leaf: _Node) -> None:
        parent = leaf.parent
        if parent is None:
            self._root = None
            return
        grand = parent.parent
        sibling = parent.c2 if parent.c1 is leaf else parent.c1
        if grand is None:
            self._root = sibling
            sibling.parent = None
        else:
            if grand.c1 is parent:
                grand.c1 = sibling
            else:
                grand.c2 = sibling
            sibling.parent = grand
            self._fix_upward(grand)

    def _fix_upward(self, node: _Node | None) -> None:
        while node is not None:
            node = self._balance(node)
            c1, c2 = node.c1, node.c2
            node.height = 1 + max(c1.height, c2.height)
            node.box = c1.box.union(c2.box)
            node = node.parent

    def _balance(self, a: _Node) -> _Node:
        if a.is_leaf or a.height < 2:
            return a
        b, c = a.c1, a.c2
        diff = c.height - b.height
        if diff > 1:
            return self._rotate(a, c, b, is_c=True)
        if diff < -1:
            return self._rotate(a, b, c, is_c=False)
        return a

    def _rotate(self, a: _Node, up: _Node, other: _Node, is_c: bool) -> _Node:
        """Rotate *up* (the taller child of *a*) above *a*."""
        x, y = up.c1, up.c2
        parent = a.parent
        up.parent = parent
        a.parent = up
        if parent is not None:
            if parent.c1 is a:
                parent.c1 = up
            else:
                parent.c2 = up
        else:
            self._root = up

        if is_c:
            up.c1 = a
            slot = "c2"
        else:
            up.c2 = a
            slot = "c1"

        if x.height > y.height:
            setattr(up, slot, x)
            if is_c:
                a.c2 = y
            else:
                a.c1 = y
            y.parent = a
            a.box = other.box.union(y.box)
            up.box = a.box.union(x.box)
            a.height = 1 + max(other.height, y.height)
            up.height = 1 + max(a.height, x.height)
        else:
            setattr(up, slot, y)
            if is_c:
                a.c2 = x
            else:
                a.c1 = x
            x.parent = a
            a.box = other.box.union(x.box)
            up.box = a.box.union(y.box)
            a.height = 1 + max(other.height, x.height)
            up.height = 1 + max(a.height, y.height)
        return up
