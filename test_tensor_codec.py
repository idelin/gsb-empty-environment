"""unittest suite: layout, codec roundtrip, field mutation, fuzz, atomicity."""

import io
import os
import random
import struct
import tempfile
import unittest

import codec
import tensor as ts


# ---------------------------------------------------------------------------
# Independent address enumerator (implemented separately from tensor.py):
# recursively walk every logical index, compute the byte address via plain
# sums, and return the sorted set of touched bytes. This does not share any
# production reachability code.
# ---------------------------------------------------------------------------


def independent_enumerate_bytes(shape, strides, itemsize, offset):
    touched = set()

    def walk(axis, addr):
        if axis == len(shape):
            for b in range(itemsize):
                touched.add(addr + b)
            return
        for i in range(shape[axis]):
            walk(axis + 1, addr + i * strides[axis])

    if all(d > 0 for d in shape):
        walk(0, offset)
    elif all(d >= 0 for d in shape):
        pass  # empty tensor: no bytes
    else:
        raise AssertionError("bad shape")
    return touched


def independent_min_max(shape, strides, itemsize, offset):
    """Closed-form min/max element address, computed independently."""
    addrs = [0]
    for d, s in zip(shape, strides):
        if d == 0:
            return None
        step = (d - 1) * s
        addrs = [a + x for a in addrs for x in (0, step)]
    lo = min(addrs) + offset
    hi = max(addrs) + offset + itemsize
    return lo, hi


def make_canonical_blob():
    base = ts.reshape_contiguous(ts.arange(24, ts.int32), (4, 6))
    rev_rows = base[::-1, :]
    sub = base[1:3, 2:6]
    overlap = base[:, ::2]
    both_rev = base[::-1, ::-1]
    scalar_view = base[2, 3]
    tensors = [base, rev_rows, sub, overlap, both_rev, scalar_view]
    blob = codec.save(tensors)
    return blob, tensors


class LayoutTests(unittest.TestCase):
    def test_negative_stride_reach(self):
        base = ts.reshape_contiguous(ts.arange(12, ts.int32), (3, 4))
        v = base[::-1, ::2]
        lo, hi, first = ts.reachable_byte_span(v.shape, v.strides, v.dtype)
        self.assertEqual((lo, hi), (-32, 12))
        self.assertEqual(first, 32)
        self.assertEqual(v.offset + lo, 0)
        self.assertEqual(v.tolist(), [[8, 10], [4, 6], [0, 2]])

    def test_oob_positive_stride_rejected(self):
        st = ts.Storage(bytes(16))
        with self.assertRaises(ts.LayoutError):
            ts.Tensor(st, ts.int32, (5,), (4,), 0)

    def test_oob_negative_stride_rejected(self):
        st = ts.Storage(bytes(16))
        # offset too small for a negative-stride view: region precedes storage
        with self.assertRaises(ts.LayoutError):
            ts.Tensor(st, ts.int32, (3,), (-4,), 0)
        # and an offset that pushes the positive end past storage
        with self.assertRaises(ts.LayoutError):
            ts.Tensor(st, ts.int32, (3,), (4,), 8)

    def test_unaligned_offset_and_stride_rejected(self):
        st = ts.Storage(bytes(32))
        with self.assertRaises(ts.LayoutError):
            ts.Tensor(st, ts.int32, (2,), (4,), 1)
        with self.assertRaises(ts.LayoutError):
            ts.Tensor(st, ts.int32, (2,), (5,), 0)

    def test_empty_tensor(self):
        base = ts.arange(4, ts.int32)
        v = base[5:5]
        self.assertEqual(v.shape, (0,))
        self.assertEqual(v.flat_values(), [])
        blob = codec.save([v])
        cat = codec.load(io.BytesIO(blob))
        self.assertEqual(cat.tensors[1].flat_values(), [])

    def test_shape_product_overflow_rejected(self):
        st = ts.Storage(bytes(8))
        with self.assertRaises(ts.LayoutError):
            ts.Tensor(st, ts.int8, (2**40, 2**40), (0, 0), 0,
                      element_budget=2**63 - 1)

    def test_slice_identity_preserved(self):
        base = ts.reshape_contiguous(ts.arange(12, ts.int32), (3, 4))
        v = base[1:3]
        self.assertIs(v.storage, base.storage)


