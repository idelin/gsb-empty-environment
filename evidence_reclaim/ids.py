"""规范化序列化与稳定标识符。

所有事实、规则、步骤、证书均通过 canonical() 得到确定性 JSON，
再以 sha256 派生稳定 ID。相同内容必然得到相同 ID，可跨进程重放。
"""

from __future__ import annotations

import hashlib
import json
from typing import Any


def canonical(obj: Any) -> str:
    """确定性 JSON 序列化（键排序、无空白、UTF-8）。"""
    return json.dumps(obj, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


def digest(obj: Any) -> str:
    """对任意可规范化对象计算 sha256 摘要（十六进制）。"""
    return hashlib.sha256(canonical(obj).encode("utf-8")).hexdigest()


def stable_id(kind: str, obj: Any) -> str:
    """生成形如 ``<kind>:<sha256 前 16 位>`` 的稳定 ID。"""
    return f"{kind}:{digest(obj)[:16]}"
