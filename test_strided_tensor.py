"""test_strided_tensor.py — strided_tensor 的 unittest 套件。

数值正确性通过与独立的纯 Python 参照实现（嵌套 list 操作）对比验证；
重叠判定通过对小张量的地址集合穷举核验，而非只比较包围字节区间。
"""

import random
import unittest
from itertools import product
from math import prod

import strided_tensor as st
from strided_tensor import (
    Storage,
    BorrowError,
    OverlapError,
    ReshapeError,
    Tensor,
    Tri,
)


# ---------------------------------------------------------------------------
# 独立参照实现（不依赖被测代码的任何逻辑）
# ---------------------------------------------------------------------------
def ref_transpose2(m):
    return [list(row) for row in zip(*m)]


def ref_flip_rows(m):
    return [row[:] for row in m[::-1]]


def ref_slice_step(flat, start, stop, step):
    return flat[start:stop:step]


def brute_internal_overlap(t):
    """穷举所有逻辑索引对，检查地址碰撞。"""
    idxs = list(st._iter_indices(t.shape))
    seen = {}
    for idx in idxs:
        addr = t.offset + sum(s * i for s, i in zip(t.strides, idx))
        if addr in seen and seen[addr] != idx:
            return True
        seen[addr] = idx
    return False


def brute_overlap(a, b):
    if a.storage is not b.storage:
        return False
    return bool(a.addresses() & b.addresses())


class TestViews(unittest.TestCase):
    def test_basic_read(self):
        t = Tensor.from_nested([[1, 2, 3], [4, 5, 6]])
        self.assertEqual(t.to_list(), [[1, 2, 3], [4, 5, 6]])
        self.assertEqual(t.strides, (3, 1))
        self.assertEqual(t.offset, 0)

    def test_transpose_is_view_and_reads_match_reference(self):
        data = [[1, 2, 3], [4, 5, 6]]
        t = Tensor.from_nested(data)
        tt = t.T
        self.assertEqual(tt.shape, (3, 2))
        self.assertEqual(tt.strides, (1, 3))
        self.assertEqual(tt.to_list(), ref_transpose2(data))
        # 视图共享存储：写转置视图，原张量可见
        tt[1, 0] = 99
        self.assertEqual(t.to_list()[0][1], 99)

    def test_negative_stride_flip_reads(self):
        data = [[1, 2, 3], [4, 5, 6]]
        t = Tensor.from_nested(data)
        self.assertEqual(t.flip(0).to_list(), ref_flip_rows(data))
        self.assertEqual(t.flip(1).to_list(), [row[::-1] for row in data])
        # 负步长下标读值
        f = t.flip(0)
        self.assertEqual(f[0, 0].to_list(), 4)
        self.assertEqual(f[1, 2].to_list(), 3)

    def test_reverse_slice_view(self):
        flat = list(range(10))
        t = Tensor.from_flat(flat, (10,))
        r = t[::-1]
        self.assertEqual(r.strides, (-1,))
        self.assertEqual(r.offset, 9)
        self.assertEqual(r.flat_values(), flat[::-1])
        self.assertEqual(t[7:1:-2].flat_values(), ref_slice_step(flat, 7, 1, -2))
        self.assertEqual(t[::3].flat_values(), ref_slice_step(flat, None, None, 3))

    def test_noncontiguous_slice(self):
        data = [[i * 4 + j for j in range(4)] for i in range(3)]
        t = Tensor.from_nested(data)
        sub = t[::2, 1::2]
        self.assertEqual(sub.shape, (2, 2))
        self.assertEqual(sub.strides, (8, 2))
        self.assertEqual(sub.to_list(), [row[1::2] for row in data[::2]])
        # 非连续视图写入会落到正确的底层位置
        sub[0, 0] = -1
        self.assertEqual(t.to_list()[0][1], -1)

    def test_zero_stride_broadcast(self):
        t = Tensor.from_flat([10, 20, 30], (3,))
        b = t.broadcast_to((4, 3))
        self.assertEqual(b.strides, (0, 1))
        self.assertEqual(b.to_list(), [[10, 20, 30]] * 4)
        self.assertEqual(b.internal_overlap(), Tri.YES)

    def test_empty_dims(self):
        t = Tensor.from_flat([], (0,))
        self.assertEqual(t.numel, 0)
        self.assertEqual(t.addresses(), set())
        self.assertEqual(t.internal_overlap(), Tri.NO)
        # 空切片
        v = Tensor.arange(5)[2:2]
        self.assertEqual(v.numel, 0)
        self.assertEqual(v.flat_values(), [])
        # 空维参与的重叠判定恒为 NO，拷贝为空操作
        w = Tensor.arange(5)
        self.assertEqual(v.overlap(w), Tri.NO)
        v.copy_from(w[:0])
        # 含空维的高维形状
        z = Tensor(Storage([]), (2, 0, 3))
        self.assertEqual(z.numel, 0)
        self.assertEqual(z.to_list(), [[], []])


