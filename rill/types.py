"""Type representations and structural subtyping for Rill.

Record types are structural and field-order independent.  The variance of a
record type depends on the *access mode* of the position where it is used:

* a ``readonly`` (read) position is covariant in record width and, recursively,
  in nested record fields -- a wider record may be supplied (more fields are
  harmless when nothing is written);
* a ``writable`` (mutation) position is invariant: the supplied record must
  match field-by-field, otherwise writing could plant a record that lacks a
  field another alias expects.
"""

from __future__ import annotations

from dataclasses import dataclass, field as dc_field


@dataclass(frozen=True)
class RecField:
    name: str
    type: "Type"


@dataclass(frozen=True)
class Type:
    name: str = ""
    fields: tuple[RecField, ...] = ()

    @staticmethod
    def record(fields: dict[str, "Type"] | list[tuple[str, "Type"]]) -> "Type":
        if isinstance(fields, dict):
            items = sorted(fields.items())
        else:
            items = sorted(fields, key=lambda pair: pair[0])
        return Type("", tuple(RecField(n, t) for n, t in items))

    @property
    def is_record(self) -> bool:
        return not self.name

    def field_map(self) -> dict[str, "Type"]:
        return {f.name: f.type for f in self.fields}

    def get(self, name: str) -> "Type | None":
        for f in self.fields:
            if f.name == name:
                return f.type
        return None

    def __str__(self) -> str:
        if not self.is_record:
            return self.name
        return "{" + ", ".join(f"{f.name}: {f.type}" for f in self.fields) + "}"


UNIT = Type("Unit")
INT = Type("Int")
STR = Type("Str")
BOOL = Type("Bool")


def same_type(a: Type, b: Type) -> bool:
    """Exact (invariant) type equality, independent of declared field order."""
    if a.is_record != b.is_record:
        return False
    if not a.is_record:
        return a.name == b.name
    fa, fb = a.field_map(), b.field_map()
    if set(fa) != set(fb):
        return False
    return all(same_type(fa[k], fb[k]) for k in fa)


def is_subtype(given: Type, expected: Type, readonly: bool) -> bool:
    """Structural subtyping.

    ``readonly`` selects the read variance: in read positions extra fields are
    allowed and nested record fields are compared read-covariantly.  In write
    positions the comparison is exact (invariant).
    """
    if not readonly:
        return same_type(given, expected)
    if not expected.is_record:
        return given.name == expected.name
    if not given.is_record:
        return False
    gf = given.field_map()
    for f in expected.fields:
        actual = gf.get(f.name)
        if actual is None:
            return False
        if not is_subtype(actual, f.type, readonly=True):
            return False
    return True
