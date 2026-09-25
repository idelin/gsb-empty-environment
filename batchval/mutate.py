"""Single-point mutation generator.

Builds a known-valid table (schema + registry + 3 batches x 4 rows) and
yields mutants, each carrying exactly one defect (plus one control
mutant that stays valid). Used to prove the validator catches every
defect class -- not just normal batches.
"""
from __future__ import annotations

import copy
import math

from .model import (
    Batch,
    ColType,
    ColumnData,
    ColumnSpec,
    DictRegistry,
    Dictionary,
    K,
    Schema,
    Severity,
    pack_bitmap,
)

CITY_V0 = ["bj", "gz", "sh", "sz"]
CITY_V1 = ["bj", "gz", "sh", "sz", "hz"]


def base_schema() -> Schema:
    return Schema(columns=(
        ColumnSpec("id", ColType.INT, nullable=False, sorted_asc=True),
        ColumnSpec("score", ColType.FLOAT, nullable=True, sorted_asc=True),
        ColumnSpec("city", ColType.STR, nullable=True, sorted_asc=True),
        ColumnSpec("qty", ColType.INT, nullable=True, sorted_asc=False),
    ))


def base_registry() -> DictRegistry:
    reg = DictRegistry()
    reg.register(Dictionary("city", 0, list(CITY_V0)))
    reg.register(Dictionary("city", 1, list(CITY_V1)))
    return reg


def _col(schema, name, length, values, bitmap=None, dict_ref=None):
    spec = next(c for c in schema.columns if c.name == name)
    return ColumnData(spec=spec, length=length, values=values,
                      bitmap=bitmap, dict_ref=dict_ref)


def base_batches() -> list[Batch]:
    """A fully valid 3-batch table. qty is deliberately unsorted to prove
    undeclared columns are never treated as ordered."""
    s = base_schema()
    all_valid = pack_bitmap([1, 1, 1, 1])
    one_null = pack_bitmap([1, 0, 1, 1])  # row 1 null
    dref = ("city", 1)
    return [
        Batch(0, 4, [
            _col(s, "id", 4, [0, 1, 2, 3]),
            _col(s, "score", 4, [0.5, 1.5, 2.5, 3.5], all_valid),
            _col(s, "city", 4, [0, 0, 1, 1], all_valid, dref),
            _col(s, "qty", 4, [9, 1, 7, 2], all_valid),
        ]),
        Batch(1, 4, [
            _col(s, "id", 4, [4, 5, 6, 7]),
            _col(s, "score", 4, [4.5, None, 6.5, 7.5], one_null),
            _col(s, "city", 4, [1, None, 2, 2], one_null, dref),
            _col(s, "qty", 4, [8, 3, 6, 4], all_valid),
        ]),
        Batch(2, 4, [
            _col(s, "id", 4, [8, 9, 10, 11]),
            _col(s, "score", 4, [8.5, 9.5, 10.5, 11.5], all_valid),
            _col(s, "city", 4, [2, 3, 3, 3], all_valid, dref),
            _col(s, "qty", 4, [5, 0, 11, 10], all_valid),
        ]),
    ]


def _find(batch, name):
    return next(c for c in batch.columns if c.spec.name == name)


def mutations():
    """Yield (name, expected_kind, expected_severity, mutated_batches).

    expected_kind None means the mutant must stay valid (control).
    """
    def m(fn):
        batches = copy.deepcopy(base_batches())
        fn(batches)
        return batches

    yield ("length_short", K.LENGTH, Severity.CORRUPT, m(
        lambda b: _find(b[1], "id").__setattr__("values", [4, 5, 6])))

    yield ("bitmap_short", K.BITMAP, Severity.CORRUPT, m(
        lambda b: _find(b[1], "score").__setattr__("bitmap", b"")))

    def oob(b):
        _find(b[1], "city").values[2] = 99
    yield ("dict_index_oob", K.DICT_INDEX, Severity.CORRUPT, m(oob))

    def neg(b):
        _find(b[1], "city").values[0] = -1
    yield ("dict_index_negative", K.DICT_INDEX, Severity.CORRUPT, m(neg))

    def invert(b):
        _find(b[1], "id").values[0] = 0  # 0 < previous batch's last key 3
    yield ("cross_batch_inversion", K.SORT, Severity.CORRUPT, m(invert))

    def stale(b):
        _find(b[2], "city").dict_ref = ("city", 0)
    yield ("stale_dict_version", K.DICT_STALE, Severity.CORRUPT, m(stale))

    def unknown(b):
        _find(b[1], "city").dict_ref = ("city", 99)
    yield ("unknown_dict_version", K.DICT_REF, Severity.MISSING, m(unknown))

    def nan_sorted(b):
        _find(b[2], "score").values[3] = math.nan
    yield ("nan_in_sorted_float", K.SORT, Severity.CORRUPT, m(nan_sorted))

    def null_inconsistency(b):
        _find(b[0], "score").values[0] = None  # valid bit still set
    yield ("null_inconsistency", K.NULL, Severity.CORRUPT, m(null_inconsistency))

    def undeclared_shared(b):
        _find(b[0], "qty").values = _find(b[0], "id").values
    yield ("undeclared_shared_buffer", K.SHARED, Severity.CORRUPT, m(undeclared_shared))

    def missing_buffer(b):
        _find(b[1], "city").values = None
    yield ("missing_buffer", K.BUFFER, Severity.MISSING, m(missing_buffer))

    def int_confusion(b):
        _find(b[0], "qty").values[1] = 1.5
    yield ("int_type_confusion", K.TYPE, Severity.CORRUPT, m(int_confusion))

    # control: reorder the undeclared qty column -> must stay valid
    def unsorted_control(b):
        _find(b[0], "qty").values = [100, 1, 50, 2]
    yield ("unsorted_control", None, None, m(unsorted_control))