class TestBounds(unittest.TestCase):
    def test_bounds_use_addresses_not_element_count(self):
        # 元素总数 3 远小于存储 8，但地址 0,4,8 越界 -> 必须拒绝
        with self.assertRaises(IndexError):
            Tensor(Storage(range(8)), (3,), (4,), 0)
        # 负步长越界
        with self.assertRaises(IndexError):
            Tensor(Storage(range(8)), (3,), (-4,), 4)  # 触及地址 -4
        # 恰好贴边合法
        t = Tensor(Storage(range(9)), (3,), (4,), 0)
        self.assertEqual(t.flat_values(), [0, 4, 8])

    def test_slice_bounds(self):
        t = Tensor.arange(5)
        with self.assertRaises(IndexError):
            _ = t[5]
        self.assertEqual(t[-1].to_list(), 4)


class TestOverlapDetection(unittest.TestCase):
    def test_internal_overlap_cases(self):
        # 普通连续视图无内部重叠
        self.assertEqual(Tensor.arange(6).internal_overlap(), Tri.NO)
        # 人为构造碰撞：shape (2,2) strides (1,1)，(0,1) 与 (1,0) 同址
        t = Tensor(Storage(range(3)), (2, 2), (1, 1))
        self.assertEqual(t.internal_overlap(), Tri.YES)
        # 快速充分条件失败但精确求解判定无碰撞：
        # strides (2,3) shape (3,3)，地址 2i+3j 共 9 个互不相同
        t2 = Tensor(Storage(range(11)), (3, 3), (2, 3))
        self.assertEqual(t2.internal_overlap(), Tri.NO)
        self.assertEqual(len(t2.addresses()), 9)

    def test_two_view_overlap_intervals_overlap_but_addresses_disjoint(self):
        # 包围区间相交但真实地址集合不相交：偶数位 vs 奇数位
        t = Tensor.arange(10)
        evens = t[0:10:2]  # 地址 0,2,4,6,8，区间 [0,8]
        odds = t[1:10:2]   # 地址 1,3,5,7,9，区间 [1,9]
        self.assertLessEqual(max(odds._address_range()[0], evens._address_range()[0]),
                             min(odds._address_range()[1], evens._address_range()[1]))
        self.assertEqual(evens.overlap(odds), Tri.NO)
        # 真正相交的情况
        self.assertEqual(t[0:6].overlap(t[4:10]), Tri.YES)
        self.assertEqual(t[0:4].overlap(t[4:10]), Tri.NO)
        # 不同 storage 恒不相交
        self.assertEqual(Tensor.arange(4).overlap(Tensor.arange(4)), Tri.NO)

    def test_unknown_is_conservative(self):
        t = Tensor.arange(64)
        a, b = t[0:64:2], t[1:64:2]
        saved = st.SOLVER_BUDGET
        try:
            st.SOLVER_BUDGET = 1  # 强制精确求解器超预算
            self.assertEqual(a.overlap(b), Tri.UNKNOWN)
            with self.assertRaises(OverlapError):
                a.copy_from(b)  # 禁止不确定覆盖
        finally:
            st.SOLVER_BUDGET = saved

    def test_exhaustive_small_tensor_verification(self):
        """随机小张量：判定结果与地址集合穷举核验一致。"""
        rng = random.Random(20261001)
        checked = 0
        while checked < 400:
            ndim = rng.randint(1, 3)
            shape = tuple(rng.randint(0, 4) for _ in range(ndim))
            strides = tuple(rng.randint(-4, 4) for _ in range(ndim))
            offset = rng.randint(0, 12)
            try:
                t = Tensor(Storage(range(16)), shape, strides, offset)
            except IndexError:
                continue
            expect = brute_internal_overlap(t)
            got = t.internal_overlap()
            self.assertIsNot(got, Tri.UNKNOWN)
            self.assertEqual(got == Tri.YES, expect, msg=f"{t!r}")
            checked += 1
        # 两视图交叠穷举核验
        checked = 0
        while checked < 400:
            def rand_view():
                ndim = rng.randint(1, 2)
                shape = tuple(rng.randint(0, 4) for _ in range(ndim))
                strides = tuple(rng.randint(-3, 3) for _ in range(ndim))
                return Tensor(Storage(range(16)), shape, strides, rng.randint(0, 12))
            try:
                a, b = rand_view(), rand_view()
            except IndexError:
                continue
            got = a.overlap(b)
            self.assertIsNot(got, Tri.UNKNOWN)
            self.assertEqual(got == Tri.YES, brute_overlap(a, b),
                             msg=f"{a!r} vs {b!r}")
            checked += 1


