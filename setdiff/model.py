"""核心数据模型：带版本的记录（存活值或删除墓碑）。"""

from __future__ import annotations

import base64
from dataclasses import dataclass
from typing import Any, Optional


@dataclass(frozen=True)
class Record:
    """一条带版本的记录。

    value 为 None 表示删除墓碑（tombstone），否则为存活值（bytes）。
    version 为单调递增的逻辑版本号，用于最后写入者胜出（LWW）合并。
    """

    key: str
    value: Optional[bytes]
    version: int

    def __post_init__(self) -> None:
        if self.version < 0:
            raise ValueError("version 必须为非负整数")

    @property
    def deleted(self) -> bool:
        """是否为删除墓碑。"""
        return self.value is None

    def to_json(self) -> dict[str, Any]:
        """序列化为 JSON 兼容字典。

        同时给出 value（UTF-8 可解码时）与 value_b64（始终给出）。
        解析时 value_b64 优先，保证任意字节值均可无损往返。
        """
        obj: dict[str, Any] = {
            "key": self.key,
            "version": self.version,
            "deleted": self.deleted,
            "value": None,
            "value_b64": None,
        }
        if self.value is not None:
            obj["value_b64"] = base64.b64encode(self.value).decode("ascii")
            try:
                obj["value"] = self.value.decode("utf-8")
            except UnicodeDecodeError:
                obj["value"] = None
        return obj

    @staticmethod
    def from_json(obj: dict[str, Any]) -> "Record":
        """从 JSON 字典解析记录；value_b64 优先于 value。"""
        key = obj["key"]
        version = int(obj["version"])
        if obj.get("deleted") or (obj.get("value") is None and obj.get("value_b64") is None):
            return Record(key=key, value=None, version=version)
        if obj.get("value_b64") is not None:
            value = base64.b64decode(obj["value_b64"])
        else:
            value = str(obj["value"]).encode("utf-8")
        return Record(key=key, value=value, version=version)
