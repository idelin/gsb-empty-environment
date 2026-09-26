"""End-to-end demo: build, serialize, restore, verify, and attack the format."""
import io
import json
import os
import struct
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from minitensor import Tensor, dumps, loads
from minitensor.format import MAGIC, RestoreError
from minitensor.verify import matmul_tensor, reference_matmul


def show(title):
    print(f"\n== {title} ==")


def main():
    show("1. Build tensors (overlapping views, negative stride)")
    base = Tensor.from_flat([1.0, 2.0, 3.0, 4.0, 5.0, 6.0], "f8")
    matrix = base.as_strided((2, 3), (3, 1), offset=0)
    flip = base.as_strided((3,), (-2,), offset=5)
    print("matrix:", matrix.tolist())
    print("flip  :", flip.tolist(), "(negative stride, shares storage)")

    show("2. Serialize (dtype/shape/strides/offset + SHA-256 digest)")
    blob = dumps({"matrix": matrix, "flip": flip})
    (hlen,) = struct.unpack("<Q", blob[4:12])
    header = json.loads(blob[12 : 12 + hlen])
    print(json.dumps(header, indent=2))
    print(f"total bytes: {len(blob)} (header {hlen}, payload {len(blob) - 12 - hlen})")

    show("3. Restore with a byte budget; sharing identity preserved")
    out = loads(blob, byte_budget=1 << 20)
    print("restored matrix:", out["matrix"].tolist())
    print("restored flip  :", out["flip"].tolist())
    print("shares storage :", out["matrix"].storage is out["flip"].storage)
    out["flip"][0] = 66.0
    print("write via flip -> matrix[1,2] =", out["matrix"][1, 2])

    show("4. Independent numerical verification (matmul)")
    a = Tensor.from_flat([float(v) for v in range(12)], "f8", shape=(3, 4))
    b = Tensor.from_flat([float(v * v - 3) for v in range(8)], "f8", shape=(4, 2))
    got = matmul_tensor(a, b)
    want = reference_matmul(
        [[float(v) for v in range(12)][i : i + 4] for i in (0, 4, 8)],
        [[float(v * v - 3) for v in range(8)][i : i + 2] for i in (0, 2, 4, 6)],
    )
    print("tensor matmul  :", got)
    print("reference      :", want)
    assert got == want
    print("numerical check: PASS")

    show("5. Hostile inputs are rejected atomically")
    attacks = {}

    truncated = blob[:-5]
    attacks["truncated payload"] = truncated

    corrupted = bytearray(blob)
    corrupted[-1] ^= 0xFF
    attacks["digest mismatch"] = bytes(corrupted)

    def with_header(mutate):
        hdr = json.loads(blob[12 : 12 + hlen])
        payload = blob[12 + hlen :]
        mutate(hdr)
        hb = json.dumps(hdr, separators=(",", ":"), sort_keys=True).encode()
        return MAGIC + struct.pack("<Q", len(hb)) + hb + payload

    attacks["misaligned offset"] = with_header(
        lambda h: h["tensors"][0].update(offset_bytes=4)
    )
    attacks["shape overflow (2^32 x 2^32)"] = with_header(
        lambda h: h["tensors"][0].update(shape=[1 << 32, 1 << 32], strides=[1, 1])
    )
    attacks["allocation bomb (1e9 x 1e9)"] = with_header(
        lambda h: h["tensors"][0].update(shape=[10**9, 10**9], strides=[1, 1])
    )
    attacks["alias cycle"] = with_header(
        lambda h: (
            h["storages"].extend([{"id": 1, "alias_of": 2}, {"id": 2, "alias_of": 1}]),
            h["tensors"][0].update(storage=1),
        )
    )
    attacks["negative stride underflow"] = with_header(
        lambda h: h["tensors"][1].update(shape=[4], strides=[-2])
    )

    for label, bad in attacks.items():
        try:
            loads(bad, byte_budget=1 << 20)
        except RestoreError as exc:
            print(f"  rejected [{type(exc).__name__:<20}] {label}: {exc}")
        else:
            print(f"  !! NOT rejected: {label}")
            raise SystemExit(1)
    print("all attacks rejected; no partial tensors published")


if __name__ == "__main__":
    main()
