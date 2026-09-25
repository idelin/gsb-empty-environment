"""Checksummed binary serialization: size, layout and per-block CRC32."""
from __future__ import annotations

import struct
import zlib

from .matrix import CSC, CSR, ELIMINATE, KEEP, SparseMatrix
from .pages import DEFAULT_PAGE_SIZE

MAGIC = b"SPK1"
BATCH_MAGIC = b"SPKB"
FORMAT_VERSION = 1

HEADER = struct.Struct("<4sIIQQQBB")
BLOCK_COUNT = struct.Struct("<Q")
BLOCK_CRC = struct.Struct("<I")
BATCH_HEADER = struct.Struct("<4sI")
LENGTH64 = struct.Struct("<Q")

LAYOUT_CODE = {CSR: 0, CSC: 1}
CODE_LAYOUT = {0: CSR, 1: CSC}
MODE_CODE = {ELIMINATE: 0, KEEP: 1}
CODE_MODE = {0: ELIMINATE, 1: KEEP}


def pack_block(values, code: str) -> bytes:
    payload = b""
    if values:
        payload = struct.pack(f"<{len(values)}{code}", *values)
    return (
        BLOCK_COUNT.pack(len(values))
        + payload
        + BLOCK_CRC.pack(zlib.crc32(payload))
    )


def unpack_block(blob: bytes, pos: int, code: str):
    if pos + BLOCK_COUNT.size > len(blob):
        raise ValueError("truncated block header")
    (count,) = BLOCK_COUNT.unpack_from(blob, pos)
    pos += BLOCK_COUNT.size
    size = count * 8
    if pos + size + BLOCK_CRC.size > len(blob):
        raise ValueError("truncated block payload")
    payload = blob[pos:pos + size]
    pos += size
    (expected_crc,) = BLOCK_CRC.unpack_from(blob, pos)
    pos += BLOCK_CRC.size
    if zlib.crc32(payload) != expected_crc:
        raise ValueError("block checksum mismatch")
    values = list(struct.unpack(f"<{count}{code}", payload)) if count else []
    return values, pos


def dumps(matrix: SparseMatrix) -> bytes:
    nrows, ncols = matrix.shape
    indptr, indices, data = matrix.arrays()
    header = HEADER.pack(
        MAGIC,
        FORMAT_VERSION,
        matrix.version,
        nrows,
        ncols,
        len(data),
        LAYOUT_CODE[matrix.layout],
        MODE_CODE[matrix.mode],
    )
    return (
        header
        + pack_block(indptr, "q")
        + pack_block(indices, "q")
        + pack_block([float(v) for v in data], "d")
    )


def _validate_structure(shape, layout, indptr, indices, data, nnz):
    nrows, ncols = shape
    major_dim = nrows if layout == CSR else ncols
    minor_dim = ncols if layout == CSR else nrows
    if len(indptr) != major_dim + 1:
        raise ValueError("pointer array length does not match dimension")
    if indptr[0] != 0 or indptr[-1] != nnz:
        raise ValueError("pointer array bounds do not match nnz")
    for k in range(major_dim):
        if indptr[k] > indptr[k + 1]:
            raise ValueError("pointer array is not monotonic")
    if len(indices) != nnz or len(data) != nnz:
        raise ValueError("index/data arrays must have length nnz")
    for v in indices:
        if not 0 <= v < minor_dim:
            raise ValueError("minor index out of range")
    for k in range(major_dim):
        segment = indices[indptr[k]:indptr[k + 1]]
        if any(segment[i] >= segment[i + 1] for i in range(len(segment) - 1)):
            raise ValueError("minor indices are not strictly sorted")


def loads(blob: bytes, page_size: int = DEFAULT_PAGE_SIZE) -> SparseMatrix:
    if len(blob) < HEADER.size:
        raise ValueError("truncated header")
    magic, fmt_version, matrix_version, nrows, ncols, nnz, layout_code, mode_code = (
        HEADER.unpack_from(blob, 0)
    )
    if magic != MAGIC:
        raise ValueError("bad magic")
    if fmt_version != FORMAT_VERSION:
        raise ValueError("unsupported format version")
    if layout_code not in CODE_LAYOUT or mode_code not in CODE_MODE:
        raise ValueError("unknown layout or mode code")
    layout = CODE_LAYOUT[layout_code]
    mode = CODE_MODE[mode_code]
    pos = HEADER.size
    indptr, pos = unpack_block(blob, pos, "q")
    indices, pos = unpack_block(blob, pos, "q")
    data, pos = unpack_block(blob, pos, "d")
    if pos != len(blob):
        raise ValueError("trailing bytes after blocks")
    _validate_structure((nrows, ncols), layout, indptr, indices, data, nnz)
    return SparseMatrix._from_arrays(
        (nrows, ncols),
        layout,
        indptr,
        indices,
        data,
        mode,
        page_size,
        version=matrix_version,
    )


def dump_batch(matrices) -> bytes:
    parts = [BATCH_HEADER.pack(BATCH_MAGIC, len(matrices))]
    for matrix in matrices:
        blob = dumps(matrix)
        parts.append(LENGTH64.pack(len(blob)))
        parts.append(blob)
    return b"".join(parts)


def load_batch(blob: bytes, page_size: int = DEFAULT_PAGE_SIZE):
    if len(blob) < BATCH_HEADER.size:
        raise ValueError("truncated batch header")
    magic, count = BATCH_HEADER.unpack_from(blob, 0)
    if magic != BATCH_MAGIC:
        raise ValueError("bad batch magic")
    pos = BATCH_HEADER.size
    frames = []
    for _ in range(count):
        if pos + LENGTH64.size > len(blob):
            raise ValueError("truncated batch frame header")
        (size,) = LENGTH64.unpack_from(blob, pos)
        pos += LENGTH64.size
        if pos + size > len(blob):
            raise ValueError("truncated batch frame")
        frames.append(blob[pos:pos + size])
        pos += size
    if pos != len(blob):
        raise ValueError("trailing bytes after batch")
    return [loads(frame, page_size=page_size) for frame in frames]
