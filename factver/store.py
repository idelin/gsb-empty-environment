"""版本化事实存储：保留全部旧版本与字段来源，批量更正原子生效。"""
from __future__ import annotations

from .model import Correction, FactVersion, FieldValue


class UnknownFactError(KeyError):
    pass


class VersionConflictError(RuntimeError):
    pass


class BatchCorrectionError(RuntimeError):
    pass


class FactStore:
    def __init__(self) -> None:
        self._facts: dict[str, list[FactVersion]] = {}

    def put(self, fact_id: str, fields: dict[str, tuple[object, str]]) -> FactVersion:
        """新建事实（version=1）。fields: {字段名: (值, 来源)}"""
        if fact_id in self._facts:
            raise BatchCorrectionError(f"fact already exists: {fact_id}")
        fv = FactVersion(
            fact_id, 1,
            {name: FieldValue(value, source) for name, (value, source) in fields.items()},
        )
        self._facts[fact_id] = [fv]
        return fv

    def get(self, fact_id: str, version: int | None = None) -> FactVersion:
        hist = self._facts.get(fact_id)
        if hist is None:
            raise UnknownFactError(fact_id)
        if version is None:
            return hist[-1]
        for fv in hist:
            if fv.version == version:
                return fv
        raise UnknownFactError(f"{fact_id}@v{version}")

    def current_version(self, fact_id: str) -> int:
        return self.get(fact_id).version

    def history(self, fact_id: str) -> list[FactVersion]:
        if fact_id not in self._facts:
            raise UnknownFactError(fact_id)
        return list(self._facts[fact_id])

    def correct(self, correction: Correction) -> FactVersion:
        return self.batch_correct([correction])[0]

    def batch_correct(self, corrections: list[Correction]) -> list[FactVersion]:
        """原子批量更正：任一校验失败则整体不生效，保持原版本。"""
        planned = [self._plan(c) for c in corrections]  # 先全部校验，失败即抛
        for fv in planned:                              # 校验通过后才提交
            self._facts[fv.fact_id].append(fv)
        return planned

    def _plan(self, c: Correction) -> FactVersion:
        if c.fact_id not in self._facts:
            raise UnknownFactError(c.fact_id)
        cur = self._facts[c.fact_id][-1]
        if c.expected_version is not None and c.expected_version != cur.version:
            raise VersionConflictError(
                f"{c.fact_id}: expected v{c.expected_version}, current v{cur.version}"
            )
        overlap = set(c.changes) & set(c.removals)
        if overlap:
            raise BatchCorrectionError(
                f"fields both changed and removed: {sorted(overlap)}"
            )
        fields = dict(cur.fields)  # 未触及的字段保留原值与原来源
        for name in c.removals:
            fields.pop(name, None)
        for name, (value, source) in c.changes.items():
            fields[name] = FieldValue(value, source)
        return FactVersion(c.fact_id, cur.version + 1, fields)
