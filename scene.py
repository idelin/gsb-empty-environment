"""Hierarchical 2D affine scene graph with exact inverse hit testing.

Features:
- World-matrix cache per node, keyed on the version chain of ALL
  ancestors (not just the node's own version), so mutating any ancestor
  invalidates exactly its descendant subtree.
- Atomic reparenting with an optional keep-world-position mode.
  Reparenting under a singular parent world matrix in keep-world mode
  is rejected with UnsolvableReparentError before any state changes.
- Versioned snapshots: every mutation bumps the scene version and is
  recorded in an append-only per-node history, so hit queries can be
  answered for any past version (multi-view snapshots).
- Exact rational arithmetic; singular world matrices yield an explicit
  NOT_INVERTIBLE hit status instead of a pseudo-inverse.
"""

from __future__ import annotations

from dataclasses import dataclass
from enum import Enum, auto
from typing import List, Optional, Tuple

from affine2d import IDENTITY, Mat2D


class HitStatus(Enum):
    OK = auto()
    NOT_INVERTIBLE = auto()


@dataclass(frozen=True)
class HitResult:
    status: HitStatus
    point: Optional[Tuple]  # local-space point when status is OK
    version: int

    @property
    def ok(self) -> bool:
        return self.status is HitStatus.OK


class SceneError(Exception):
    pass


class CycleError(SceneError):
    """Reparenting would create a cycle in the hierarchy."""


class UnsolvableReparentError(SceneError):
    """keep_world=True requested but the new parent's world matrix is
    singular, so no exact local transform can preserve the world pose."""


class UnknownVersionError(SceneError):
    """Queried version is in the future or predates the node's creation."""


class Node:
    __slots__ = (
        "scene", "name", "parent", "local", "local_version",
        "_history", "_cache_key", "_cache_mat",
    )

    def __init__(self, scene: "Scene", name: str,
                 parent: Optional["Node"], local: Mat2D, version: int):
        self.scene = scene
        self.name = name
        self.parent = parent
        self.local = local
        self.local_version = 0
        # Append-only log of (scene_version, parent, local).
        self._history: List[Tuple[int, Optional["Node"], Mat2D]] = [
            (version, parent, local)
        ]
        self._cache_key = None
        self._cache_mat: Optional[Mat2D] = None

    def __repr__(self) -> str:
        return f"Node({self.name!r})"


class Snapshot:
    """Immutable view of the scene at a fixed version."""

    __slots__ = ("scene", "version")

    def __init__(self, scene: "Scene", version: int):
        self.scene = scene
        self.version = version

    def world_matrix(self, node: Node) -> Mat2D:
        return self.scene.world_matrix(node, version=self.version)

    def hit(self, node: Node, x, y) -> HitResult:
        return self.scene.hit(node, x, y, version=self.version)

    def local_to_world(self, node: Node, x, y) -> Tuple:
        return self.scene.local_to_world(node, x, y, version=self.version)


class Scene:
    def __init__(self) -> None:
        self._version = 0
        self.nodes: List[Node] = []
        self.cache_hits = 0
        self.cache_misses = 0

    @property
    def version(self) -> int:
        return self._version

    # ---------------------------------------------------------- mutations

    def create_node(self, name: str, parent: Optional[Node] = None,
                    local: Mat2D = IDENTITY) -> Node:
        node = Node(self, name, parent, local, self._version)
        self.nodes.append(node)
        return node

    def set_local(self, node: Node, local: Mat2D) -> None:
        self._version += 1
        node.local = local
        node.local_version += 1
        node._history.append((self._version, node.parent, local))

    def reparent(self, node: Node, new_parent: Optional[Node],
                 keep_world: bool = False) -> None:
        """Atomically re-attach `node` under `new_parent`.

        With keep_world=True the node's local transform is solved as
        inv(new_parent_world) @ node_world so its world pose is
        preserved. If the new parent's world matrix is singular this is
        unsolvable: UnsolvableReparentError is raised and no state is
        modified (the operation is atomic).
        """
        ancestor = new_parent
        while ancestor is not None:
            if ancestor is node:
                raise CycleError(
                    f"reparenting {node.name} under {new_parent.name} "
                    "would create a cycle"
                )
            ancestor = ancestor.parent

        if keep_world:
            parent_world = (self.world_matrix(new_parent)
                            if new_parent is not None else IDENTITY)
            inv = parent_world.inverse()
            if inv is None:
                raise UnsolvableReparentError(
                    f"cannot keep world pose of {node.name}: new parent "
                    f"{new_parent.name} has a singular world matrix "
                    f"(det == 0)"
                )
            new_local = inv @ self.world_matrix(node)
        else:
            new_local = node.local

        self._version += 1
        node.parent = new_parent
        node.local = new_local
        node.local_version += 1
        node._history.append((self._version, new_parent, new_local))

    # ------------------------------------------------------------ queries

    def snapshot(self) -> Snapshot:
        return Snapshot(self, self._version)

    def world_matrix(self, node: Node, version: Optional[int] = None) -> Mat2D:
        if version is None:
            version = self._version
        if version > self._version:
            raise UnknownVersionError(
                f"version {version} is in the future (current {self._version})"
            )
        if version == self._version:
            return self._world_current(node)
        return self._world_at(node, version)

    def hit(self, node: Node, x, y, version: Optional[int] = None) -> HitResult:
        """Map a world-space point into the node's local space.

        Returns HitStatus.NOT_INVERTIBLE when the node's world matrix is
        singular -- an exact, explicitly defined degenerate result.
        """
        if version is None:
            version = self._version
        inv = self.world_matrix(node, version).inverse()
        if inv is None:
            return HitResult(HitStatus.NOT_INVERTIBLE, None, version)
        return HitResult(HitStatus.OK, inv.apply(x, y), version)

    def local_to_world(self, node: Node, x, y,
                       version: Optional[int] = None) -> Tuple:
        return self.world_matrix(node, version).apply(x, y)

    # ----------------------------------------------------------- internal

    def _world_current(self, node: Node) -> Mat2D:
        # Cache key binds the identity AND local version of every node on
        # the path to the root, so a change to any ancestor (or a
        # reparent anywhere above) misses the cache -- never stale.
        path: List[Node] = []
        cursor = node
        while cursor is not None:
            path.append(cursor)
            cursor = cursor.parent
        key = tuple((nd, nd.local_version) for nd in path)
        if node._cache_key == key:
            self.cache_hits += 1
            return node._cache_mat
        self.cache_misses += 1
        world = IDENTITY
        for nd in reversed(path):
            world = world @ nd.local
        node._cache_key = key
        node._cache_mat = world
        return world

    def _state_at(self, node: Node, version: int):
        for record in reversed(node._history):
            if record[0] <= version:
                return record
        raise UnknownVersionError(
            f"node {node.name} did not exist at version {version}"
        )

    def _world_at(self, node: Node, version: int) -> Mat2D:
        world = IDENTITY
        cursor: Optional[Node] = node
        while cursor is not None:
            _, parent, local = self._state_at(cursor, version)
            world = local @ world
            cursor = parent
        return world
