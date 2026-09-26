"""Core data model: documents, text queries, facet selections.

Semantics that must stay explicit (also documented in README.md):
  * Inside one facet dimension, selected values combine with **OR**.
  * Across different dimensions, selections combine with **AND**.
  * ``MISSING`` (attribute key absent) and ``EMPTY`` (attribute present
    with an empty value list) are two distinct, separately countable
    buckets; either may be selected like a normal value.
"""

from __future__ import annotations

from dataclasses import dataclass, field


class SpecialValue:
    """Sentinel selecting a special bucket instead of a concrete value."""

    __slots__ = ("name",)

    def __init__(self, name: str) -> None:
        self.name = name

    def __repr__(self) -> str:  # pragma: no cover - cosmetic
        return self.name


#: The attribute key is absent from the document.
MISSING = SpecialValue("MISSING")
#: The attribute key is present but its value list is empty.
EMPTY = SpecialValue("EMPTY")


@dataclass(frozen=True)
class Document:
    """An immutable document: external id, UTF-8 text, multi-valued attrs.

    ``attributes`` maps a dimension name to a tuple of string values.
    A missing key means MISSING; a present-but-empty tuple means EMPTY.
    Duplicate values inside one tuple are allowed on input and are
    de-duplicated at index time (same doc + same value counts once).
    """

    doc_id: str
    text: str
    attributes: dict[str, tuple[str, ...]] = field(default_factory=dict)


@dataclass(frozen=True)
class TextQuery:
    """Boolean main query over tokens.

    * ``all_of``: every listed term must occur (AND).
    * ``any_of``: at least one listed term must occur (OR); empty = no
      constraint.
    * ``none_of``: no listed term may occur (NOT).

    Each term is itself tokenized; a term yielding several tokens requires
    all of them (AND). A term yielding zero tokens is a ``ValueError``.
    An entirely empty query matches every live document.
    """

    all_of: tuple[str, ...] = ()
    any_of: tuple[str, ...] = ()
    none_of: tuple[str, ...] = ()


#: A facet selection maps dimension name -> tuple of selected values.
#: Values are strings or the MISSING / EMPTY sentinels.
Selection = dict[str, tuple]
