"""Multi-field sort keys with null placement and stable id tie-break."""
from dataclasses import dataclass
from functools import cmp_to_key


@dataclass(frozen=True)
class FieldSpec:
    name: str
    desc: bool = False
    nulls_last: bool = True


class SortSpec:
    """Ordered list of FieldSpec; ties always broken by entry id (asc)."""

    def __init__(self, fields):
        fields = tuple(fields)
        if not fields:
            raise ValueError("SortSpec needs at least one field")
        self.fields = fields

    @staticmethod
    def _cmp_value(a, b, field):
        if a is None and b is None:
            return 0
        if a is None:
            return 1 if field.nulls_last else -1
        if b is None:
            return -1 if field.nulls_last else 1
        if a == b:
            return 0
        less = a < b
        if field.desc:
            less = not less
        return -1 if less else 1

    def compare(self, ea, eb):
        for field in self.fields:
            c = self._cmp_value(ea.values.get(field.name),
                                eb.values.get(field.name), field)
            if c:
                return c
        if ea.id == eb.id:
            return 0
        return -1 if ea.id < eb.id else 1

    def key(self):
        return cmp_to_key(self.compare)

    def to_json(self):
        return [{"name": f.name, "desc": f.desc, "nulls_last": f.nulls_last}
                for f in self.fields]

    @classmethod
    def from_json(cls, data):
        return cls(FieldSpec(**d) for d in data)
