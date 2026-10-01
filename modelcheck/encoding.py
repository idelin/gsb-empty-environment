"""Deterministic, reversible byte encoding of model states.

Supported state atoms: None, bool, int, str, bytes, tuple, frozenset.
The encoding is canonical (equal states encode to equal bytes) and
self-delimiting, so concatenated items can be parsed back unambiguously.
"""
from __future__ import annotations


def encode_state(state) -> bytes:
    if state is None:
        return b"N"
    if state is True:
        return b"T"
    if state is False:
        return b"F"
    if isinstance(state, int):
        return b"I" + str(state).encode("ascii") + b";"
    if isinstance(state, str):
        data = state.encode("utf-8")
        return b"S" + str(len(data)).encode("ascii") + b":" + data
    if isinstance(state, bytes):
        return b"B" + str(len(state)).encode("ascii") + b":" + state
    if isinstance(state, tuple):
        parts = [encode_state(item) for item in state]
        return b"P" + str(len(parts)).encode("ascii") + b":" + b"".join(parts)
    if isinstance(state, frozenset):
        parts = sorted(encode_state(item) for item in state)
        return b"Z" + str(len(parts)).encode("ascii") + b":" + b"".join(parts)
    raise TypeError(f"cannot encode state of type {type(state)!r}")


def decode_state(data: bytes):
    value, pos = _decode(data, 0)
    if pos != len(data):
        raise ValueError("trailing bytes after encoded state")
    return value


def _decode(data: bytes, pos: int):
    tag = data[pos:pos + 1]
    pos += 1
    if tag == b"N":
        return None, pos
    if tag == b"T":
        return True, pos
    if tag == b"F":
        return False, pos
    if tag == b"I":
        end = data.index(b";", pos)
        return int(data[pos:end]), end + 1
    if tag in (b"S", b"B"):
        colon = data.index(b":", pos)
        length = int(data[pos:colon])
        start = colon + 1
        raw = data[start:start + length]
        if len(raw) != length:
            raise ValueError("truncated encoded state")
        return (raw.decode("utf-8") if tag == b"S" else raw), start + length
    if tag in (b"P", b"Z"):
        colon = data.index(b":", pos)
        count = int(data[pos:colon])
        cursor = colon + 1
        items = []
        for _ in range(count):
            value, cursor = _decode(data, cursor)
            items.append(value)
        return (tuple(items) if tag == b"P" else frozenset(items)), cursor
    raise ValueError(f"unknown tag {tag!r}")


def to_jsonable(state):
    """Convert a state into a JSON-serializable structure (for evidence files)."""
    if state is None or isinstance(state, (bool, int, str)):
        return state
    if isinstance(state, bytes):
        return {"__bytes__": state.hex()}
    if isinstance(state, tuple):
        return [to_jsonable(item) for item in state]
    if isinstance(state, frozenset):
        return {"__frozenset__": sorted((to_jsonable(i) for i in state), key=repr)}
    raise TypeError(f"cannot jsonify state of type {type(state)!r}")
