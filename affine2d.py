"""Exact 2D affine transform hierarchy with inverse hit testing.

All arithmetic uses ``fractions.Fraction`` so coordinates stay exact
(integers / rationals). Degenerate (singular) matrices are detected via an
exact zero determinant -- no epsilon tolerances anywhere.

Layout convention: column vectors, matrix stored as
    | a c e |
    | b d f |
    | 0 0 1 |
so ``world = parent_world @ local`` and points map as ``M @ p``.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import Dict, List, Optional, Tuple

Number = "int | Fraction"
Point = Tuple[Fraction, Fraction]


def _frac(value) -> Fraction:
    if isinstance(value, Fraction):
        return value
    if isinstance(value, int):
        return Fraction(value)
    raise TypeError(
        f"coordinates must be int or Fraction, got {type(value).__name__}"
    )


class SingularTransformError(Exception):
    """Raised when an operation requires an invertible matrix but the
    matrix at hand is singular (determinant exactly zero)."""


class Matrix:
    """Immutable exact 2D affine transform."""

    __slots__ = ("a", "b", "c", "d", "e", "f")

    def __init__(self, a=1, b=0, c=0, d=1, e=0, f=0):
        self.a = _frac(a)
        self.b = _frac(b)
        self.c = _frac(c)
        self.d = _frac(d)
        self.e = _frac(e)
        self.f = _frac(f)

    # -- constructors ----------------------------------------------------
    @staticmethod
    def identity() -> "Matrix":
        return Matrix()

    @staticmethod
    def translation(tx, ty) -> "Matrix":
        return Matrix(1, 0, 0, 1, tx, ty)

    @staticmethod
    def scale(sx, sy=None) -> "Matrix":
        if sy is None:
            sy = sx
        return Matrix(sx, 0, 0, sy, 0, 0)

    @staticmethod
    def rotation_exact(cos, sin) -> "Matrix":
        """Rotation by an angle whose cosine/sine are given exactly.

        Only exact (rational) cos/sin pairs are accepted, so e.g. 90-degree
        turns are exact while arbitrary angles must be supplied by the
        caller as rationals -- no float approximation sneaks in.
        """
        cos, sin = _frac(cos), _frac(sin)
        return Matrix(cos, sin, -sin, cos, 0, 0)

    @staticmethod
    def rotation_quarter(turns: int) -> "Matrix":
        """Exact rotation by ``turns`` * 90 degrees counter-clockwise."""
        t = turns % 4
        if t == 0:
            return Matrix.identity()
        if t == 1:
            return Matrix(0, 1, -1, 0, 0, 0)
        if t == 2:
            return Matrix(-1, 0, 0, -1, 0, 0)
        return Matrix(0, -1, 1, 0, 0, 0)

    @staticmethod
    def shear(shx, shy) -> "Matrix":
        return Matrix(1, shy, shx, 1, 0, 0)

    # -- algebra ---------------------------------------------------------
    def __matmul__(self, other: "Matrix") -> "Matrix":
        """Composition: ``self @ other`` applies ``other`` first."""
        return Matrix(
            self.a * other.a + self.c * other.b,
            self.b * other.a + self.d * other.b,
            self.a * other.c + self.c * other.d,
            self.b * other.c + self.d * other.d,
            self.a * other.e + self.c * other.f + self.e,
            self.b * other.e + self.d * other.f + self.f,
        )

    def apply(self, point) -> Point:
        x, y = _frac(point[0]), _frac(point[1])
        return (self.a * x + self.c * y + self.e,
                self.b * x + self.d * y + self.f)

    def determinant(self) -> Fraction:
        return self.a * self.d - self.b * self.c

    def is_singular(self) -> bool:
        # Exact test: degenerate iff determinant is exactly zero.
        return self.determinant() == 0

    def inverse(self) -> Optional["Matrix"]:
        """Exact inverse, or ``None`` when singular.

        A singular matrix (e.g. scale by 0) collapses a dimension and has
        no exact inverse; we never substitute a pseudo-inverse.
        """
        det = self.determinant()
        if det == 0:
            return None
        ia, ib = self.d / det, -self.b / det
        ic, id_ = -self.c / det, self.a / det
        ie = -(ia * self.e + ic * self.f)
        if_ = -(ib * self.e + id_ * self.f)
        return Matrix(ia, ib, ic, id_, ie, if_)

    # -- dunder helpers --------------------------------------------------
    def __eq__(self, other) -> bool:
        if not isinstance(other, Matrix):
            return NotImplemented
        return (self.a, self.b, self.c, self.d, self.e, self.f) == \
               (other.a, other.b, other.c, other.d, other.e, other.f)

    def __hash__(self):
        return hash((self.a, self.b, self.c, self.d, self.e, self.f))

    def __repr__(self):
        return (f"Matrix(a={self.a}, b={self.b}, c={self.c}, "
                f"d={self.d}, e={self.e}, f={self.f})")


@dataclass(frozen=True)
class HitResult:
    """Result of an inverse hit query (world point -> local point)."""
    ok: bool
    point: Optional[Point]
    reason: Optional[str] = None


class Node:
    """A node in the transform hierarchy."""

    __slots__ = ("name", "_scene", "_parent", "_children", "_local", "_version")

    def __init__(self, scene: "Scene", name: str,
                 parent: Optional["Node"], local: Matrix):
        self.name = name
        self._scene = scene
        self._parent = parent
        self._children: List[Node] = []
        self._local = local
        self._version = 0

    @property
    def parent(self) -> Optional["Node"]:
        return self._parent

    @property
    def children(self) -> Tuple["Node", ...]:
        return tuple(self._children)

    @property
    def local(self) -> Matrix:
        return self._local

    @property
    def version(self) -> int:
        return self._version

    def is_ancestor_of(self, other: "Node") -> bool:
        cur = other._parent
        while cur is not None:
            if cur is self:
                return True
            cur = cur._parent
        return False

    def __repr__(self):
        return f"Node({self.name!r})"


class Scene:
    """Owns the node hierarchy and the world-matrix cache.

    Cache entries are keyed by node and validated against a *fingerprint*
    of the versions of every ancestor on the path to the root -- so an
    ancestor edit invalidates exactly its own subtree, never siblings.
    """

    def __init__(self):
        self._nodes: List[Node] = []
        self._cache: Dict[Node, Tuple[tuple, Matrix]] = {}

    # -- hierarchy construction -----------------------------------------
    def create_node(self, name: str, parent: Optional[Node] = None,
                    local: Optional[Matrix] = None) -> Node:
        if parent is not None and parent._scene is not self:
            raise ValueError("parent belongs to a different scene")
        node = Node(self, name, parent, local or Matrix.identity())
        if parent is not None:
            parent._children.append(node)
        self._nodes.append(node)
        return node

    # -- mutation --------------------------------------------------------
    def set_local(self, node: Node, local: Matrix) -> None:
        self._check(node)
        node._local = local
        node._version += 1  # ancestors' fingerprints change for subtree

    def reparent(self, node: Node, new_parent: Optional[Node],
                 keep_world: bool = False) -> None:
        """Atomically re-attach ``node`` under ``new_parent``.

        With ``keep_world=True`` the local transform is recomputed so the
        node's world transform (and hence its whole subtree's world
        placement) is preserved. If the new parent's world matrix is
        singular, that recomputation is impossible: the operation raises
        ``SingularTransformError`` and the hierarchy is left untouched.
        """
        self._check(node)
        if new_parent is not None:
            self._check(new_parent)
            if new_parent is node or node.is_ancestor_of(new_parent):
                raise ValueError(
                    f"reparenting {node.name!r} under {new_parent.name!r} "
                    "would create a cycle")

        new_local = node._local
        if keep_world:
            parent_world = (self.world_matrix(new_parent)
                            if new_parent is not None else Matrix.identity())
            inv = parent_world.inverse()
            if inv is None:
                raise SingularTransformError(
                    f"cannot keep world position of {node.name!r}: new "
                    f"parent {new_parent.name if new_parent else None!r} "
                    "has a singular world matrix (determinant == 0), so "
                    "no exact local transform exists")
            new_local = inv @ self.world_matrix(node)

        # All validation done -- mutate atomically from here.
        if node._parent is not None:
            node._parent._children.remove(node)
        node._parent = new_parent
        if new_parent is not None:
            new_parent._children.append(node)
        node._local = new_local
        node._version += 1

    # -- queries ---------------------------------------------------------
    def world_matrix(self, node: Optional[Node]) -> Matrix:
        if node is None:
            return Matrix.identity()
        self._check(node)
        fingerprint = self._fingerprint(node)
        entry = self._cache.get(node)
        if entry is not None and entry[0] == fingerprint:
            return entry[1]
        matrix = self.world_matrix(node._parent) @ node._local
        self._cache[node] = (fingerprint, matrix)
        return matrix

    def hit(self, node: Node, world_point) -> HitResult:
        """Map a world-space point into ``node``'s local space."""
        world = self.world_matrix(node)
        inv = world.inverse()
        if inv is None:
            return HitResult(
                ok=False, point=None,
                reason=(f"world matrix of {node.name!r} is singular "
                        "(determinant == 0); inverse hit is undefined"))
        return HitResult(ok=True, point=inv.apply(world_point))

    def is_cache_valid(self, node: Node) -> bool:
        """Introspection helper: is the cached world matrix still fresh?"""
        entry = self._cache.get(node)
        return entry is not None and entry[0] == self._fingerprint(node)

    def snapshot(self) -> "Snapshot":
        """Capture an immutable view of the current hierarchy version."""
        return Snapshot(self)

    # -- internals -------------------------------------------------------
    def _check(self, node: Node) -> None:
        if node._scene is not self:
            raise ValueError("node belongs to a different scene")

    def _fingerprint(self, node: Node) -> tuple:
        """Versions of every node on the root->node path.

        The cache depends on the whole ancestor chain, not just the
        node's own version counter.
        """
        parts = []
        cur: Optional[Node] = node
        while cur is not None:
            parts.append((id(cur), cur._version))
            cur = cur._parent
        return tuple(parts)


class Snapshot:
    """Immutable view of a scene at capture time (old-version queries)."""

    def __init__(self, scene: Scene):
        self._state: Dict[Node, Tuple[Optional[Node], Matrix]] = {
            node: (node._parent, node._local) for node in scene._nodes
        }
        self._cache: Dict[Node, Matrix] = {}

    def world_matrix(self, node: Node) -> Matrix:
        if node not in self._state:
            raise ValueError("node did not exist when the snapshot was taken")
        cached = self._cache.get(node)
        if cached is not None:
            return cached
        parent, local = self._state[node]
        matrix = (self.world_matrix(parent) @ local) if parent is not None else local
        self._cache[node] = matrix
        return matrix

    def hit(self, node: Node, world_point) -> HitResult:
        world = self.world_matrix(node)
        inv = world.inverse()
        if inv is None:
            return HitResult(
                ok=False, point=None,
                reason=(f"world matrix of {node.name!r} is singular "
                        "(determinant == 0); inverse hit is undefined"))
        return HitResult(ok=True, point=inv.apply(world_point))
