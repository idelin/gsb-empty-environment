"""Finite representation of label sets over a possibly infinite universe.

Used both for prerelease *channels* and for *target conditions*.  A label
set is stored as ``(include, exclude)`` and denotes
``(universe if include is None else include) - exclude``.  ``include=None``
means "all labels", which keeps complements and differences exact without
ever enumerating the universe.
"""

from __future__ import annotations


class LabelSet:
    __slots__ = ("include", "exclude")

    def __init__(self, include=None, exclude=()):
        if include is not None:
            include = frozenset(include)
            # Normalise: with a finite include-list the exclude-list is
            # folded in, so the canonical form is (include - exclude, ()).
            include = include - frozenset(exclude)
            exclude = frozenset()
        else:
            exclude = frozenset(exclude)
        self.include = include
        self.exclude = exclude

    # -- constructors ---------------------------------------------------
    @classmethod
    def all(cls) -> "LabelSet":
        return cls(None)

    @classmethod
    def none(cls) -> "LabelSet":
        return cls(frozenset())

    @classmethod
    def of(cls, labels) -> "LabelSet":
        return cls(frozenset(labels))

    # -- predicates -----------------------------------------------------
    def is_all(self) -> bool:
        return self.include is None and not self.exclude

    def is_empty(self) -> bool:
        # With include=None the universe is treated as unbounded, so the
        # set is never empty (finitely many exclusions cannot exhaust it).
        return self.include is not None and not self.include

    def contains(self, label: str) -> bool:
        if self.include is None:
            return label not in self.exclude
        return label in self.include

    def is_subset(self, other: "LabelSet") -> bool:
        return self.difference(other).is_empty()

    # -- algebra ----------------------------------------------------------
    def union(self, other: "LabelSet") -> "LabelSet":
        if self.include is None and other.include is None:
            return LabelSet(None, self.exclude & other.exclude)
        if self.include is None:
            # ALL-e1 union F  ==  ALL - (e1 - F)
            return LabelSet(None, self.exclude - other.include)
        if other.include is None:
            return other.union(self)
        return LabelSet(self.include | other.include)

    def intersect(self, other: "LabelSet") -> "LabelSet":
        if self.include is None and other.include is None:
            return LabelSet(None, self.exclude | other.exclude)
        if self.include is None:
            return LabelSet(other.include - self.exclude)
        if other.include is None:
            return LabelSet(self.include - other.exclude)
        return LabelSet(self.include & other.include)

    def complement(self) -> "LabelSet":
        if self.include is None:
            return LabelSet(self.exclude)
        return LabelSet(None, self.include)

    def difference(self, other: "LabelSet") -> "LabelSet":
        return self.intersect(other.complement())

    # -- dunder -----------------------------------------------------------
    def __eq__(self, other):
        return isinstance(other, LabelSet) and \
            self.include == other.include and self.exclude == other.exclude

    def __hash__(self):
        return hash((self.include, self.exclude))

    def to_dict(self):
        if self.include is None:
            if not self.exclude:
                return {"all": True}
            return {"all_except": sorted(self.exclude)}
        return {"only": sorted(self.include)}

    @classmethod
    def from_dict(cls, data) -> "LabelSet":
        if data is None or data == {"all": True}:
            return cls.all()
        if "all_except" in data:
            return cls(None, data["all_except"])
        return cls.of(data.get("only", ()))

    def __repr__(self):
        if self.include is None:
            return "LabelSet.all()" if not self.exclude else \
                f"LabelSet(all_except={sorted(self.exclude)})"
        return f"LabelSet.of({sorted(self.include)})"
