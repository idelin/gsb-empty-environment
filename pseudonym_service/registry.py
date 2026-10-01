"""Pseudonym registry: records, indexes, and the rotation (old->new) map."""
from __future__ import annotations

from dataclasses import dataclass, asdict


@dataclass(frozen=True)
class Record:
    pseudonym: str
    domain: str
    subject: str
    key_version: int
    epoch: int


class Registry:
    def __init__(self):
        self.records: dict[str, Record] = {}
        self.by_subject: dict[tuple[int, str, str], str] = {}
        self.rotation_map: dict[str, str] = {}

    def add(self, record: Record) -> None:
        if record.pseudonym in self.records:
            raise ValueError(f"duplicate pseudonym: {record.pseudonym}")
        key = (record.key_version, record.domain, record.subject)
        if key in self.by_subject:
            raise ValueError(f"duplicate subject for version: {key}")
        self.records[record.pseudonym] = record
        self.by_subject[key] = record.pseudonym

    def owner(self, pseudonym: str) -> Record | None:
        return self.records.get(pseudonym)

    def lookup_subject(self, version: int, domain: str, subject: str) -> Record | None:
        pseudonym = self.by_subject.get((version, domain, subject))
        return self.records.get(pseudonym) if pseudonym else None

    def subjects_for_version(self, version: int) -> list[tuple[str, str]]:
        return sorted((d, s) for (v, d, s) in self.by_subject if v == version)

    def to_dict(self) -> dict:
        return {
            "records": [asdict(r) for r in self.records.values()],
            "rotation_map": dict(self.rotation_map),
        }

    @classmethod
    def from_dict(cls, data: dict) -> "Registry":
        registry = cls()
        for raw in data["records"]:
            registry.add(Record(**raw))
        registry.rotation_map.update(data["rotation_map"])
        return registry
