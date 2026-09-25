"""Versioned immutable bounding-volume tree (AVL) with resource accounting.

Design invariants:

* Nodes are immutable once created.  Every edit (insert / move / delete)
  produces a *new* root; unchanged subtrees are shared by identity with
  the previous version.  A local move therefore only rebuilds the nodes
  on the path from the affected leaf to the root (plus O(1) rotation
  nodes per rebalanced level).

* Old versions stay valid as long as someone references their root.
  Rebalancing only *creates* nodes; it never mutates or frees nodes that
  an in-flight query (holding a snapshot root) is reading.  Reclamation
  is delegated to reference counting / GC, and ``NodePool`` keeps an
  exact live-node count so tests can prove old versions are released.

* Internal nodes cache the exact union bbox of their children, so bbox
  repairs after a local move propagate only along the edited path.

The tree is leaf-oriented: segment data lives in leaves keyed by
``seg_id``; internal nodes store ``key`` = max key of the left subtree
for navigation and ``max_key`` = max key of the whole subtree.
"""

from __future__ import annotations

from geometry import bbox_of_segment, bbox_union


class NodePool:
    """Exact accounting of live tree nodes.

    ``live`` is incremented on allocation and decremented when a node is
    reclaimed (refcount/GC).  Tests compare ``live`` against the number
    of nodes reachable from the current version(s) to prove that old
    versions are released exactly when their last reference disappears.
    """

    __slots__ = ("live", "created", "freed")

    def __init__(self):
        self.live = 0
        self.created = 0
        self.freed = 0

    def _alloc(self):
        self.live += 1
        self.created += 1

    def _free(self):
        self.live -= 1
        self.freed += 1


class Node:
    __slots__ = (
        "key", "max_key", "seg_id", "p0", "p1", "bbox",
        "left", "right", "height", "size", "_pool", "__weakref__",
    )

    def __init__(self, pool, key, max_key, seg_id, p0, p1, bbox,
                 left, right, height, size):
        pool._alloc()
        self._pool = pool
        self.key = key
        self.max_key = max_key
        self.seg_id = seg_id
        self.p0 = p0
        self.p1 = p1
        self.bbox = bbox
        self.left = left
        self.right = right
        self.height = height
        self.size = size

    def __del__(self):
        try:
            self._pool._free()
        except Exception:
            pass  # interpreter shutdown

    @property
    def is_leaf(self) -> bool:
        return self.seg_id is not None

    def __repr__(self):
        if self.is_leaf:
            return f"Leaf({self.seg_id})"
        return f"Internal(key={self.key}, h={self.height}, n={self.size})"


def make_leaf(pool, seg_id, p0, p1):
    return Node(
        pool, key=seg_id, max_key=seg_id, seg_id=seg_id, p0=p0, p1=p1,
        bbox=bbox_of_segment(p0, p1), left=None, right=None,
        height=1, size=1,
    )


def make_internal(pool, left, right):
    return Node(
        pool, key=left.max_key, max_key=right.max_key, seg_id=None,
        p0=None, p1=None, bbox=bbox_union(left.bbox, right.bbox),
        left=left, right=right,
        height=max(left.height, right.height) + 1,
        size=left.size + right.size,
    )


def _rot_left(pool, n):
    r = n.right
    return make_internal(pool, make_internal(pool, n.left, r.left), r.right)


def _rot_right(pool, n):
    l = n.left
    return make_internal(pool, l.left, make_internal(pool, l.right, n.right))


def _rebalance(pool, n):
    bf = n.left.height - n.right.height
    if bf > 1:
        left = n.left
        if left.left.height < left.right.height:
            left = _rot_left(pool, left)
            n = make_internal(pool, left, n.right)
        return _rot_right(pool, n)
    if bf < -1:
        right = n.right
        if right.right.height < right.left.height:
            right = _rot_right(pool, right)
            n = make_internal(pool, n.left, right)
        return _rot_left(pool, n)
    return n


def tree_insert(pool, root, seg_id, p0, p1):
    """Insert or replace (move) a leaf; returns the new root.

    Only nodes on the search path (and rotation nodes) are newly
    allocated; every other node is shared with the old version.
    """
    if root is None:
        return make_leaf(pool, seg_id, p0, p1)
    if root.is_leaf:
        if seg_id == root.seg_id:
            return make_leaf(pool, seg_id, p0, p1)
        leaf = make_leaf(pool, seg_id, p0, p1)
        if seg_id < root.seg_id:
            return make_internal(pool, leaf, root)
        return make_internal(pool, root, leaf)
    if seg_id <= root.key:
        new_left = tree_insert(pool, root.left, seg_id, p0, p1)
        return _rebalance(pool, make_internal(pool, new_left, root.right))
    new_right = tree_insert(pool, root.right, seg_id, p0, p1)
    return _rebalance(pool, make_internal(pool, root.left, new_right))


def tree_delete(pool, root, seg_id):
    """Remove a leaf; returns the new root (None when the tree empties)."""
    if root.is_leaf:
        if root.seg_id != seg_id:
            raise KeyError(seg_id)
        return None
    if seg_id <= root.key:
        new_left = tree_delete(pool, root.left, seg_id)
        if new_left is None:
            return root.right
        return _rebalance(pool, make_internal(pool, new_left, root.right))
    new_right = tree_delete(pool, root.right, seg_id)
    if new_right is None:
        return root.left
    return _rebalance(pool, make_internal(pool, root.left, new_right))


def tree_find(root, seg_id):
    """Return the leaf for ``seg_id`` or None."""
    n = root
    while n is not None and not n.is_leaf:
        n = n.left if seg_id <= n.key else n.right
    if n is not None and n.seg_id == seg_id:
        return n
    return None


def iter_leaves(root):
    """In-order leaf iteration (ascending seg_id)."""
    stack = [root] if root is not None else []
    while stack:
        n = stack.pop()
        if n.is_leaf:
            yield n
        else:
            stack.append(n.right)
            stack.append(n.left)


def count_nodes(root) -> int:
    """Number of nodes reachable from ``root`` (shared subtrees counted
    once per tree; callers summing over versions should deduplicate)."""
    if root is None:
        return 0
    return 1 + (0 if root.is_leaf else
                count_nodes(root.left) + count_nodes(root.right))


def check_avl(root):
    """Structural validation helper for tests."""
    if root is None or root.is_leaf:
        return
    assert root.left.max_key == root.key
    assert root.right.max_key == root.max_key
    assert root.height == max(root.left.height, root.right.height) + 1
    assert root.size == root.left.size + root.right.size
    assert abs(root.left.height - root.right.height) <= 1
    assert root.bbox == bbox_union(root.left.bbox, root.right.bbox)
    check_avl(root.left)
    check_avl(root.right)
