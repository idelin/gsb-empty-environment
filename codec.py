"""Streaming save/load for small CPU tensors with shared storage and views.

Wire format (little-endian):

  magic(4) = b"PMT1", version u8, entry_count u32
  entries, each:
    kind u8: 1 = storage, 2 = tensor
    storage:  id u32, nbytes u64, payload[nbytes], sha256(32) of payload
    tensor:   id u32, storage_or_tensor_id u32, kind u8(1=storage,2=tensor),
              dtype u8, offset i64, ndim u8,
              ndim x (dim u64, stride i64), nelem u64, sha256(32) summary

Load is two-phase: every entry is fully validated (byte budgets, integer
overflow, reachable address range, reference graph cycles) into a private
staging area, and only then are all views published atomically. Any failure
raises CodecError and no half-built object is made available.
"""

from __future__ import annotations

import hashlib
import io
import struct
from dataclasses import dataclass, field

from tensor import (
    DType,
    LayoutError,
    Storage,
    Tensor,
    dtype_by_code,
    reachable_byte_span,
    validate_layout,
)

MAGIC = b"PMT1"
VERSION = 1
KIND_STORAGE = 1
KIND_TENSOR = 2
REF_STORAGE = 1
REF_TENSOR = 2

DEFAULT_MAX_TOTAL_BYTES = 256 * 1024 * 1024
DEFAULT_MAX_OBJECTS = 10_000
MAX_NDIM = 32
MAX_DIM = 2**63 - 1


class CodecError(ValueError):
    """Raised for any malformed/budget-violating stream."""


@dataclass
class Limits:
    max_total_bytes: int = DEFAULT_MAX_TOTAL_BYTES
    max_objects: int = DEFAULT_MAX_OBJECTS
    max_ndim: int = MAX_NDIM


@dataclass
class Catalog:
    """Result of a load: named maps, replaced atomically on success."""

    storages: dict[int, Storage] = field(default_factory=dict)
    tensors: dict[int, Tensor] = field(default_factory=dict)

    def tensor_values(self) -> dict[int, list]:
        return {i: t.flat_values() for i, t in self.tensors.items()}


def _checked_shape_product(shape, limits: Limits) -> int:
    n = 1
    for d in shape:
        if d < 0:
            raise CodecError(f"negative shape dimension {d}")
        if d == 0:
            return 0
        if n > (2**63 - 1) // d:
            raise CodecError("shape product overflow")
        n *= d
    return n


def canonical_digest(t: Tensor) -> bytes:
    """Data summary: sha256 over (dtype code, shape, logical element bytes)."""
    h = hashlib.sha256()
    h.update(bytes([t.dtype.code]))
    h.update(struct.pack("<B", t.ndim))
    for d in t.shape:
        h.update(struct.pack("<q", d))
    for v in t.flat_values():
        h.update(struct.pack(t.dtype.struct_fmt, v))
    return h.digest()


def _read_exact(src, n: int) -> bytes:
    buf = src.read(n)
    if len(buf) != n:
        raise CodecError("unexpected end of stream")
    return buf


def _read_u8(src) -> int:
    return _read_exact(src, 1)[0]


def _read_u32(src) -> int:
    return struct.unpack("<I", _read_exact(src, 4))[0]


def _read_u64(src) -> int:
    return struct.unpack("<Q", _read_exact(src, 8))[0]


def _read_i64(src) -> int:
    return struct.unpack("<q", _read_exact(src, 8))[0]


