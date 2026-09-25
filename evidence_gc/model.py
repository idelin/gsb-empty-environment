"""基础模型工具：规范化 JSON、哈希、实例键。"""

from __future__ import annotations

import hashlib
import json
from typing import Any

InstanceKey = tuple[str, int]


def canonical(obj: Any) -> str:
    """确定性 JSON 序列化（键排序、紧凑分隔符）。"""
    return json.dumps(obj, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


def sha256_text(text: str) -> str:
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


def content_hash(payload: Any) -> str:
    return sha256_text(canonical(payload))


def synthetic_content(eid: str, version: int) -> str:
    """合成数据内容：完全由 (eid, version) 决定，保证可重放。"""
    return f"synthetic-content:{eid}:v{version}"


def key_of(eid: str, version: int) -> InstanceKey:
    return (eid, version)


def key_str(key: InstanceKey) -> str:
    return f"{key[0]}@v{key[1]}"
