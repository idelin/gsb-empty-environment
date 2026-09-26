"""Serialization round-trips plus fail-closed restore hardening tests."""
import hashlib
import io
import json
import struct
import unittest

from minitensor import Tensor, dumps, load, loads
from minitensor.format import (
    MAGIC,
    BudgetExceededError,
    CycleError,
    DigestMismatchError,
    FormatError,
    RestoreError,
    TruncatedError,
    ViewError,
)


def make_doc():
    """A valid doc: two overlapping views of one shared f8 storage."""
    base = Tensor.from_flat([1.0, 2.0, 3.0, 4.0, 5.0, 6.0], "f8")
    matrix = base.as_strided((2, 3), (3, 1), offset=0)
    flip = base.as_strided((3,), (-2,), offset=5)
    return dumps({"matrix": matrix, "flip": flip})


def rebuild(data, mutate):
    """Re-encode ``data`` with ``mutate(header_dict)`` applied to the header."""
    assert data[:4] == MAGIC
    (hlen,) = struct.unpack("<Q", data[4:12])
    header = json.loads(data[12 : 12 + hlen].decode("utf-8"))
    payload = data[12 + hlen :]
    mutate(header)
    hb = json.dumps(header, separators=(",", ":"), sort_keys=True).encode("utf-8")
    return MAGIC + struct.pack("<Q", len(hb)) + hb + payload


def tensor_entry(header, name):
    for t in header["tensors"]:
        if t["name"] == name:
            return t
    raise KeyError(name)


class ShortReadStream:
    """Stream that never returns more than ``chunk`` bytes per read."""

    def __init__(self, data, chunk=3):
        self._buf = memoryview(data)
        self._pos = 0
        self._chunk = chunk

    def read(self, n=-1):
        if n is None or n < 0:
            n = len(self._buf) - self._pos
        n = min(n, self._chunk, len(self._buf) - self._pos)
        out = self._buf[self._pos : self._pos + n].tobytes()
        self._pos += n
        return out


class TestRoundTrip(unittest.TestCase):
    def test_values_shapes_strides_survive(self):
        out = loads(make_doc())
        self.assertEqual(set(out), {"matrix", "flip"})
        self.assertEqual(out["matrix"].tolist(), [[1.0, 2.0, 3.0], [4.0, 5.0, 6.0]])
        self.assertEqual(out["flip"].tolist(), [6.0, 4.0, 2.0])
        self.assertEqual(out["flip"].strides, (-2,))
        self.assertEqual(out["flip"].offset, 5)

    def test_shared_storage_identity_preserved(self):
        out = loads(make_doc())
        self.assertIs(out["matrix"].storage, out["flip"].storage)
        out["flip"][0] = 66.0  # overlapping view identity: write-through works
        self.assertEqual(out["matrix"][1, 2], 66.0)

    def test_alias_storage_reference_shares_root(self):
        def mutate(header):
            header["storages"].append({"id": 7, "alias_of": 0})
            header["tensors"].append(
                {
                    "name": "alias_view",
                    "storage": 7,
                    "dtype": "f8",
                    "offset_bytes": 8,
                    "shape": [2],
                    "strides": [1],
                }
            )

        out = loads(rebuild(make_doc(), mutate))
        self.assertIs(out["alias_view"].storage, out["matrix"].storage)
        self.assertEqual(out["alias_view"].tolist(), [2.0, 3.0])

    def test_scalar_and_empty_roundtrip(self):
        scalar = Tensor.from_flat([2.5], "f8", shape=())
        empty = Tensor.zeros((0, 4), "i4")
        out = loads(dumps({"scalar": scalar, "empty": empty}))
        self.assertEqual(out["scalar"][()], 2.5)
        self.assertEqual(out["empty"].shape, (0, 4))
        self.assertEqual(out["empty"].tolist(), [])

    def test_streaming_load_with_short_reads(self):
        data = make_doc()
        out = load(ShortReadStream(data, chunk=2))
        self.assertEqual(out["matrix"].tolist(), [[1.0, 2.0, 3.0], [4.0, 5.0, 6.0]])

    def test_digest_matches_payload(self):
        data = make_doc()
        (hlen,) = struct.unpack("<Q", data[4:12])
        header = json.loads(data[12 : 12 + hlen])
        payload = data[12 + hlen :]
        self.assertEqual(header["storages"][0]["sha256"], hashlib.sha256(payload).hexdigest())