def save(tensors: list[Tensor] | dict[object, Tensor], dest=None):
    """Serialize tensors. Accepts a list (ids 1..N) or {id: tensor} dict."""
    if isinstance(tensors, dict):
        id_for_tensor = {int(k): t for k, t in tensors.items()}
        ordered = sorted(id_for_tensor.items())
    else:
        id_for_tensor = {i + 1: t for i, t in enumerate(tensors)}
        ordered = sorted(id_for_tensor.items())
    if len(ordered) > DEFAULT_MAX_OBJECTS:
        raise CodecError("too many tensors")

    storage_ids: dict[int, int] = {}
    storage_order: list[Storage] = []
    for _, t in ordered:
        key = id(t.storage)
        if key not in storage_ids:
            storage_ids[key] = len(storage_order) + 1
            storage_order.append(t.storage)
    total = sum(s.nbytes for s in storage_order)
    if total > DEFAULT_MAX_TOTAL_BYTES:
        raise CodecError("total storage bytes exceed budget")

    out = dest if dest is not None else io.BytesIO()
    n_entries = len(storage_order) + len(ordered)
    out.write(MAGIC)
    out.write(bytes([VERSION]))
    out.write(struct.pack("<I", n_entries))

    for sid, st in enumerate(storage_order, start=1):
        payload = bytes(st.data)
        out.write(bytes([KIND_STORAGE]))
        out.write(struct.pack("<IQ", sid, len(payload)))
        out.write(payload)
        out.write(hashlib.sha256(payload).digest())

    for tid, t in ordered:
        _checked_shape_product(t.shape, Limits())
        out.write(bytes([KIND_TENSOR]))
        out.write(struct.pack("<IIB", tid, storage_ids[id(t.storage)], REF_STORAGE))
        out.write(bytes([t.dtype.code]))
        out.write(struct.pack("<qB", t.offset, t.ndim))
        for d, s in zip(t.shape, t.strides):
            out.write(struct.pack("<Qq", d, s))
        out.write(struct.pack("<Q", t.size))
        out.write(canonical_digest(t))
    if dest is None:
        return out.getvalue()
    return None


@dataclass
class _TEntry:
    tid: int
    ref_id: int
    ref_kind: int
    dtype: DType
    offset: int
    shape: tuple
    strides: tuple
    nelem: int
    digest: bytes


def load(src, limits: Limits | None = None) -> Catalog:
    """Validate-then-publish load. Never returns partially built state."""
    limits = limits or Limits()
    if hasattr(src, "read"):
        stream = src
        owns = False
    else:
        stream = open(src, "rb")
        owns = True
    staging_storages: dict[int, Storage] = {}
    staging_tensors: dict[int, _TEntry] = {}
    total_storage_bytes = 0
    try:
        magic = _read_exact(stream, 4)
        if magic != MAGIC:
            raise CodecError("bad magic")
        version = _read_u8(stream)
        if version != VERSION:
            raise CodecError(f"unsupported version {version}")
        n_entries = _read_u32(stream)
        if n_entries > limits.max_objects * 2:
            raise CodecError("entry count exceeds object budget")

        for _ in range(n_entries):
            kind = _read_u8(stream)
            if kind == KIND_STORAGE:
                sid = _read_u32(stream)
                nbytes = _read_u64(stream)
                if sid == 0:
                    raise CodecError("storage id 0 is reserved")
                if sid in staging_storages:
                    raise CodecError(f"duplicate storage id {sid}")
                if nbytes > limits.max_total_bytes - total_storage_bytes:
                    raise CodecError("storage byte budget exceeded")
                h = hashlib.sha256()
                data = bytearray(nbytes)
                view = memoryview(data)
                got = 0
                while got < nbytes:
                    chunk = stream.read(min(1 << 20, nbytes - got))
                    if not chunk:
                        raise CodecError("truncated storage payload")
                    view[got:got + len(chunk)] = chunk
                    h.update(chunk)
                    got += len(chunk)
                digest = _read_exact(stream, 32)
                if h.digest() != digest:
                    raise CodecError(f"storage {sid} checksum mismatch")
                total_storage_bytes += nbytes
                staging_storages[sid] = Storage(data)
            elif kind == KIND_TENSOR:
                tid, ref_id, ref_kind = struct.unpack(
                    "<IIB", _read_exact(stream, 9)
                )
                if tid == 0:
                    raise CodecError("tensor id 0 is reserved")
                if tid in staging_tensors:
                    raise CodecError(f"duplicate tensor id {tid}")
                if ref_kind not in (REF_STORAGE, REF_TENSOR):
                    raise CodecError("unknown reference kind")
                dtype = dtype_by_code(_read_u8(stream))
                offset = _read_i64(stream)
                ndim = _read_u8(stream)
                if ndim > limits.max_ndim:
                    raise CodecError("ndim exceeds limit")
                shape = []
                strides = []
                for _axis in range(ndim):
                    d = _read_u64(stream)
                    s = _read_i64(stream)
                    if d > MAX_DIM:
                        raise CodecError("dimension too large")
                    shape.append(d)
                    strides.append(s)
                nelem = _read_u64(stream)
                digest = _read_exact(stream, 32)
                prod = _checked_shape_product(shape, limits)
                if prod != nelem:
                    raise CodecError("nelem does not match shape product")
                staging_tensors[tid] = _TEntry(
                    tid, ref_id, ref_kind, dtype, offset,
                    tuple(shape), tuple(strides), nelem, digest,
                )
            else:
                raise CodecError(f"unknown entry kind {kind}")

        tail = stream.read(1)
        if tail:
            raise CodecError("trailing bytes after entries")

        if len(staging_storages) + len(staging_tensors) > limits.max_objects * 2:
            raise CodecError("object count exceeds budget")

        return _resolve_and_publish(staging_storages, staging_tensors, limits)
    except CodecError:
        raise
    except (LayoutError, ValueError, struct.error, OSError, EOFError) as exc:
        raise CodecError(str(exc)) from exc
    finally:
        if owns:
            stream.close()


