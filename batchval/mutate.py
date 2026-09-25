"""Random valid-table generator and single-point mutation operators.

Every mutation changes exactly one structural fact so that the expected
error kind is known. Mutations cover corruption *and* missing data, and
include hostile inputs (huge offsets) that must not destabilize checks.
"""
from __future__ import annotations

import copy
from dataclasses import dataclass

from .model import Batch, BufferRef, Column, CType, TableSchema


@dataclass
class Env:
    schema: TableSchema
    batches: list[Batch]
    dictionaries: dict
    buffers: dict


def make_env(rng) -> Env:
    """Build a table that passes every check."""
    dictionaries = {"labels": {1: ["a", "b", "c", "d"]}}
    payload_buf: list = []
    batches: list[Batch] = []
    next_id = rng.randint(0, 1000)

    for _ in range(rng.randint(3, 5)):
        n = rng.randint(2, 8)

        ids = []
        for _ in range(n):               # globally strictly increasing
            next_id += rng.randint(1, 5)
            ids.append(next_id)

        scores = []
        bitmap = bytearray((n + 7) // 8)
        for r in range(n):
            x = rng.random()
            if x < 0.15:
                scores.append(0.0)      # null slot, bitmap bit stays 0
            elif x < 0.3:
                bitmap[r >> 3] |= 1 << (r & 7)
                scores.append(float("nan"))   # valid NaN, distinct from null
            else:
                bitmap[r >> 3] |= 1 << (r & 7)
                scores.append(round(rng.uniform(-100.0, 100.0), 3))

        labels = [rng.randrange(4) for _ in range(n)]
        flags = [bool(rng.getrandbits(1)) for _ in range(n)]

        offset = len(payload_buf)
        payload_buf.extend(f"s{rng.randrange(1000)}" for _ in range(n))

        batches.append(Batch(
            row_count=n,
            columns=[
                Column("id", CType.INT64, data=ids),
                Column("score", CType.FLOAT64, data=scores, validity=bytes(bitmap)),
                Column("label", CType.UTF8, data=labels,
                       dict_id="labels", dict_version=1),
                Column("flag", CType.BOOL, data=flags),
                Column("payload", CType.UTF8,
                       buffer=BufferRef("payload_buf", offset, n)),
            ],
        ))

    schema = TableSchema(
        columns=[("id", CType.INT64), ("score", CType.FLOAT64),
                 ("label", CType.UTF8), ("flag", CType.BOOL),
                 ("payload", CType.UTF8)],
        sorted={"id": "asc"},
    )
    return Env(schema, batches, dictionaries, {"payload_buf": payload_buf})


def _shorten_column(env: Env, rng) -> str:
    batch = rng.choice(env.batches)
    batch.columns[1].data.pop()           # one column one row short
    return "length"


def _corrupt_bitmap(env: Env, rng) -> str:
    batch = rng.choice(env.batches)
    batch.columns[1].validity = b""      # bitmap truncated to zero bytes
    return "bitmap"


def _dict_index_out_of_range(env: Env, rng) -> str:
    batch = rng.choice(env.batches)
    col = batch.columns[2]
    r = rng.randrange(len(col.data))
    col.data[r] = len(env.dictionaries["labels"][1]) + 50
    return "dict_index"


def _break_sort(env: Env, rng) -> str:
    batch = rng.choice(env.batches)
    col = batch.columns[0]
    r = rng.randrange(1, len(col.data))
    col.data[r] = col.data[r - 1] - 1    # guaranteed inversion at row r
    return "order"


def _wrong_column_type(env: Env, rng) -> str:
    rng.choice(env.batches).columns[3].ctype = CType.INT64
    return "schema"


def _missing_dict_version(env: Env, rng) -> str:
    rng.choice(env.batches).columns[2].dict_version = 999
    return "dict_version"


def _huge_buffer_offset(env: Env, rng) -> str:
    rng.choice(env.batches).columns[4].buffer.offset = 10 ** 12
    return "buffer"


MUTATIONS = {
    "shorten_column": _shorten_column,
    "corrupt_bitmap": _corrupt_bitmap,
    "dict_index_out_of_range": _dict_index_out_of_range,
    "break_sort": _break_sort,
    "wrong_column_type": _wrong_column_type,
    "missing_dict_version": _missing_dict_version,
    "huge_buffer_offset": _huge_buffer_offset,
}


def apply_mutation(env: Env, name: str, rng) -> tuple[Env, str]:
    """Deep-copy the environment, apply one named mutation, return env + kind."""
    mutated = copy.deepcopy(env)
    kind = MUTATIONS[name](mutated, rng)
    return mutated, kind
