"""Runnable sample: save/restore shared-storage views and show rejections."""

import io
import struct

import codec
import tensor as ts


def main() -> None:
    print("== build tensors over one shared storage ==")
    base = ts.reshape_contiguous(ts.arange(24, ts.int32), (4, 6))
    views = [
        ("base", base),
        ("rows reversed", base[::-1, :]),
        ("sub 1:3,2:6", base[1:3, 2:6]),
        ("every other col", base[:, ::2]),
        ("both reversed", base[::-1, ::-1]),
    ]
    for name, t in views:
        print(f"  {name:16s} shape={t.shape} strides={t.strides} "
              f"offset={t.offset} storage@0x{id(t.storage):x}")

    blob = codec.save([t for _, t in views])
    print(f"\nserialized {len(blob)} bytes for {len(views)} views, 1 storage")

    cat = codec.load(io.BytesIO(blob))
    print("\n== restored views ==")
    storage_ids = {id(t.storage) for t in cat.tensors.values()}
    print(f"distinct backing storages after restore: {len(storage_ids)} "
          "(shared identity preserved)")
    for i in sorted(cat.tensors):
        print(f"  tensor {i}: {cat.tensors[i].tolist()}")

    print("\n== attacks are rejected cleanly ==")

    truncated = blob[: len(blob) // 2]
    try:
        codec.load(io.BytesIO(truncated))
    except codec.CodecError as exc:
        print(f"  truncated stream -> CodecError: {exc}")

    tampered = bytearray(blob)
    tampered[24] ^= 0xFF  # inside storage payload, past the framing fields
    try:
        codec.load(io.BytesIO(bytes(tampered)))
    except codec.CodecError as exc:
        print(f"  flipped storage byte -> CodecError: {exc}")

    bomb = (codec.MAGIC + bytes([codec.VERSION]) + struct.pack("<I", 1)
            + bytes([codec.KIND_STORAGE])
            + struct.pack("<IQ", 1, 10**12))
    try:
        codec.load(io.BytesIO(bomb), codec.Limits(max_total_bytes=1024))
    except codec.CodecError as exc:
        print(f"  1 TB storage claim   -> CodecError: {exc}")

    import hashlib
    tiny = b"\x00" * 4
    huge_shape = (
        codec.MAGIC + bytes([codec.VERSION]) + struct.pack("<I", 2)
        + bytes([codec.KIND_STORAGE]) + struct.pack("<IQ", 1, 4)
        + tiny + hashlib.sha256(tiny).digest()
        + bytes([codec.KIND_TENSOR])
        + struct.pack("<IIB", 1, 1, codec.REF_STORAGE)
        + bytes([ts.int8.code]) + struct.pack("<qB", 0, 2)
        + struct.pack("<Qq", 2**40, 0) + struct.pack("<Qq", 2**40, 1)
        + struct.pack("<Q", 2**64 - 1) + bytes(32)
    )
    try:
        codec.load(io.BytesIO(huge_shape), codec.Limits(max_total_bytes=64))
    except codec.CodecError as exc:
        print(f"  2^40 x 2^40 shape    -> CodecError: {exc}")


if __name__ == "__main__":
    main()