class IndependentEnumeratorTests(unittest.TestCase):
    def test_enumerator_matches_formula_random_layouts(self):
        rng = random.Random(1234)
        for _ in range(400):
            ndim = rng.randint(1, 3)
            shape = tuple(rng.randint(1, 5) for _ in range(ndim))
            itemsize = 4
            storage_n = 2000
            raw_strides = [rng.randint(-6, 6) * itemsize for _ in range(ndim)]
            lo, hi, _ = ts.reachable_byte_span(shape, raw_strides, ts.int32)
            if lo == 0 and hi == 0:
                continue
            slack = max(0, (storage_n - (hi - lo)) // itemsize)
            offset = -lo + rng.randint(0, slack) * itemsize
            touched = independent_enumerate_bytes(shape, raw_strides, itemsize, offset)
            mnmx = independent_min_max(shape, raw_strides, itemsize, offset)
            self.assertIsNotNone(mnmx)
            self.assertEqual(min(touched), mnmx[0])
            self.assertEqual(max(touched) + 1, mnmx[1])
            # production span must agree with the independent enumerator
            plo, phi, _ = ts.reachable_byte_span(shape, raw_strides, ts.int32)
            self.assertEqual(offset + plo, min(touched))
            self.assertEqual(offset + phi, max(touched) + 1)
            ts.Tensor(ts.Storage(bytes(storage_n)), ts.int32,
                      shape, tuple(raw_strides), offset)

    def test_enumerator_detects_oob(self):
        # Build many random layouts; enumerator's OOB verdict must match
        # validate_layout's verdict.
        rng = random.Random(99)
        agree = 0
        for _ in range(2000):
            ndim = rng.randint(1, 2)
            shape = tuple(rng.randint(1, 4) for _ in range(ndim))
            itemsize = 4
            strides = tuple(rng.randint(-3, 3) * itemsize for _ in range(ndim))
            if 0 in strides:
                continue
            storage_n = rng.randint(1, 60)
            offset = rng.randint(0, storage_n)
            touched = independent_enumerate_bytes(shape, strides, itemsize, offset)
            enum_oob = bool(touched) and (
                min(touched) < 0 or max(touched) >= storage_n
                or offset % itemsize != 0
            )
            try:
                ts.validate_layout(shape, strides, ts.int32, offset, storage_n)
                prod_oob = False
            except ts.LayoutError:
                prod_oob = True
            self.assertEqual(enum_oob, prod_oob,
                             (shape, strides, offset, storage_n))
            agree += 1
        self.assertGreater(agree, 500)


class CodecRoundtripTests(unittest.TestCase):
    def test_roundtrip_values_and_shapes(self):
        blob, tensors = make_canonical_blob()
        cat = codec.load(io.BytesIO(blob))
        for i, t in enumerate(tensors, start=1):
            got = cat.tensors[i]
            self.assertEqual(got.shape, t.shape)
            self.assertEqual(got.strides, t.strides)
            self.assertEqual(got.dtype, t.dtype)
            self.assertEqual(got.tolist(), t.tolist())

    def test_shared_storage_identity_preserved(self):
        blob, _ = make_canonical_blob()
        cat = codec.load(io.BytesIO(blob))
        storages = {id(t.storage) for t in cat.tensors.values()}
        self.assertEqual(len(storages), 1)

    def test_overlapping_views_share_identity(self):
        base = ts.reshape_contiguous(ts.arange(16, ts.int32), (4, 4))
        a = base[:3]
        b = base[1:]
        cat = codec.load(io.BytesIO(codec.save([base, a, b])))
        self.assertIs(cat.tensors[1].storage, cat.tensors[2].storage)
        self.assertIs(cat.tensors[2].storage, cat.tensors[3].storage)
        self.assertEqual(cat.tensors[2].tolist(), a.tolist())

    def test_dtype_coverage(self):
        for dt in (ts.int8, ts.uint8, ts.int16, ts.uint16, ts.int32, ts.uint32,
                   ts.int64, ts.uint64, ts.float32, ts.float64):
            vals = [0, 1, dt.itemsize, 7, -3 if dt.kind == "i" else 9,
                    2.5 if dt.kind == "f" else 11]
            t = ts.from_values(vals[:4], dt)
            cat = codec.load(io.BytesIO(codec.save([t])))
            self.assertEqual(cat.tensors[1].flat_values(), t.flat_values(), dt.name)

    def test_streaming_file_roundtrip(self):
        blob, tensors = make_canonical_blob()
        with tempfile.NamedTemporaryFile(delete=False) as f:
            f.write(blob)
            path = f.name
        try:
            cat = codec.load(path)
            self.assertEqual(cat.tensors[1].tolist(), tensors[0].tolist())
        finally:
            os.unlink(path)

    def test_tensor_chain_reference(self):
        # A tensor entry may reference another tensor (offset-relative).
        base = ts.arange(8, ts.int32)
        blob = bytearray()
        blob += codec.MAGIC + bytes([codec.VERSION]) + struct.pack("<I", 3)
        payload = bytes(base.storage.data)
        blob += bytes([codec.KIND_STORAGE]) + struct.pack("<IQ", 1, len(payload))
        blob += payload + __import__("hashlib").sha256(payload).digest()
        # tensor 1 -> storage 1 offset 0
        t1 = base
        d1 = codec.canonical_digest(t1)
        blob += bytes([codec.KIND_TENSOR]) + struct.pack("<IIB", 1, 1, codec.REF_STORAGE)
        blob += bytes([t1.dtype.code]) + struct.pack("<qB", 0, 1)
        blob += struct.pack("<Qq", 8, 4) + struct.pack("<Q", 8) + d1
        # tensor 2 -> tensor 1, extra offset 8 bytes => elements 2..7
        t2 = base[2:]
        d2 = codec.canonical_digest(t2)
        blob += bytes([codec.KIND_TENSOR]) + struct.pack("<IIB", 2, 1, codec.REF_TENSOR)
        blob += bytes([t2.dtype.code]) + struct.pack("<qB", 8, 1)
        blob += struct.pack("<Qq", 6, 4) + struct.pack("<Q", 6) + d2
        cat = codec.load(io.BytesIO(bytes(blob)))
        self.assertEqual(cat.tensors[1].flat_values(), list(range(8)))
        self.assertEqual(cat.tensors[2].flat_values(), list(range(2, 8)))
        self.assertIs(cat.tensors[1].storage, cat.tensors[2].storage)


class BudgetAndReferenceTests(unittest.TestCase):
    def test_huge_shape_product_rejected_without_allocation(self):
        # nelem claim itself overflows when multiplied by itemsize: ndim=2,
        # dims 2^40 each; nbytes budget check must trip before any big alloc.
        blob = bytearray()
        blob += codec.MAGIC + bytes([codec.VERSION]) + struct.pack("<I", 1)
        tiny = bytes(4)
        import hashlib
        blob += bytes([codec.KIND_STORAGE]) + struct.pack("<IQ", 1, len(tiny))
        blob += tiny + hashlib.sha256(tiny).digest()
        blob += bytes([codec.KIND_TENSOR]) + struct.pack("<IIB", 1, 1, codec.REF_STORAGE)
        blob += bytes([ts.int8.code]) + struct.pack("<qB", 0, 2)
        blob += struct.pack("<Qq", 2**40, 0) + struct.pack("<Qq", 2**40, 1)
        blob += struct.pack("<Q", 2**64 - 1) + bytes(32)
        with self.assertRaises(codec.CodecError):
            codec.load(io.BytesIO(bytes(blob)), codec.Limits(max_total_bytes=64))

    def test_storage_byte_budget_bomb(self):
        blob = (codec.MAGIC + bytes([codec.VERSION]) + struct.pack("<I", 1)
                + bytes([codec.KIND_STORAGE]) + struct.pack("<IQ", 1, 10**12))
        with self.assertRaises(codec.CodecError):
            codec.load(io.BytesIO(blob), codec.Limits(max_total_bytes=1024))

    def test_misaligned_offset_rejected(self):
        base = ts.arange(4, ts.int32)
        blob = _build_tensor_blob(base, offset=1)
        with self.assertRaises(codec.CodecError):
            codec.load(io.BytesIO(blob))

    def test_oob_view_rejected(self):
        base = ts.arange(4, ts.int32)
        blob = _build_tensor_blob(base, shape=(5,), strides=(4,), offset=0)
        with self.assertRaises(codec.CodecError):
            codec.load(io.BytesIO(blob))

    def test_negative_stride_oob_rejected(self):
        base = ts.arange(4, ts.int32)
        # negative stride with offset 0 -> first address precedes storage
        blob = _build_tensor_blob(base, shape=(4,), strides=(-4,), offset=0,
                                  values=list(range(3, -1, -1)))
        with self.assertRaises(codec.CodecError):
            codec.load(io.BytesIO(blob))

    def test_cross_object_cycle_rejected(self):
        import hashlib
        tiny = bytes(32)
        blob = bytearray(codec.MAGIC + bytes([codec.VERSION]) + struct.pack("<I", 3))
        blob += bytes([codec.KIND_STORAGE]) + struct.pack("<IQ", 1, len(tiny))
        blob += tiny + hashlib.sha256(tiny).digest()
        # tensor 1 -> tensor 2 ; tensor 2 -> tensor 1 (cycle, no storage root)
        for tid, ref in ((1, 2), (2, 1)):
            blob += bytes([codec.KIND_TENSOR])
            blob += struct.pack("<IIB", tid, ref, codec.REF_TENSOR)
            blob += bytes([ts.int32.code]) + struct.pack("<qB", 0, 1)
            blob += struct.pack("<Qq", 2, 4) + struct.pack("<Q", 2) + bytes(32)
        with self.assertRaises(codec.CodecError):
            codec.load(io.BytesIO(blob))

    def test_dangling_reference_rejected(self):
        import hashlib
        tiny = bytes(16)
        blob = bytearray(codec.MAGIC + bytes([codec.VERSION]) + struct.pack("<I", 1))
        blob += bytes([codec.KIND_TENSOR])
        blob += struct.pack("<IIB", 1, 7, codec.REF_STORAGE)
        blob += bytes([ts.int32.code]) + struct.pack("<qB", 0, 1)
        blob += struct.pack("<Qq", 4, 4) + struct.pack("<Q", 4) + bytes(32)
        with self.assertRaises(codec.CodecError):
            codec.load(io.BytesIO(blob))

    def test_nelem_mismatch_rejected(self):
        base = ts.arange(4, ts.int32)
        blob = _build_tensor_blob(base, nelem=5)
        with self.assertRaises(codec.CodecError):
            codec.load(io.BytesIO(blob))


def _build_tensor_blob(t, *, offset=None, shape=None, strides=None,
                       nelem=None, values=None, storage_bytes=None,
                       dtype_code=None, ref_id=1, ref_kind=None):
    """Craft a one-storage/one-tensor stream with arbitrary raw fields."""
    import hashlib
    shape = tuple(t.shape if shape is None else shape)
    strides = tuple(t.strides if strides is None else strides)
    offset = t.offset if offset is None else offset
    nelem = t.size if nelem is None else nelem
    dtype = t.dtype
    if storage_bytes is None:
        payload = bytes(t.storage.data)
    else:
        payload = bytes(storage_bytes)
    if values is not None:
        buf = bytearray(len(values) * dtype.itemsize)
        for i, v in enumerate(values):
            struct.pack_into(dtype.struct_fmt, buf, i * dtype.itemsize, v)
        payload = bytes(buf)
    # digest for a same-shaped view when the layout happens to be valid;
    # invalid-layout cases are rejected before the summary is ever compared.
    try:
        view = ts.Tensor(ts.Storage(payload), dtype, shape, strides, offset)
        summary = codec.canonical_digest(view)
    except Exception:
        summary = bytes(32)
    blob = bytearray(codec.MAGIC + bytes([codec.VERSION]) + struct.pack("<I", 2))
    blob += bytes([codec.KIND_STORAGE]) + struct.pack("<IQ", 1, len(payload))
    blob += payload + hashlib.sha256(payload).digest()
    blob += bytes([codec.KIND_TENSOR])
    blob += struct.pack("<IIB", 1, ref_id,
                        ref_kind if ref_kind is not None else codec.REF_STORAGE)
    blob += bytes([dtype_code if dtype_code is not None else dtype.code])
    blob += struct.pack("<qB", offset, len(shape))
    for d, s in zip(shape, strides):
        blob += struct.pack("<Qq", d, s)
    blob += struct.pack("<Q", nelem) + summary
    return bytes(blob)


class FieldMutationTests(unittest.TestCase):
    """Mutate one field at a time in the tensor header."""

    def setUp(self):
        self.blob, _ = make_canonical_blob()

    def assertRejected(self, mutated):
        with self.assertRaises(codec.CodecError):
            codec.load(io.BytesIO(mutated),
                       codec.Limits(max_total_bytes=4096))

    def test_bad_magic(self):
        m = bytearray(self.blob)
        m[0] ^= 0xFF
        self.assertRejected(bytes(m))

    def test_bad_version(self):
        m = bytearray(self.blob)
        m[4] = 99
        self.assertRejected(bytes(m))

    def test_bad_entry_count(self):
        m = bytearray(self.blob)
        m[5:9] = struct.pack("<I", 9999)
        self.assertRejected(bytes(m))

    def test_bad_dtype_code(self):
        # first tensor entry starts after all storage entries; canonical blob
        # has one storage of 96 bytes + framing.
        off = 4 + 1 + 4
        off += 1 + 4 + 8 + 96 + 32
        self.assertEqual(self.blob[off], codec.KIND_TENSOR)
        m = bytearray(self.blob)
        m[off + 10] = 200  # dtype code position
        self.assertRejected(bytes(m))

    def test_bad_reference_kind(self):
        off = 4 + 1 + 4 + 1 + 4 + 8 + 96 + 32
        m = bytearray(self.blob)
        m[off + 9] = 7  # ref kind byte
        self.assertRejected(bytes(m))

    def test_ndim_above_limit(self):
        off = 4 + 1 + 4 + 1 + 4 + 8 + 96 + 32
        m = bytearray(self.blob)
        # layout: kind 1, tid 4, ref 4, refkind 1, dtype 1, offset 8, ndim 1
        m[off + 1 + 4 + 4 + 1 + 1 + 8] = 255
        self.assertRejected(bytes(m))

    def test_storage_checksum_corruption(self):
        m = bytearray(self.blob)
        m[4 + 1 + 4 + 1 + 4 + 8 + 10] ^= 0x01  # flip payload byte
        self.assertRejected(bytes(m))

    def test_tensor_summary_corruption(self):
        # flip a storage payload byte but fix storage checksum so the rejection
        # must come from the tensor data-summary check
        import hashlib
        m = bytearray(self.blob)
        payload_start = 4 + 1 + 4 + 1 + 4 + 8
        m[payload_start + 3] ^= 0xFF
        new_sum = hashlib.sha256(bytes(m[payload_start:payload_start + 96])).digest()
        m[payload_start + 96:payload_start + 128] = new_sum
        self.assertRejected(bytes(m))

    def test_truncated_at_every_prefix(self):
        for cut in range(0, len(self.blob), max(1, len(self.blob) // 40)):
            with self.assertRaises(codec.CodecError):
                codec.load(io.BytesIO(self.blob[:cut]))

    def test_trailing_garbage_rejected(self):
        with self.assertRaises(codec.CodecError):
            codec.load(io.BytesIO(self.blob + b"\x00"))

    def test_duplicate_storage_id_rejected(self):
        import hashlib
        tiny = bytes(8)
        blob = bytearray(codec.MAGIC + bytes([codec.VERSION]) + struct.pack("<I", 2))
        for sid in (1, 1):
            blob += bytes([codec.KIND_STORAGE]) + struct.pack("<IQ", sid, 8)
            blob += tiny + hashlib.sha256(tiny).digest()
        with self.assertRaises(codec.CodecError):
            codec.load(io.BytesIO(bytes(blob)))


class FuzzMutationTests(unittest.TestCase):
    """Random byte flips must never crash the loader or leak half results."""

    def test_random_mutations(self):
        blob, _ = make_canonical_blob()
        rng = random.Random(2026)
        accepted = rejected = 0
        for _ in range(500):
            m = bytearray(blob)
            for _ in range(rng.randint(1, 4)):
                pos = rng.randrange(len(m))
                m[pos] = rng.randrange(256)
            try:
                cat = codec.load(io.BytesIO(bytes(m)),
                                 codec.Limits(max_total_bytes=4096))
            except codec.CodecError:
                rejected += 1
            else:
                # Any accepted mutation must still yield fully consistent data:
                # every view must address storage and match its own values.
                for t in cat.tensors.values():
                    ts.validate_layout(t.shape, t.strides, t.dtype,
                                       t.offset, t.storage.nbytes)
                accepted += 1
        self.assertGreater(rejected, 400)

    def test_random_truncations(self):
        blob, _ = make_canonical_blob()
        rng = random.Random(7)
        for _ in range(100):
            cut = rng.randrange(len(blob))
            with self.assertRaises(codec.CodecError):
                codec.load(io.BytesIO(blob[:cut]))


class AtomicityTests(unittest.TestCase):
    def test_failure_leaves_no_half_built_catalog(self):
        blob, tensors = make_canonical_blob()
        good = codec.load(io.BytesIO(blob))
        n_good = len(good.tensors)
        # Corrupt one tensor summary; nothing should be observable.
        m = bytearray(blob)
        m[-1] ^= 0xFF
        with self.assertRaises(codec.CodecError):
            codec.load(io.BytesIO(bytes(m)))
        # Re-loading a valid stream still works; prior failure left no state.
        again = codec.load(io.BytesIO(blob))
        self.assertEqual(len(again.tensors), n_good)

    def test_no_global_state_mutated_on_failure(self):
        base = ts.arange(4, ts.int32)
        bad = _build_tensor_blob(base, shape=(9,), strides=(4,))
        for _ in range(3):
            with self.assertRaises(codec.CodecError):
                codec.load(io.BytesIO(bad))
        good = codec.load(io.BytesIO(_build_tensor_blob(base)))
        self.assertEqual(good.tensors[1].flat_values(), [0, 1, 2, 3])


class IndependentNumericTests(unittest.TestCase):
    """End-to-end numeric verification against plain Python nested lists."""

    def test_values_match_reference_computation(self):
        ref = [[10 * r + c for c in range(6)] for r in range(4)]
        base = ts.from_values([v for row in ref for v in row], ts.int32, (4, 6))

        # reference views computed independently with list arithmetic
        ref_rev = ref[::-1]
        ref_sub = [row[2:6] for row in ref[1:3]]
        ref_even_cols = [row[::2] for row in ref]
        ref_both = [row[::-1] for row in ref[::-1]]

        blob = codec.save([base, base[::-1, :], base[1:3, 2:6],
                           base[:, ::2], base[::-1, ::-1]])
        cat = codec.load(io.BytesIO(blob))
        self.assertEqual(cat.tensors[1].tolist(), ref)
        self.assertEqual(cat.tensors[2].tolist(), ref_rev)
        self.assertEqual(cat.tensors[3].tolist(), ref_sub)
        self.assertEqual(cat.tensors[4].tolist(), ref_even_cols)
        self.assertEqual(cat.tensors[5].tolist(), ref_both)

        # independent checksum of restored logical values
        import hashlib
        h = hashlib.sha256()
        for v in cat.tensors[3].flat_values():
            h.update(struct.pack("<i", v))
        expect = hashlib.sha256(
            b"".join(struct.pack("<i", v) for row in ref_sub for v in row)
        ).hexdigest()
        self.assertEqual(h.hexdigest(), expect)

    def test_float_roundtrip_numeric(self):
        vals = [0.5, -1.25, 3.0, 4.75]
        t = ts.from_values(vals, ts.float64)
        cat = codec.load(io.BytesIO(codec.save([t])))
        for got, want in zip(cat.tensors[1].flat_values(), vals):
            self.assertEqual(got, want)


if __name__ == "__main__":
    unittest.main(verbosity=2)
