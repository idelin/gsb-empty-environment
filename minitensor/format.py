"""Streaming tensor serialization with strict, budget-checked restore.

Wire format (little-endian)::

    MAGIC        4 bytes   b"MNT1"
    header_len   8 bytes   unsigned LE, length of the JSON header
    header       JSON      {"version":1, "storages":[...], "tensors":[...]}
    payload      raw bytes of every root storage, in header order

Restore protocol (fail-closed, atomic publish):

1. read + parse header (bounded size), validate every field's type/range;
2. resolve the storage reference graph -- aliases may share a root storage,
   cycles / dangling references are rejected;
3. check element counts and byte budget *before* reading the payload, using
   overflow-checked arithmetic (no allocation bombs);
4. read each root storage's bytes exactly; truncation or trailing garbage
   aborts; verify each storage's SHA-256 digest;
5. validate every view (alignment, reachable address range incl. negative
   strides) against the verified storages;
6. only then construct and publish all tensors at once -- a failure at any
   step raises and no half-built result is returned.
"""
from __future__ import annotations

import hashlib
import io
import json
import struct

from .dtypes import is_dtype_name, dtype_of
from .tensor import MAX_ELEMENTS, MAX_NDIM, Storage, Tensor, address_bounds

MAGIC = b"MNT1"
VERSION = 1
MAX_HEADER_BYTES = 1 << 20
DEFAULT_BYTE_BUDGET = 1 << 26  # 64 MiB


class RestoreError(Exception):
    """Base class for every restore failure."""


class FormatError(RestoreError):
    """Malformed framing or header schema violation."""


class TruncatedError(RestoreError):
    """Stream ended before the declared bytes were read."""


class DigestMismatchError(RestoreError):
    """A storage's bytes do not match its declared SHA-256."""


class BudgetExceededError(RestoreError):
    """Element count / byte budget exceeded, or arithmetic overflow."""


class CycleError(RestoreError):
    """Cyclic or dangling storage reference."""


class ViewError(RestoreError):
    """A tensor view is misaligned or reaches outside its storage."""


# ---------------------------------------------------------------------------
# serialize
# ---------------------------------------------------------------------------
def dumps(tensors) -> bytes:
    """Serialize a ``{name: Tensor}`` mapping to bytes.

    Storages shared between tensors are emitted once; views keep their
    shape/strides/offset so overlapping-view identity survives a round trip.
    """
    if not hasattr(tensors, "items"):
        raise TypeError("expected a mapping of name -> Tensor")
    storage_ids = {}
    storage_entries = []
    payload = bytearray()
    tensor_entries = []
    for name, tensor in tensors.items():
        if not isinstance(name, str):
            raise TypeError("tensor names must be strings")
        if not isinstance(tensor, Tensor):
            raise TypeError(f"{name!r} is not a Tensor")
        sid = storage_ids.get(tensor.storage)
        if sid is None:
            sid = len(storage_entries)
            storage_ids[tensor.storage] = sid
            raw = bytes(tensor.storage.buffer)
            storage_entries.append(
                {
                    "id": sid,
                    "nbytes": len(raw),
                    "sha256": hashlib.sha256(raw).hexdigest(),
                }
            )
            payload += raw
        tensor_entries.append(
            {
                "name": name,
                "storage": sid,
                "dtype": tensor.dtype.name,
                "offset_bytes": tensor.offset * tensor.dtype.itemsize,
                "shape": list(tensor.shape),
                "strides": list(tensor.strides),
            }
        )
    header = {
        "version": VERSION,
        "storages": storage_entries,
        "tensors": tensor_entries,
    }
    header_bytes = json.dumps(header, separators=(",", ":"), sort_keys=True).encode("utf-8")
    return MAGIC + struct.pack("<Q", len(header_bytes)) + header_bytes + bytes(payload)


# ---------------------------------------------------------------------------
# low-level stream helpers
# ---------------------------------------------------------------------------
def _read_exact(stream, n: int, what: str) -> bytes:
    chunks = []
    remaining = n
    while remaining > 0:
        chunk = stream.read(remaining)
        if not chunk:
            raise TruncatedError(f"stream ended {remaining} bytes early while reading {what}")
        chunks.append(chunk)
        remaining -= len(chunk)
    return b"".join(chunks)


def _is_int(v) -> bool:
    return isinstance(v, int) and not isinstance(v, bool)