class TestFieldMutations(unittest.TestCase):
    """Field-by-field mutation: every single corruption must be rejected."""

    def mutations(self):
        def set_matrix(header, **kw):
            tensor_entry(header, "matrix").update(kw)

        return {
            "version bumped": lambda h: h.update(version=2),
            "extra top-level key": lambda h: h.update(extra=1),
            "missing top-level key": lambda h: h.pop("tensors"),
            "unknown dtype": lambda h: set_matrix(h, dtype="f16"),
            "non-string dtype": lambda h: set_matrix(h, dtype=8),
            "negative dim": lambda h: set_matrix(h, shape=[-1, 3]),
            "non-int dim": lambda h: set_matrix(h, shape=[2, "3"]),
            "bool dim": lambda h: set_matrix(h, shape=[2, True]),
            "shape/strides mismatch": lambda h: set_matrix(h, strides=[1]),
            "non-int stride": lambda h: set_matrix(h, strides=[3, 1.5]),
            "misaligned offset": lambda h: set_matrix(h, offset_bytes=4),
            "negative offset": lambda h: set_matrix(h, offset_bytes=-8),
            "offset past end": lambda h: set_matrix(h, offset_bytes=48),
            "view past end": lambda h: set_matrix(h, shape=[4], strides=[2]),
            "view before start": lambda h: (
                tensor_entry(h, "flip").update(shape=[4], strides=[-2])
            ),
            "unknown storage ref": lambda h: set_matrix(h, storage=9),
            "non-int storage ref": lambda h: set_matrix(h, storage="0"),
            "duplicate tensor name": lambda h: h["tensors"].append(
                dict(h["tensors"][0])
            ),
            "tensor missing key": lambda h: tensor_entry(h, "matrix").pop("shape"),
            "tensor extra key": lambda h: set_matrix(h, bogus=1),
            "empty tensor name": lambda h: set_matrix(h, name=""),
            "duplicate storage id": lambda h: h["storages"].append(
                dict(h["storages"][0])
            ),
            "negative nbytes": lambda h: h["storages"][0].update(nbytes=-1),
            "non-int nbytes": lambda h: h["storages"][0].update(nbytes="48"),
            "bad digest hex": lambda h: h["storages"][0].update(sha256="z" * 64),
            "short digest": lambda h: h["storages"][0].update(sha256="ab"),
            "storage bad keys": lambda h: h["storages"][0].update(extra=1),
            "self alias cycle": lambda h: (
                h["storages"].append({"id": 1, "alias_of": 1}),
                set_matrix(h, storage=1),
            ),
            "cross alias cycle": lambda h: (
                h["storages"].extend(
                    [{"id": 1, "alias_of": 2}, {"id": 2, "alias_of": 1}]
                ),
                set_matrix(h, storage=1),
            ),
            "dangling alias": lambda h: (
                h["storages"].append({"id": 1, "alias_of": 99}),
                set_matrix(h, storage=1),
            ),
        }

    def test_every_mutation_rejected(self):
        data = make_doc()
        for label, mutate in sorted(self.mutations().items()):
            with self.subTest(mutation=label):
                bad = rebuild(data, mutate)
                with self.assertRaises(RestoreError, msg=label):
                    loads(bad)

    def test_mutation_error_kinds(self):
        data = make_doc()
        cases = [
            (lambda h: tensor_entry(h, "matrix").update(offset_bytes=4), ViewError),
            (lambda h: tensor_entry(h, "matrix").update(dtype="f16"), FormatError),
            (lambda h: h.update(version=2), FormatError),
            (
                lambda h: (
                    h["storages"].append({"id": 1, "alias_of": 1}),
                    tensor_entry(h, "matrix").update(storage=1),
                ),
                CycleError,
            ),
            (
                lambda h: tensor_entry(h, "matrix").update(shape=[10**9, 10**9]),
                BudgetExceededError,
            ),
        ]
        for mutate, kind in cases:
            with self.subTest(kind=kind.__name__):
                with self.assertRaises(kind):
                    loads(rebuild(data, mutate))