def _resolve_and_publish(storages, tensors, limits: Limits) -> Catalog:
    """Resolve references (cycle detection), validate ranges, publish atomically."""
    # Phase A: every tensor chain must terminate at a storage; reject cycles/dangling.
    resolved: dict[int, tuple[Storage, int]] = {}

    def resolve(tid: int, seen: frozenset[int]) -> tuple[Storage, int]:
        if tid in resolved:
            return resolved[tid]
        if tid in seen:
            raise CodecError(f"tensor reference cycle through id {tid}")
        entry = tensors.get(tid)
        if entry is None:
            raise CodecError(f"dangling tensor reference {tid}")
        if entry.ref_kind == REF_STORAGE:
            st = storages.get(entry.ref_id)
            if st is None:
                raise CodecError(f"dangling storage reference {entry.ref_id}")
            base = st
            extra = 0
        else:
            base, base_off = resolve(entry.ref_id, seen | {tid})
            extra = base_off
        abs_offset = entry.offset + extra
        resolved[tid] = (base, abs_offset)
        return base, abs_offset

    for tid in tensors:
        resolve(tid, frozenset())

    # Phase B: full layout validation per view (overflow + reachable range).
    built: dict[int, Tensor] = {}
    for tid, e in tensors.items():
        storage, abs_offset = resolved[tid]
        if abs_offset < 0:
            raise CodecError(f"tensor {tid}: offset before storage start")
        element_budget = max(1, limits.max_total_bytes // max(1, e.dtype.itemsize))
        validate_layout(
            e.shape, e.strides, e.dtype, abs_offset, storage.nbytes, element_budget
        )
        if e.shape:
            lo, hi, _ = reachable_byte_span(e.shape, e.strides, e.dtype)
            start = abs_offset + lo
            end = abs_offset + hi
            if not (0 <= start and end <= storage.nbytes):
                raise CodecError(f"tensor {tid}: reachable range outside storage")
        built[tid] = Tensor(storage, e.dtype, e.shape, e.strides, abs_offset)

    # Phase C: verify every data summary (catches corrupt element bytes too).
    for tid, e in tensors.items():
        if canonical_digest(built[tid]) != e.digest:
            raise CodecError(f"tensor {tid}: data summary mismatch")

    # Phase D: atomic publish — single object construction after all checks.
    return Catalog(dict(storages), built)
