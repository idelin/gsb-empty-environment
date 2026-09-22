from dataclasses import dataclass

from .errors import BindError


@dataclass(frozen=True)
class Relation:
    columns: tuple[str, ...]
    rows: tuple[tuple[object, ...], ...]

    def __post_init__(self):
        if not self.columns:
            raise BindError("relation must have at least one column")
        for row in self.rows:
            if len(row) != len(self.columns):
                raise BindError("relation row does not match relation columns")


class Database:
    def __init__(self, relations):
        self.relations = dict(relations)

    def get(self, name):
        if name in self.relations:
            return self.relations[name]
        lowered = name.lower()
        for key, relation in self.relations.items():
            if key.lower() == lowered:
                return relation
        raise BindError(f"unknown table {name!r}")