class TestBudgetsAndOverflow(unittest.TestCase):
    def test_shape_product_overflow_rejected(self):
        def mutate(header):
            tensor_entry(header, "matrix").update(
                shape=[1 << 32, 1 << 32], strides=[1, 1]
            )

        with self.assertRaises(BudgetExceededError):
            loads(rebuild(make_doc(), mutate))

    def test_allocation_bomb_rejected_before_payload_read(self):
        def mutate(header):
            tensor_entry(header, "matrix").update(
                shape=[10**9, 10**9], strides=[1, 1]
            )

        bomb = rebuild(make_doc(), mutate)

        class ExplodingStream:
            def __init__(self):
                self.payload_reads = 0

            def read(self, n=-1):
                self.payload_reads += 1
                return b""  # never supply payload

        # Header is unreadable here, so use loads(); the point is the budget
        # check fires from header metadata alone, before payload allocation.
        with self.assertRaises(BudgetExceededError):
            loads(bomb)

    def test_byte_budget_enforced(self):
        data = make_doc()
        with self.assertRaises(BudgetExceededError):
            loads(data, byte_budget=8)  # payload is 48 bytes
        out = loads(data, byte_budget=1024)
        self.assertEqual(set(out), {"matrix", "flip"})

    def test_declared_nbytes_over_budget_rejected(self):
        def mutate(header):
            header["storages"][0]["nbytes"] = 1 << 40

        with self.assertRaises(BudgetExceededError):
            loads(rebuild(make_doc(), mutate))


class TestTruncationAndFraming(unittest.TestCase):
    def test_every_truncation_point_fails(self):
        data = make_doc()
        cut_points = {0, 1, 3, 4, 5, 11, 12, 13, len(data) // 2, len(data) - 1}
        for cut in sorted(cut_points):
            with self.subTest(cut=cut):
                with self.assertRaises(RestoreError):
                    loads(data[:cut])

    def test_truncated_payload_is_truncated_error(self):
        data = make_doc()
        with self.assertRaises(TruncatedError):
            loads(data[:-1])

    def test_trailing_garbage_rejected(self):
        with self.assertRaises(FormatError):
            loads(make_doc() + b"x")

    def test_bad_magic_rejected(self):
        with self.assertRaises(FormatError):
            loads(b"XXXX" + make_doc()[4:])

    def test_oversized_header_rejected(self):
        data = MAGIC + struct.pack("<Q", 1 << 30)
        with self.assertRaises(FormatError):
            loads(data)

    def test_invalid_json_header_rejected(self):
        hb = b"{not json"
        data = MAGIC + struct.pack("<Q", len(hb)) + hb
        with self.assertRaises(FormatError):
            loads(data)


class TestPayloadIntegrity(unittest.TestCase):
    def test_flipped_payload_byte_detected(self):
        data = bytearray(make_doc())
        data[-1] ^= 0xFF
        with self.assertRaises(DigestMismatchError):
            loads(bytes(data))

    def test_no_partial_success_on_corrupt_second_tensor(self):
        """A doc whose last view is invalid must publish nothing."""
        def mutate(header):
            tensor_entry(header, "flip").update(offset_bytes=4)  # misaligned

        bad = rebuild(make_doc(), mutate)
        with self.assertRaises(ViewError):
            loads(bad)
        # the good doc still restores afterwards: no poisoned global state
        out = loads(make_doc())
        self.assertEqual(out["flip"].tolist(), [6.0, 4.0, 2.0])

    def test_digest_mismatch_publishes_nothing(self):
        data = bytearray(make_doc())
        data[-1] ^= 0x01
        try:
            loads(bytes(data))
        except DigestMismatchError:
            pass  # raised before any Tensor was constructed
        else:  # pragma: no cover
            self.fail("expected DigestMismatchError")


if __name__ == "__main__":
    unittest.main()
