"""Subtype lattice used to judge whether type changes are compatible.

Parameter (input) widening is compatible because callers may still pass
the old, narrower values.  Return/output widening is incompatible because
existing callers may rely on the narrower type.  Output narrowing and
input narrowing have the opposite meanings.

The built-in lattice mirrors the Python numeric/collection hierarchy:
``bool`` < ``int`` < ``float`` < ``complex`` < ``object`` and the common
collection base classes.  Unknown type names are treated as only related
to themselves and to ``object`` (everything is an object).
"""

from __future__ import annotations

from typing import Dict, Iterable, List, Optional, Tuple

DEFAULT_PARENTS: Dict[str, Tuple[str, ...]] = {
    "bool": ("int",),
    "int": ("float",),
    "float": ("complex",),
    "complex": ("object",),
    "str": ("object",),
    "bytes": ("object",),
    "bytearray": ("object",),
    "list": ("object",),
    "tuple": ("object",),
    "set": ("object",),
    "frozenset": ("object",),
    "dict": ("object",),
    "None": ("object",),
    "NoneType": ("None",),
    "object": (),
}

# relation(a, b) outcomes
EQUAL = "equal"
SUBTYPE = "subtype"      # a is a strict subtype of b
SUPERTYPE = "supertype"  # a is a strict supertype of b
UNRELATED = "unrelated"
UNKNOWN = "unknown"      # cannot decide (missing/unknown types)


class TypeLattice:
    def __init__(self, extra_parents: Optional[Dict[str, Iterable[str]]] = None):
        self._parents: Dict[str, Tuple[str, ...]] = {
            name: tuple(parents) for name, parents in DEFAULT_PARENTS.items()
        }
        if extra_parents:
            for name, parents in extra_parents.items():
                parents = tuple(parents)
                if name == "object":
                    raise ValueError("the root type 'object' cannot be redefined")
                self._parents[name] = parents
        self._known: set = {"object"}
        for name, parents in self._parents.items():
            self._known.add(name)
            self._known.update(parents)

    def parents(self, name: str) -> Tuple[str, ...]:
        return self._parents.get(name, ("object",))

    def is_subtype(self, child: str, parent: str) -> Optional[bool]:
        """True/False when decidable, None when a type is not in the lattice."""
        if child == parent:
            return True
        if child not in self._known or parent not in self._known:
            return None
        stack = list(self._parents[child])
        seen = set()
        while stack:
            current = stack.pop()
            if current == parent:
                return True
            if current in seen:
                continue
            seen.add(current)
            stack.extend(self._parents.get(current, ("object",)))
        return False

    def relation(self, old: str, new: str) -> str:
        if old == new:
            return EQUAL
        sub = self.is_subtype(old, new)
        if sub is True:
            return SUBTYPE
        if sub is False:
            reverse = self.is_subtype(new, old)
            if reverse is True:
                return SUPERTYPE
            return UNRELATED
        return UNKNOWN

    def parameter_compatible(self, old: str, new: str) -> bool:
        """Input position: same or widened accepted types are compatible."""
        match = self.relation(old, new)
        return match in (EQUAL, SUBTYPE)

    def output_compatible(self, old: str, new: str) -> bool:
        """Output position: same or narrowed produced types are compatible."""
        match = self.relation(old, new)
        return match in (EQUAL, SUPERTYPE)

    def chain(self, child: str, ancestor: str) -> List[str]:
        """Return a shortest parent chain including both endpoints."""
        if child == ancestor:
            return [child]
        queue: List[Tuple[str, List[str]]] = [(child, [child])]
        seen = {child}
        while queue:
            current, path = queue.pop(0)
            for parent in self.parents(current):
                if parent == ancestor:
                    return path + [parent]
                if parent not in seen:
                    seen.add(parent)
                    queue.append((parent, path + [parent]))
        return []
