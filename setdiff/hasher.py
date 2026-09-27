"""可注入的哈希器：生产用 SHA-256，测试可注入恒定哈希强制碰撞。"""

from __future__ import annotations

import hashlib
from typing import Protocol, runtime_checkable

DIGEST_HEX_LEN = 64  # 摘要统一为 64 个十六进制字符（256 位）


@runtime_checkable
class Hasher(Protocol):
    """哈希器协议：任意实现只需提供 name 与 digest(data) -> hex str。"""

    name: str

    def digest(self, data: bytes) -> str:
        """返回 64 字符的十六进制摘要。"""
        ...


class Sha256Hasher:
    """生产环境默认哈希器（SHA-256）。"""

    name = "sha256"

    def digest(self, data: bytes) -> str:
        return hashlib.sha256(data).hexdigest()


class ConstantHasher:
    """恒定哈希器：所有输入返回同一摘要，用于测试强制碰撞。

    在此哈希器下，任意两个不同集合的摘要都相等，因此
    “摘要相等即集合相同”的结论不再成立——协议必须依赖
    精确模式的逐条核验才能发现差异。
    """

    def __init__(self, value: int = 0) -> None:
        self.name = f"constant-{value}"
        self._digest = format(value & ((1 << 256) - 1), "064x")

    def digest(self, data: bytes) -> str:
        return self._digest
