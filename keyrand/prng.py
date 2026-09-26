"""显式键驱动 PRNG（无全局状态，JAX 风格）。

设计要点
--------
- 随机性完全由不可变的 ``Key`` 值决定，不存在任何全局/可变状态。
- ``split`` 从一个键派生任意多个互相独立的子键；``fold_in`` 把一个
  非负整数（如稳定样本 ID、节点名哈希）混合进键中。
- 元素级随机位通过 ``random_bits(key, index)`` 以计数器方式生成，
  因此同一 (key, index) 无论以何种顺序、何种分块求值都得到相同结果，
  即“状态独立子流”。

位混合使用 SplitMix64 的最终混淆器（Stafford 变体常数），公式：

    z  = (x + 0x9E3779B97F4A7C15) mod 2^64
    z  = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9 mod 2^64
    z  = (z ^ (z >> 27)) * 0x94D049BB133111EB mod 2^64
    out = z ^ (z >> 31)
"""
from __future__ import annotations

MASK64 = (1 << 64) - 1
GOLDEN_GAMMA = 0x9E3779B97F4A7C15


def _mix64(x: int) -> int:
    z = (x + GOLDEN_GAMMA) & MASK64
    z = ((z ^ (z >> 30)) * 0xBF58476D1CE4E5B9) & MASK64
    z = ((z ^ (z >> 27)) * 0x94D049BB133111EB) & MASK64
    return z ^ (z >> 31)


class Key:
    """128 位随机键（两个 uint64 半字），不可变、可哈希。"""

    __slots__ = ("hi", "lo")

    def __init__(self, hi: int, lo: int):
        self.hi = hi & MASK64
        self.lo = lo & MASK64

    def __eq__(self, other):
        return isinstance(other, Key) and (self.hi, self.lo) == (other.hi, other.lo)

    def __hash__(self):
        return hash((self.hi, self.lo))

    def __repr__(self):
        return f"Key(hi=0x{self.hi:016x}, lo=0x{self.lo:016x})"


def root_key(seed: int) -> Key:
    """由整数种子构造根键。"""
    if seed < 0:
        raise ValueError("seed 必须为非负整数")
    return Key(_mix64(seed), _mix64(seed ^ GOLDEN_GAMMA))


def split(key: Key, n: int = 2) -> tuple[Key, ...]:
    """把 ``key`` 拆成 ``n`` 个确定性子键；同一 (key, n) 结果恒定。"""
    if n < 1:
        raise ValueError("split 的 n 必须 >= 1")
    return tuple(
        Key(_mix64(key.hi ^ _mix64(i)), _mix64((key.lo + i) & MASK64))
        for i in range(n)
    )


def fold_in(key: Key, data: int) -> Key:
    """把非负整数 ``data``（如稳定样本 ID）混合进键，得到独立子流。"""
    if data < 0:
        raise ValueError("fold_in 的 data 必须为非负整数")
    return Key(
        _mix64(key.hi ^ _mix64(data)),
        _mix64(key.lo ^ _mix64((data + GOLDEN_GAMMA) & MASK64)),
    )


def random_bits(key: Key, index: int) -> int:
    """计数器式 64 位随机位：同一 (key, index) 永远得到同一结果。"""
    if index < 0:
        raise ValueError("index 必须为非负整数")
    return _mix64(key.lo ^ _mix64((key.hi + index) & MASK64))


def uniform01(key: Key, index: int) -> float:
    """[0, 1) 均匀浮点：取高 53 位除以 2^53。"""
    return (random_bits(key, index) >> 11) * (1.0 / (1 << 53))
