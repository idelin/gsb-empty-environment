"""Compressed radix tree (Patricia trie) core.

Each node owns the label of its incoming edge: ``node.edge`` is the
string that must follow its parent's full path to reach this node.  A
node terminating a stored key carries an integer ``weight``; pure
routing nodes have ``weight is None``.

Compression invariant: every non-root node with ``weight is None`` has
at least two children.  Deletions that violate this rule merge the
lone child's edge back into the node (the merge is performed bottom-up
by :func:`delete`).
"""

from __future__ import annotations

from typing import Iterator, Optional


class RadixNode:
    __slots__ = ("edge", "weight", "children", "size")

    def __init__(
        self,
        edge: str = "",
        weight: Optional[int] = None,
        children: Optional[dict[str, "RadixNode"]] = None,
    ) -> None:
        self.edge: str = edge
        self.weight: Optional[int] = weight
        self.children: dict[str, RadixNode] = (
            children if children is not None else {}
        )
        self.size: int = 1 if weight is not None else 0


def _common_prefix(a: str, b: str) -> int:
    n = min(len(a), len(b))
    i = 0
    while i < n and a[i] == b[i]:
        i += 1
    return i


def _refresh(node: RadixNode) -> None:
    node.size = (1 if node.weight is not None else 0) + sum(
        child.size for child in node.children.values()
    )


def insert(root: RadixNode, key: str, weight: int) -> None:
    """Set ``key`` to ``weight`` under ``root`` (insert or overwrite)."""
    node = root
    suffix = key
    while True:
        if suffix == "":
            node.weight = weight
            _refresh(node)
            return
        first = suffix[0]
        child = node.children.get(first)
        if child is None:
            node.children[first] = RadixNode(suffix, weight)
            _refresh(node)
            return
        cp = _common_prefix(suffix, child.edge)
        if cp == len(child.edge):
            if cp == len(suffix):
                child.weight = weight
                _refresh(child)
                return
            suffix = suffix[cp:]
            node = child
            continue
        # Divergence (or suffix ends) inside the shared edge: split it.
        mid = RadixNode(child.edge[:cp])
        child.edge = child.edge[cp:]
        mid.children[child.edge[0]] = child
        if cp < len(suffix):
            mid.children[suffix[cp]] = RadixNode(suffix[cp:], weight)
        else:
            mid.weight = weight
        node.children[first] = mid
        _refresh(mid)
        _refresh(node)
        return


def _maybe_merge(node: RadixNode) -> Optional[RadixNode]:
    """Compress a node after deletion.

    Returns the node that should remain in its parent's child slot, or
    None when the slot is removed.  The root (edge == "") is never
    merged away.
    """
    if node.edge == "":
        return node
    if node.weight is None and not node.children:
        return None
    if node.weight is None and len(node.children) == 1:
        only = next(iter(node.children.values()))
        only.edge = node.edge + only.edge
        return only
    return node


def _delete(node: RadixNode, suffix: str) -> tuple[bool, Optional[RadixNode]]:
    if suffix == "":
        if node.weight is None:
            return False, node
        node.weight = None
        _refresh(node)
        return True, _maybe_merge(node)

    child = node.children.get(suffix[0])
    if child is None:
        return False, node
    cp = _common_prefix(suffix, child.edge)
    if cp < len(child.edge):
        # Diverges mid-edge, or the key stops before the edge ends:
        # no terminal exists exactly at ``suffix``.
        return False, node
    found, replacement = _delete(child, suffix[cp:])
    if not found:
        return False, node
    if replacement is None:
        del node.children[suffix[0]]
    else:
        node.children[suffix[0]] = replacement
    _refresh(node)
    return True, _maybe_merge(node)


def delete(root: RadixNode, key: str) -> bool:
    """Delete ``key``; return True if it was present."""
    found, _ = _delete(root, key)
    return found


def get(root: RadixNode, key: str) -> Optional[int]:
    node = root
    suffix = key
    while True:
        if suffix == "":
            return node.weight
        child = node.children.get(suffix[0])
        if child is None:
            return None
        cp = _common_prefix(suffix, child.edge)
        if cp < len(child.edge):
            return None
        suffix = suffix[cp:]
        node = child


def locate(root: RadixNode, prefix: str) -> Optional[tuple[RadixNode, str]]:
    """Return ``(subtree root, full path)`` for keys starting with prefix.

    A match requires the trie path to cover the *whole* prefix: a node
    whose edge diverges from, or runs past, the prefix is not a match.
    """
    node = root
    suffix = prefix
    path = ""
    while suffix:
        child = node.children.get(suffix[0])
        if child is None:
            return None
        cp = _common_prefix(suffix, child.edge)
        if cp == 0:
            return None
        if cp < len(suffix):
            if cp < len(child.edge):
                return None
            suffix = suffix[cp:]
            path += child.edge
            node = child
            continue
        # Whole suffix consumed; edge must not extend past the prefix.
        if cp < len(child.edge):
            return None
        return child, path + child.edge
    return node, path


def iter_subtree(node: RadixNode, path: str) -> Iterator[tuple[str, Optional[int]]]:
    """Yield ``(full key, weight)`` for every terminal under ``node``."""
    if node.weight is not None:
        yield path, node.weight
    for child in node.children.values():
        yield from iter_subtree(child, path + child.edge)
