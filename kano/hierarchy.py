"""Per-field finite generalization hierarchies (rooted trees).

A hierarchy maps each raw (leaf) value to a chain of ancestors up to a
single root. Generalization level ``h`` means: replace a raw value by its
ancestor at height ``h`` (height 0 = the leaf itself, i.e. no
generalization). Levels are therefore finite: ``0 .. max_height``.
"""

from __future__ import annotations


class GeneralizationHierarchy:
    """A rooted tree of categorical generalization for one field."""

    def __init__(self, field: str, parent: dict[str, str | None]):
        self.field = field
        self._parent = dict(parent)
        roots = [n for n, p in self._parent.items() if p is None]
        if len(roots) != 1:
            raise ValueError(f"{field}: expected exactly 1 root, got {len(roots)}")
        for node, par in self._parent.items():
            if par is not None and par not in self._parent:
                raise ValueError(f"{field}: parent {par!r} of {node!r} is not a node")
        self.root = roots[0]
        self._children: dict[str, list[str]] = {n: [] for n in self._parent}
        for node, par in self._parent.items():
            if par is not None:
                self._children[par].append(node)
        self.leaves = frozenset(n for n, c in self._children.items() if not c)
        if not self.leaves:
            raise ValueError(f"{field}: hierarchy has no leaves")
        # chain[leaf] = (leaf, parent, ..., root); index = height.
        self._chain: dict[str, tuple[str, ...]] = {}
        for leaf in self.leaves:
            chain = [leaf]
            seen = {leaf}
            node = leaf
            while self._parent[node] is not None:
                node = self._parent[node]
                if node in seen:
                    raise ValueError(f"{field}: cycle detected at {node!r}")
                seen.add(node)
                chain.append(node)
            self._chain[leaf] = tuple(chain)
        self.max_level = max(len(c) for c in self._chain.values()) - 1
        # leaves_under[node] used by the loss function.
        self._leaves_under: dict[str, int] = {}
        for node in self._parent:
            self._leaves_under[node] = self._count_leaves(node)

    def _count_leaves(self, node: str) -> int:
        if not self._children[node]:
            return 1
        return sum(self._count_leaves(c) for c in self._children[node])

    @classmethod
    def from_nested(cls, field: str, spec: dict) -> "GeneralizationHierarchy":
        """Build from nested dicts, e.g. {"*": {"young": {"18", "25"}}}.

        Leaf values may be given as sets/lists of strings or as empty dicts.
        """
        parent: dict[str, str | None] = {}

        def walk(node: str, par: str | None, subtree) -> None:
            if node in parent:
                raise ValueError(f"{field}: duplicate node {node!r} (not a tree)")
            parent[node] = par
            if isinstance(subtree, dict):
                for child, sub in subtree.items():
                    walk(child, node, sub)
            else:
                for child in subtree:
                    walk(child, node, {})

        if len(spec) != 1:
            raise ValueError(f"{field}: spec must have exactly one root")
        (root, subtree), = spec.items()
        walk(root, None, subtree)
        return cls(field, parent)

    def generalize(self, value: str, level: int) -> str:
        """Map a raw leaf value to its ancestor at the given height."""
        if not 0 <= level <= self.max_level:
            raise ValueError(f"level {level} out of range 0..{self.max_level}")
        chain = self._chain[value]
        return chain[min(level, len(chain) - 1)]

    def leaves_under(self, node: str) -> int:
        return self._leaves_under[node]

    def check_value(self, value: str) -> None:
        if value not in self._chain:
            raise ValueError(f"{self.field}: {value!r} is not a leaf value")