class TestWrites(unittest.TestCase):
    def test_snapshot_semantics_overlapping_copy(self):
        # 右移一位：t[1:] = t[:-1]，交叠 -> 快照语义 => [1,1,2,3] 而非 [1,1,1,1]
        t = Tensor.from_flat([1, 2, 3, 4], (4,))
        t[1:].copy_from(t[:-1])
        self.assertEqual(t.flat_values(), [1, 1, 2, 3])
        # 反向视图整体拷贝：t[:] = t[::-1] => 完全反转
        t2 = Tensor.from_flat([1, 2, 3, 4], (4,))
        t2[:].copy_from(t2[::-1])
        self.assertEqual(t2.flat_values(), [4, 3, 2, 1])

    def test_iadd_aliased_snapshot(self):
        t = Tensor.from_flat([1, 2, 3], (3,))
        t += t[::-1]  # [1+3, 2+2, 3+1]
        self.assertEqual(t.flat_values(), [4, 4, 4])

    def test_broadcast_target_write_forbidden(self):
        t = Tensor.from_flat([1, 2, 3], (3,))
        b = t.broadcast_to((4, 3))
        with self.assertRaises(OverlapError):
            b.copy_from(Tensor.from_nested([[0, 0, 0]] * 4))
        with self.assertRaises(OverlapError):
            b.fill(0)

    def test_borrow_write_protection(self):
        t = Tensor.arange(4)
        with t.borrow():
            with self.assertRaises(BorrowError):
                t[0] = 99
            with self.assertRaises(BorrowError):
                t.fill(0)
            with self.assertRaises(BorrowError):
                t += 1
            # 读不受限
            self.assertEqual(t.flat_values(), [0, 1, 2, 3])
        # 借用结束后恢复可写
        t[0] = 99
        self.assertEqual(t.flat_values()[0], 99)
        # 同一 storage 的其他视图同样被保护
        v = t[::-1]
        with t.borrow():
            with self.assertRaises(BorrowError):
                v[0] = 1

    def test_setitem_scalar_and_tensor(self):
        t = Tensor.arange(6).reshape((2, 3))
        t[0, 1] = -7
        self.assertEqual(t.to_list()[0][1], -7)
        t[:, 2] = Tensor.from_flat([30, 60], (2,))
        self.assertEqual(t.to_list(), [[0, -7, 30], [3, 4, 60]])


class TestReshape(unittest.TestCase):
    def test_contiguous_reshape_is_view(self):
        t = Tensor.arange(6)
        r = t.reshape((2, 3))
        self.assertIs(r.storage, t.storage)
        r[0, 1] = 99
        self.assertEqual(t.flat_values()[1], 99)

    def test_transpose_reshape_rejected_or_copied(self):
        t = Tensor.from_nested([[1, 2, 3], [4, 5, 6]])
        tt = t.T
        self.assertFalse(tt._is_c_contiguous())
        with self.assertRaises(ReshapeError):
            tt.reshape((6,))
        rc = tt.reshape((6,), copy=True)
        self.assertEqual(rc.flat_values(), [1, 4, 2, 5, 3, 6])
        self.assertIsNot(rc.storage, t.storage)

    def test_reshape_negative_stride_rejected(self):
        t = Tensor.arange(4)[::-1]
        with self.assertRaises(ReshapeError):
            t.reshape((2, 2))
        rc = t.reshape((2, 2), copy=True)
        self.assertEqual(rc.to_list(), [[3, 2], [1, 0]])

    def test_reshape_size_mismatch(self):
        with self.assertRaises(ReshapeError):
            Tensor.arange(6).reshape((4, 2))
        r = Tensor.arange(6).reshape((-1,))
        self.assertEqual(r.shape, (6,))


class TestNumericalIndependence(unittest.TestCase):
    """与纯 Python 参照逐项对比的独立数值验证。"""

    def test_random_views_match_nested_list_reference(self):
        rng = random.Random(7)
        for _ in range(50):
            rows, cols = rng.randint(1, 5), rng.randint(1, 5)
            data = [[rng.randint(-9, 9) for _ in range(cols)] for _ in range(rows)]
            t = Tensor.from_nested(data)
            # 转置
            self.assertEqual(t.T.to_list(), ref_transpose2(data))
            # 行/列翻转
            self.assertEqual(t.flip(0).to_list(), ref_flip_rows(data))
            self.assertEqual(t.flip(1).to_list(), [r[::-1] for r in data])
            # 非连续切片
            rs = rng.choice([1, 2, 3])
            cs = rng.choice([1, 2, 3])
            self.assertEqual(t[::rs, ::cs].to_list(),
                             [r[::cs] for r in data[::rs]])
            # 复合：转置后翻转再切片
            comp = t.T.flip(0)[::2]
            ref = ref_transpose2(data)[::-1][::2]
            self.assertEqual(comp.to_list(), ref)


if __name__ == "__main__":
    unittest.main(verbosity=2)
