"""版本化键值存储：维护 键 -> Record 映射（含删除墓碑），支持 LWW 合并。"""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from typing import Any, Iterable, Iterator, Optional

from .model import Record


def _lww_winner(local: Optional[Record], remote: Record) -> Optional[Record]:
    """确定性 LWW 仲裁：返回应收下的记录，None 表示保留本地。

    规则（保证全序、可收敛）：
      1. 版本高者胜出（墓碑版本高于存活值时，合并结果为删除）；
      2. 版本相同，墓碑优先于存活值；
      3. 版本相同且同为存活值，值字节大者胜出（确定性平局裁决）。
    """
    if local is None:
        return remote
    if remote.version != local.version:
        return remote if remote.version > local.version else None
    if remote.deleted != local.deleted:
        return remote if remote.deleted else None
    if remote.deleted:
        return None  # 双方同为墓碑，等价
    assert remote.value is not None and local.value is not None
    return remote if remote.value > local.value else None


@dataclass
class MergeReport:
    """一次合并的可复核报告。"""

    inserted: list[str] = field(default_factory=list)
    updated: list[str] = field(default_factory=list)
    kept_local: list[str] = field(default_factory=list)

    @property
    def applied(self) -> int:
        return len(self.inserted) + len(self.updated)

    def to_json(self) -> dict[str, Any]:
        return {
            "inserted": self.inserted,
            "updated": self.updated,
            "kept_local": self.kept_local,
            "applied": self.applied,
        }


class VersionedStore:
    """键 -> Record 的版本化映射，删除以墓碑形式保留。"""

    def __init__(self, records: Iterable[Record] = ()) -> None:
        self._records: dict[str, Record] = {}
        for record in records:
            self._records[record.key] = record

    def __len__(self) -> int:
        return len(self._records)

    def __contains__(self, key: str) -> bool:
        return key in self._records

    def __iter__(self) -> Iterator[Record]:
        return iter(self.records())

    def put(self, key: str, value: bytes, version: int) -> Record:
        """写入存活值（无条件覆盖，调用方负责版本单调性）。"""
        record = Record(key=key, value=value, version=version)
        self._records[key] = record
        return record

    def delete(self, key: str, version: int) -> Record:
        """写入删除墓碑。"""
        record = Record(key=key, value=None, version=version)
        self._records[key] = record
        return record

    def get(self, key: str) -> Optional[Record]:
        return self._records.get(key)

    def records(self) -> list[Record]:
        """按键排序的全部记录（含墓碑），顺序确定。"""
        return [self._records[key] for key in sorted(self._records)]

    def live_records(self) -> list[Record]:
        return [record for record in self.records() if not record.deleted]

    def tombstones(self) -> list[Record]:
        return [record for record in self.records() if record.deleted]

    def merge(self, records: Iterable[Record]) -> MergeReport:
        """按确定性 LWW 规则合并远端记录，返回可复核报告。"""
        report = MergeReport()
        for remote in records:
            local = self._records.get(remote.key)
            winner = _lww_winner(local, remote)
            if winner is None:
                report.kept_local.append(remote.key)
            else:
                self._records[remote.key] = winner
                if local is None:
                    report.inserted.append(remote.key)
                else:
                    report.updated.append(remote.key)
        return report

    def to_json(self) -> dict[str, Any]:
        return {"records": [record.to_json() for record in self.records()]}

    @staticmethod
    def from_json(obj: dict[str, Any]) -> "VersionedStore":
        return VersionedStore(Record.from_json(item) for item in obj.get("records", []))

    def dump_json(self) -> str:
        return json.dumps(self.to_json(), ensure_ascii=False, indent=2, sort_keys=True)

    @staticmethod
    def load_json(text: str) -> "VersionedStore":
        return VersionedStore.from_json(json.loads(text))
