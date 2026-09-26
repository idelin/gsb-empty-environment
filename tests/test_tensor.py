"""Core tensor semantics + cross-checks against independent enumerators."""
import random
import unittest

from minitensor import Storage, Tensor, dtype_of
from minitensor.tensor import address_bounds
from minitensor.verify import (
    enumerate_addresses,
    matmul_tensor,
    reference_matmul,
    reference_view,
)


class TestDTypes(unittest.TestCase):
    def test_pack_unpack_roundtrip_all_dtypes(self):
        samples = {
            "u1": [0, 1, 255],
            "i1": [-128, 0, 127],
            "u2": [0, 65535],
            "i2": [-32768, 32767],
            "u4": [0, 2**32 - 1],
            "i4": [-(2**31), 2**31 - 1],
            "u8": [0, 2**64 - 1],
            "i8": [-(2**63), 2**63 - 1],
            "f4": [0.5, -1.25],
            "f8": [0.1, -3.75, 1e300],
        }
        for name, values in samples.items():
            dt = dtype_of(name)
            self.assertEqual(dt.itemsize, len(dt.pack(values[0])))
            for v in values:
                got = dt.unpack(dt.pack(v))
                if dt.is_float and dt.itemsize == 4:
                    self.assertAlmostEqual(got, v, places=6)
                else:
                    self.assertEqual(got, v)

    def test_unknown_dtype_rejected(self):
        with self.assertRaises(KeyError):
            dtype_of("f16")


class TestTensorBasics(unittest.TestCase):
    def test_get_set_and_negative_index(self):
        t = Tensor.from_flat([1, 2, 3, 4, 5, 6], "i4", shape=(2, 3))
        self.assertEqual(t[1, 2], 6)
        self.assertEqual(t[-1, -1], 6)
        t[0, 1] = 99
        self.assertEqual(t[0, 1], 99)
        with self.assertRaises(IndexError):
            _ = t[2, 0]
        with self.assertRaises(IndexError):
            _ = t[0]

    def test_scalar_tensor(self):
        t = Tensor.from_flat([42], "i8", shape=())
        self.assertEqual(t.ndim, 0)
        self.assertEqual(t.numel(), 1)
        self.assertEqual(t[()], 42)
        self.assertEqual(t.tolist(), 42)

    def test_empty_tensor(self):
        t = Tensor.zeros((0, 3), "f8")
        self.assertEqual(t.numel(), 0)
        self.assertEqual(list(t.addresses()), [])
        self.assertEqual(t.tolist(), [])

    def test_negative_stride_view_shares_storage(self):
        base = Tensor.from_flat([1, 2, 3, 4, 5, 6], "i4")
        flip = base.as_strided((3,), (-2,), offset=5)
        self.assertIs(flip.storage, base.storage)
        self.assertEqual(flip.tolist(), [6, 4, 2])
        flip[1] = 400  # writes through to the shared storage
        self.assertEqual(base[3], 400)

    def test_overlapping_window_view(self):
        base = Tensor.from_flat(list(range(10)), "i4")
        window = base.as_strided((2, 3), (3, 1), offset=2)
        self.assertEqual(window.tolist(), [[2, 3, 4], [5, 6, 7]])
        window[1, 0] = -5
        self.assertEqual(base[5], -5)

    def test_constructor_rejects_out_of_range_views(self):
        storage = Storage.allocate(6 * 4)
        # negative stride reaching before element 0
        with self.assertRaises(ValueError):
            Tensor(storage, "i4", (4,), (-2,), 5)
        # positive stride reaching past the end
        with self.assertRaises(ValueError):
            Tensor(storage, "i4", (4,), (2,), 0)
        # negative offset
        with self.assertRaises(ValueError):
            Tensor(storage, "i4", (2,), (1,), -1)
        # negative dimension
        with self.assertRaises(ValueError):
            Tensor(storage, "i4", (-1,), (1,), 0)

    def test_address_bounds(self):
        self.assertEqual(address_bounds((3,), (-2,), 5), (1, 5))
        self.assertEqual(address_bounds((2, 3), (3, 1), 0), (0, 5))
        self.assertIsNone(address_bounds((0, 3), (3, 1), 0))


class TestIndependentCrossCheck(unittest.TestCase):
    """Tensor iteration vs. the independent recursive address enumerator."""

    def test_random_views_match_enumerator(self):
        rng = random.Random(20260926)
        capacity = 64
        flat = [rng.randint(-1000, 1000) for _ in range(capacity)]
        base = Tensor.from_flat(flat, "i4")
        checked = 0
        for _ in range(300):
            ndim = rng.randint(1, 3)
            shape = tuple(rng.randint(1, 4) for _ in range(ndim))
            strides = tuple(rng.randint(-3, 3) for _ in range(ndim))
            lo, hi = address_bounds(shape, strides, 0)
            slack = capacity - 1 - (hi - lo)
            if slack < 0:
                continue
            offset = -lo + rng.randint(0, slack)
            view = base.as_strided(shape, strides, offset)
            # 1) iteration order matches the independent enumerator exactly
            expected_addrs = enumerate_addresses(shape, strides, offset)
            self.assertEqual(list(view.addresses()), expected_addrs)
            # 2) bounds helper agrees with the enumerator's extremes
            self.assertEqual(
                address_bounds(shape, strides, offset),
                (min(expected_addrs), max(expected_addrs)),
            )
            # 3) materialized values match the independent reference view
            self.assertEqual(
                view.tolist(), reference_view(flat, shape, strides, offset)
            )
            checked += 1
        self.assertGreater(checked, 200)


class TestNumericalVerification(unittest.TestCase):
    def test_matmul_matches_independent_reference(self):
        a_vals = [float(v) for v in range(12)]
        b_vals = [float(v * v - 3) for v in range(8)]
        a = Tensor.from_flat(a_vals, "f8", shape=(3, 4))
        b = Tensor.from_flat(b_vals, "f8", shape=(4, 2))
        got = matmul_tensor(a, b)
        want = reference_matmul(
            [a_vals[0:4], a_vals[4:8], a_vals[8:12]],
            [b_vals[0:2], b_vals[2:4], b_vals[4:6], b_vals[6:8]],
        )
        self.assertEqual(len(got), 3)
        for row_g, row_w in zip(got, want):
            for g, w in zip(row_g, row_w):
                self.assertAlmostEqual(g, w, places=9)

    def test_sum_over_negative_stride_view(self):
        flat = [10, 11, 12, 13, 14, 15]
        base = Tensor.from_flat(flat, "i8")
        rev = base.as_strided((6,), (-1,), offset=5)
        self.assertEqual(sum(rev.values()), sum(flat))
        self.assertEqual(list(rev.values()), list(reversed(flat)))


if __name__ == "__main__":
    unittest.main()
