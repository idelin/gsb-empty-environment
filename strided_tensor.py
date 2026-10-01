"""strided_tensor.py

CPU 小张量的 stride/offset 视图实现（仅标准库，Python 3.11）。

特性：
- 视图 = (storage, shape, strides, offset)，支持转置、负步长翻转、非连续切片、
  零步长广播、空维。
- 原地写入前做别名检查：内部重叠的写入目标直接拒绝；两视图交叠时使用
  快照语义（先整体物化源，再写目标）；无法判定（UNKNOWN）时拒绝不确定覆盖。
- 重叠判定：快速充分条件（排序步长间隔条件 / 包围区间不相交）不足时，
  用有限精确求解（可达地址集合 DP，带预算上限），超预算返回保守 UNKNOWN。
- 越界检查基于实际地址区间 [lo, hi]，而不是元素总数。
- 借用期写保护：borrow() 上下文内同一 storage 的所有写入被拒绝。
"""

from __future__ import annotations

from contextlib import contextmanager
from enum import Enum
from math import prod

__all__ = [
    "Tensor",
    "Storage",
    "Tri",
    "BorrowError",
    "OverlapError",
    "ReshapeError",
]

# 精确求解器的预算：可达地址集合允许的最大规模，超出则返回 UNKNOWN（保守不安全）。
SOLVER_BUDGET = 200_000


class Tri(Enum):
    """三态判定结果。UNKNOWN 在所有写路径上按“不安全”处理。"""

    NO = 0
    YES = 1
    UNKNOWN = 2


class BorrowError(RuntimeError):
    """借用期内尝试写入。"""


class OverlapError(RuntimeError):
    """写入目标内部重叠，或无法证明源/目标不相交。"""


class ReshapeError(ValueError):
    """非连续视图不允许作为视图 reshape。"""


class _BudgetExceeded(Exception):
    pass


class Storage:
    """共享底层缓冲，附带借用计数（写保护）。"""

    __slots__ = ("data", "borrow_count")

    def __init__(self, data):
        self.data = list(data)
        self.borrow_count = 0

    def __len__(self):
        return len(self.data)


def _c_strides(shape):
    acc = 1
    out = []
    for n in reversed(shape):
        out.append(acc)
        acc *= n
    return tuple(reversed(out))


def _iter_indices(shape):
    """按 C 序（行主序）枚举索引元组；空维不产出任何索引。"""
    if any(n == 0 for n in shape):
        return
    if not shape:
        yield ()
        return
    idx = [0] * len(shape)
    while True:
        yield tuple(idx)
        for ax in range(len(shape) - 1, -1, -1):
            idx[ax] += 1
            if idx[ax] < shape[ax]:
                break
            idx[ax] = 0
        else:
            return


def _reachable_sums(terms):
    """精确有限求解：{sum s_k*i_k | 0 <= i_k < n_k}。

    terms 为 (stride, count) 列表。集合规模超过 SOLVER_BUDGET 时抛
    _BudgetExceeded，由调用方降级为 UNKNOWN。
    """
    reachable = {0}
    for stride, count in terms:
        if count <= 1 or stride == 0:
            continue
        if len(reachable) * count > SOLVER_BUDGET:
            raise _BudgetExceeded
        nxt = set()
        for base in reachable:
            val = base
            for _ in range(count):
                nxt.add(val)
                val += stride
        reachable = nxt
    return reachable