def _checked_product(dims, limit: int, what: str) -> int:
    acc = 1
    for d in dims:
        acc *= d
        if acc > limit:
            raise BudgetExceededError(f"{what} exceeds limit ({acc} > {limit})")
    return acc


# ---------------------------------------------------------------------------
# header validation
# ---------------------------------------------------------------------------
_STORAGE_ROOT_KEYS = {"id", "nbytes", "sha256"}
_STORAGE_ALIAS_KEYS = {"id", "alias_of"}
_TENSOR_KEYS = {"name", "storage", "dtype", "offset_bytes", "shape", "strides"}


def _validate_header(obj, byte_budget: int):
    if not isinstance(obj, dict):
        raise FormatError("header must be a JSON object")
    if set(obj) != {"version", "storages", "tensors"}:
        raise FormatError("header must contain exactly version/storages/tensors")
    if obj["version"] != VERSION:
        raise FormatError(f"unsupported version {obj['version']!r}")
    storages = obj["storages"]
    tensors = obj["tensors"]
    if not isinstance(storages, list) or not isinstance(tensors, list):
        raise FormatError("storages and tensors must be lists")

    # -- storage entries ---------------------------------------------------
    entries = {}
    total_bytes = 0
    for entry in storages:
        if not isinstance(entry, dict):
            raise FormatError("storage entry must be an object")
        keys = set(entry)
        if keys not in (_STORAGE_ROOT_KEYS, _STORAGE_ALIAS_KEYS):
            raise FormatError(f"bad storage entry keys: {sorted(keys)}")
        sid = entry["id"]
        if not _is_int(sid) or sid < 0:
            raise FormatError("storage id must be a non-negative int")
        if sid in entries:
            raise FormatError(f"duplicate storage id {sid}")
        if "alias_of" in entry:
            target = entry["alias_of"]
            if not _is_int(target) or target < 0:
                raise FormatError("alias_of must be a non-negative int")
        else:
            nbytes = entry["nbytes"]
            if not _is_int(nbytes) or nbytes < 0:
                raise FormatError("nbytes must be a non-negative int")
            if nbytes > byte_budget:
                raise BudgetExceededError(f"storage {sid} of {nbytes} bytes exceeds budget")
            total_bytes += nbytes
            if total_bytes > byte_budget:
                raise BudgetExceededError("combined storage bytes exceed budget")
            digest = entry["sha256"]
            if (
                not isinstance(digest, str)
                or len(digest) != 64
                or any(c not in "0123456789abcdef" for c in digest)
            ):
                raise FormatError("sha256 must be 64 lowercase hex chars")
        entries[sid] = entry

    # -- resolve alias graph (sharing allowed, cycles rejected) -------------
    root_of = {}

    def resolve(sid, trail):
        if sid in root_of:
            return root_of[sid]
        if sid in trail:
            raise CycleError(f"cyclic storage reference involving id {sid}")
        entry = entries.get(sid)
        if entry is None:
            raise CycleError(f"dangling storage reference to id {sid}")
        if "alias_of" in entry:
            root = resolve(entry["alias_of"], trail | {sid})
        else:
            root = sid
        root_of[sid] = root
        return root

    for sid in entries:
        resolve(sid, frozenset())

    # -- tensor entries ------------------------------------------------------
    views = []
    names = set()
    for entry in tensors:
        if not isinstance(entry, dict) or set(entry) != _TENSOR_KEYS:
            raise FormatError(f"tensor entry must have keys {sorted(_TENSOR_KEYS)}")
        name = entry["name"]
        if not isinstance(name, str) or not name:
            raise FormatError("tensor name must be a non-empty string")
        if name in names:
            raise FormatError(f"duplicate tensor name {name!r}")
        names.add(name)
        sid = entry["storage"]
        if not _is_int(sid) or sid not in entries:
            raise FormatError(f"tensor {name!r} references unknown storage {sid!r}")
        dtype_name = entry["dtype"]
        if not is_dtype_name(dtype_name):
            raise FormatError(f"unknown dtype {dtype_name!r}")
        dt = dtype_of(dtype_name)
        offset_bytes = entry["offset_bytes"]
        if not _is_int(offset_bytes) or offset_bytes < 0:
            raise FormatError("offset_bytes must be a non-negative int")
        if offset_bytes % dt.itemsize != 0:
            raise ViewError(
                f"tensor {name!r}: offset {offset_bytes} misaligned for {dt.name}"
            )
        shape = entry["shape"]
        strides = entry["strides"]
        if not isinstance(shape, list) or not isinstance(strides, list):
            raise FormatError("shape and strides must be lists")
        if len(shape) != len(strides):
            raise FormatError("shape/strides length mismatch")
        if len(shape) > MAX_NDIM:
            raise FormatError("too many dimensions")
        if any(not _is_int(n) or n < 0 for n in shape):
            raise FormatError("shape must be non-negative ints")
        if any(not _is_int(s) for s in strides):
            raise FormatError("strides must be ints")
        if any(n > MAX_ELEMENTS for n in shape) or any(
            abs(s) > MAX_ELEMENTS for s in strides
        ):
            raise BudgetExceededError("shape/stride overflow")
        # element-count vs byte budget, with overflow-checked multiplication
        elem_limit = min(MAX_ELEMENTS, byte_budget // dt.itemsize)
        _checked_product(shape, elem_limit, f"tensor {name!r} element count")
        views.append(
            {
                "name": name,
                "root": root_of[sid],
                "dtype": dt,
                "offset": offset_bytes // dt.itemsize,
                "shape": tuple(shape),
                "strides": tuple(strides),
            }
        )
    return entries, views


def _validate_view_ranges(entries, views):
    for view in views:
        nbytes = entries[view["root"]]["nbytes"]
        capacity = nbytes // view["dtype"].itemsize
        try:
            bounds = address_bounds(view["shape"], view["strides"], view["offset"])
        except ValueError as exc:
            raise ViewError(f"tensor {view['name']!r}: {exc}") from exc
        if bounds is None:
            if not 0 <= view["offset"] <= capacity:
                raise ViewError(f"tensor {view['name']!r}: offset outside storage")
            continue
        lo, hi = bounds
        if lo < 0:
            raise ViewError(f"tensor {view['name']!r}: reaches before storage start")
        if hi >= capacity:
            raise ViewError(f"tensor {view['name']!r}: reaches past storage end")


# ---------------------------------------------------------------------------
# restore
# ---------------------------------------------------------------------------
def load(stream, *, byte_budget: int = DEFAULT_BYTE_BUDGET):
    """Restore tensors from a binary stream.  Returns ``{name: Tensor}``.

    Raises :class:`RestoreError` (or a subclass) on any problem; on failure
    nothing is returned and no partially built tensors are published.
    """
    if not _is_int(byte_budget) or byte_budget < 0:
        raise ValueError("byte_budget must be a non-negative int")
    if _read_exact(stream, len(MAGIC), "magic") != MAGIC:
        raise FormatError("bad magic")
    (header_len,) = struct.unpack("<Q", _read_exact(stream, 8, "header length"))
    if header_len > MAX_HEADER_BYTES:
        raise FormatError(f"header too large ({header_len} bytes)")
    header_bytes = _read_exact(stream, header_len, "header")
    try:
        obj = json.loads(header_bytes.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise FormatError(f"invalid header JSON: {exc}") from exc

    entries, views = _validate_header(obj, byte_budget)
    _validate_view_ranges(entries, views)

    # All metadata validated *before* any payload byte is consumed or any
    # allocation happens; from here, build verified storages.
    root_ids = [sid for sid, e in entries.items() if "alias_of" not in e]
    storages = {}
    for sid in root_ids:
        entry = entries[sid]
        raw = _read_exact(stream, entry["nbytes"], f"storage {sid} payload")
        if hashlib.sha256(raw).hexdigest() != entry["sha256"]:
            raise DigestMismatchError(f"storage {sid} digest mismatch")
        storages[sid] = Storage.from_bytes(raw)
    if stream.read(1):
        raise FormatError("trailing bytes after payload")

    # Atomic publish: construct every view only after all storages verified.
    result = {}
    for view in views:
        result[view["name"]] = Tensor(
            storages[view["root"]],
            view["dtype"],
            view["shape"],
            view["strides"],
            view["offset"],
        )
    return result


def loads(data: bytes, *, byte_budget: int = DEFAULT_BYTE_BUDGET):
    """Restore tensors from a ``bytes`` object."""
    if not isinstance(data, (bytes, bytearray, memoryview)):
        raise TypeError("loads expects a bytes-like object")
    return load(io.BytesIO(bytes(data)), byte_budget=byte_budget)