class Tensor:
    """stride/offset 视图。所有视图共享同一个 Storage。"""

    __slots__ = ("storage", "shape", "strides", "offset")

    def __init__(self, storage, shape, strides=None, offset=0):
        if not isinstance(storage, Storage):
            storage = Storage(storage)
        self.storage = storage
        self.shape = tuple(int(n) for n in shape)
        if any(n < 0 for n in self.shape):
            raise ValueError(f"negative dim in shape {self.shape}")
        self.strides = (
            tuple(int(s) for s in strides)
            if strides is not None
            else _c_strides(self.shape)
        )
        if len(self.strides) != len(self.shape):
            raise ValueError("shape/strides rank mismatch")
        self.offset = int(offset)
        self._check_bounds()

    # ------------------------------------------------------------------
    # 构造
    # ------------------------------------------------------------------
    @classmethod
    def from_flat(cls, data, shape):
        if len(data) != prod(shape):
            raise ValueError("data length does not match shape")
        return cls(Storage(data), shape)

    @classmethod
    def from_nested(cls, nested):
        shape = []
        node = nested
        while isinstance(node, list):
            shape.append(len(node))
            node = node[0] if node else []
        flat = []

        def walk(x, depth):
            if depth == len(shape):
                flat.append(x)
                return
            if len(x) != shape[depth]:
                raise ValueError("ragged nested list")
            for item in x:
                walk(item, depth + 1)

        walk(nested, 0)
        return cls(Storage(flat), tuple(shape))

    @classmethod
    def arange(cls, n):
        return cls(Storage(list(range(n))), (n,))

    # ------------------------------------------------------------------
    # 基本属性与越界检查（按真实地址区间，而非元素总数）
    # ------------------------------------------------------------------
    @property
    def ndim(self):
        return len(self.shape)

    @property
    def numel(self):
        return prod(self.shape)

    def _address_range(self):
        """返回 (lo, hi) 实际寻址区间；空视图返回 None。"""
        if any(n == 0 for n in self.shape):
            return None
        lo = hi = self.offset
        for n, s in zip(self.shape, self.strides):
            ext = (n - 1) * s
            if s >= 0:
                hi += ext
            else:
                lo += ext
        return lo, hi

    def _check_bounds(self):
        rng = self._address_range()
        if rng is None:
            return  # 空维：不寻址任何元素
        lo, hi = rng
        if lo < 0 or hi >= len(self.storage):
            raise IndexError(
                f"view addresses [{lo}, {hi}] out of storage bounds "
                f"[0, {len(self.storage) - 1}]"
            )

    # ------------------------------------------------------------------
    # 读路径
    # ------------------------------------------------------------------
    def _flat_addresses(self):
        off = self.offset
        strides = self.strides
        return [
            off + sum(s * i for s, i in zip(strides, idx))
            for idx in _iter_indices(self.shape)
        ]

    def addresses(self):
        """该视图实际触及的存储地址集合（小张量穷举核验用）。"""
        return set(self._flat_addresses())

    def flat_values(self):
        data = self.storage.data
        return [data[a] for a in self._flat_addresses()]

    def to_list(self):
        vals = self.flat_values()

        def build(shape, vals, pos):
            if not shape:
                return vals[pos]
            n = shape[0]
            step = prod(shape[1:])
            return [build(shape[1:], vals, pos + i * step) for i in range(n)]

        return build(self.shape, vals, 0)

    def __repr__(self):
        return (
            f"Tensor(shape={self.shape}, strides={self.strides}, "
            f"offset={self.offset}, data={self.to_list()!r})"
        )

    # ------------------------------------------------------------------
    # 视图操作
    # ------------------------------------------------------------------
    def transpose(self, axes=None):
        if axes is None:
            axes = tuple(reversed(range(self.ndim)))
        axes = tuple(a % self.ndim for a in axes)
        if sorted(axes) != list(range(self.ndim)):
            raise ValueError(f"invalid permutation {axes}")
        return Tensor(
            self.storage,
            tuple(self.shape[a] for a in axes),
            tuple(self.strides[a] for a in axes),
            self.offset,
        )

    @property
    def T(self):
        return self.transpose()

    def flip(self, axis):
        """负步长视图：沿 axis 反转，不复制数据。"""
        axis %= self.ndim
        shape = list(self.shape)
        strides = list(self.strides)
        offset = self.offset + (shape[axis] - 1) * strides[axis]
        strides[axis] = -strides[axis]
        return Tensor(self.storage, shape, strides, offset)

    def broadcast_to(self, new_shape):
        """零步长广播。源维为 1 或新增前导维时步长置 0。"""
        new_shape = tuple(int(n) for n in new_shape)
        if len(new_shape) < self.ndim:
            raise ValueError("cannot broadcast to fewer dims")
        pad = len(new_shape) - self.ndim
        old_shape = (1,) * pad + self.shape
        old_strides = (0,) * pad + self.strides
        strides = []
        for n, old_n, old_s in zip(new_shape, old_shape, old_strides):
            if old_n == n:
                strides.append(old_s)
            elif old_n == 1:
                strides.append(0)
            else:
                raise ValueError(f"cannot broadcast {self.shape} to {new_shape}")
        return Tensor(self.storage, new_shape, tuple(strides), self.offset)

    def _is_c_contiguous(self):
        if self.numel <= 1:
            return True
        expected = 1
        for n, s in zip(reversed(self.shape), reversed(self.strides)):
            if n == 1:
                continue
            if s != expected:
                return False
            expected *= n
        return True

    def reshape(self, new_shape, *, copy=False):
        """连续视图直接改形；非连续（如转置后）默认拒绝，copy=True 时复制。"""
        new_shape = list(new_shape)
        if new_shape.count(-1) > 1:
            raise ReshapeError("at most one -1 in shape")
        if -1 in new_shape:
            known = prod(n for n in new_shape if n != -1)
            if known == 0 or self.numel % known:
                raise ReshapeError("cannot infer -1 dim")
            new_shape[new_shape.index(-1)] = self.numel // known
        new_shape = tuple(new_shape)
        if prod(new_shape) != self.numel:
            raise ReshapeError(
                f"cannot reshape {self.shape} ({self.numel} elems) to {new_shape}"
            )
        if self._is_c_contiguous():
            return Tensor(self.storage, new_shape, _c_strides(new_shape), self.offset)
        if not copy:
            raise ReshapeError(
                "reshape of non-contiguous view requires copy=True"
            )
        return Tensor(Storage(self.flat_values()), new_shape)

    def clone(self):
        return Tensor(Storage(self.flat_values()), self.shape)

    # ------------------------------------------------------------------
    # 切片 / 下标
    # ------------------------------------------------------------------
    def __getitem__(self, key):
        if not isinstance(key, tuple):
            key = (key,)
        key = list(key)
        if key.count(Ellipsis) > 1:
            raise IndexError("only one ellipsis allowed")
        specified = sum(1 for k in key if k is not Ellipsis)
        if Ellipsis in key:
            i = key.index(Ellipsis)
            key[i : i + 1] = [slice(None)] * (self.ndim - specified)
        if len(key) > self.ndim:
            raise IndexError("too many indices")
        key += [slice(None)] * (self.ndim - len(key))

        shape, strides = [], []
        offset = self.offset
        for k, n, s in zip(key, self.shape, self.strides):
            if isinstance(k, int):
                i = k + n if k < 0 else k
                if not 0 <= i < n:
                    raise IndexError(f"index {k} out of range for dim of size {n}")
                offset += i * s
            elif isinstance(k, slice):
                start, stop, step = k.indices(n)
                length = len(range(start, stop, step))
                offset += start * s
                shape.append(length)
                strides.append(s * step)
            else:
                raise TypeError(f"unsupported index {k!r}")
        return Tensor(self.storage, tuple(shape), tuple(strides), offset)

    # ------------------------------------------------------------------
    # 重叠判定
    # ------------------------------------------------------------------
    def _sum_terms(self):
        """对地址集合有实际贡献的 (stride, count) 项（忽略零步长与长度 1 维）。"""
        return [
            (s, n) for s, n in zip(self.strides, self.shape) if n > 1 and s != 0
        ]

    def internal_overlap(self) -> Tri:
        """视图内部是否存在两个不同逻辑索引映射到同一地址。"""
        numel = self.numel
        if numel <= 1:
            return Tri.NO
        dims = [(s, n) for s, n in zip(self.strides, self.shape) if n > 1]
        if any(s == 0 for s, _ in dims):
            return Tri.YES  # 零步长广播：必然内部重叠
        # 快速充分条件：|s| 升序后每个步长严格大于前面所有步长的可达跨度。
        reach = 1  # 1 + sum |s_j|*(n_j-1)
        sufficient = True
        for a, n in sorted((abs(s), n) for s, n in dims):
            if a < reach:
                sufficient = False
                break
            reach += a * (n - 1)
        if sufficient:
            return Tri.NO
        # 精确有限求解：地址像集大小 < 元素数 <=> 存在碰撞。
        try:
            image = _reachable_sums(self._sum_terms())
        except _BudgetExceeded:
            return Tri.UNKNOWN
        return Tri.NO if len(image) == numel else Tri.YES

    def overlap(self, other: "Tensor") -> Tri:
        """两视图是否共享至少一个存储地址。"""
        if self.storage is not other.storage:
            return Tri.NO
        if self.numel == 0 or other.numel == 0:
            return Tri.NO
        # 快速充分条件：包围地址区间不相交。
        (lo1, hi1), (lo2, hi2) = self._address_range(), other._address_range()
        if hi1 < lo2 or hi2 < lo1:
            return Tri.NO
        # 精确有限求解：真实地址集合求交（不是只比较包围区间）。
        try:
            a1 = {self.offset + x for x in _reachable_sums(self._sum_terms())}
            a2 = {other.offset + x for x in _reachable_sums(other._sum_terms())}
        except _BudgetExceeded:
            return Tri.UNKNOWN
        return Tri.YES if a1 & a2 else Tri.NO

    # ------------------------------------------------------------------
    # 写路径（借用保护 + 别名检查 + 快照语义）
    # ------------------------------------------------------------------
    def _check_writable(self):
        if self.storage.borrow_count:
            raise BorrowError(
                "storage is borrowed; writes are forbidden during borrow"
            )

    @contextmanager
    def borrow(self):
        """借用上下文：期间同一 storage 上的任何写入抛 BorrowError。"""
        self.storage.borrow_count += 1
        try:
            yield self
        finally:
            self.storage.borrow_count -= 1

    def _write_flat(self, values):
        data = self.storage.data
        for addr, v in zip(self._flat_addresses(), values):
            data[addr] = v

    def copy_from(self, src):
        """把 src 写入本视图。

        - 目标内部重叠（如广播视图）-> OverlapError；
        - 源/目标交叠 -> 快照语义：先完整物化源，再写目标；
        - 交叠判定 UNKNOWN -> OverlapError（禁止不确定覆盖）。
        """
        self._check_writable()
        if self.internal_overlap() is not Tri.NO:
            raise OverlapError("write target has internal overlap")
        if not isinstance(src, Tensor):
            self._write_flat([src] * self.numel)
            return
        if src.shape != self.shape:
            src = src.broadcast_to(self.shape)
        ov = self.overlap(src)
        if ov is Tri.UNKNOWN:
            raise OverlapError(
                "cannot prove source/destination disjoint; refusing uncertain overwrite"
            )
        # 快照语义：无论是否交叠，都先物化源再写，结果与“先快照”一致。
        snapshot = src.flat_values()
        self._write_flat(snapshot)

    def fill(self, value):
        self._check_writable()
        if self.internal_overlap() is not Tri.NO:
            raise OverlapError("write target has internal overlap")
        self._write_flat([value] * self.numel)

    def __setitem__(self, key, value):
        self[key].copy_from(value)

    def __iadd__(self, other):
        self._check_writable()
        if self.internal_overlap() is not Tri.NO:
            raise OverlapError("write target has internal overlap")
        if isinstance(other, Tensor):
            if other.shape != self.shape:
                other = other.broadcast_to(self.shape)
            ov = self.overlap(other)
            if ov is Tri.UNKNOWN:
                raise OverlapError(
                    "cannot prove operand disjoint; refusing uncertain overwrite"
                )
            rhs = other.flat_values()  # 快照
            vals = [a + b for a, b in zip(self.flat_values(), rhs)]
        else:
            vals = [a + other for a in self.flat_values()]
        self._write_flat(vals)
        return self
