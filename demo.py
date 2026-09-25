"""Run sample: validate a table with several independent structural errors.

Usage: python3 demo.py
"""
from __future__ import annotations

import pickle

from batchval import (Batch, BufferRef, Column, CType, Severity, TableSchema,
                      Validator, earliest_bad_row, validate)


def build_demo_table():
    schema = TableSchema(
        columns=[("id", CType.INT64), ("score", CType.FLOAT64),
                 ("label", CType.UTF8), ("payload", CType.UTF8)],
        sorted={"id": "asc"},
    )
    dictionaries = {"labels": {1: ["red", "green", "blue"]}}
    buffers = {"payload_buf": ["p0", "p1", "p2", "p3", "p4", "p5"]}

    batch0 = Batch(3, [  # clean
        Column("id", CType.INT64, data=[10, 20, 30]),
        Column("score", CType.FLOAT64, data=[1.5, float("nan"), 2.5],
               validity=b"\x07"),
        Column("label", CType.UTF8, data=[0, 1, 2],
               dict_id="labels", dict_version=1),
        Column("payload", CType.UTF8, buffer=BufferRef("payload_buf", 0, 3)),
    ])
    batch1 = Batch(3, [  # cross-batch inversion + short column + bad dict index
        Column("id", CType.INT64, data=[25, 40, 50]),       # 25 < 30 of batch0
        Column("score", CType.FLOAT64, data=[3.5, 4.5],     # one row short
               validity=b"\x03"),
        Column("label", CType.UTF8, data=[2, 99, 1],        # 99 out of range
               dict_id="labels", dict_version=1),
        Column("payload", CType.UTF8, buffer=BufferRef("payload_buf", 3, 3)),
    ])
    batch2 = Batch(2, [  # missing dictionary version + hostile buffer offset
        Column("id", CType.INT64, data=[60, 70]),
        Column("score", CType.FLOAT64, data=[5.5, 6.5], validity=b"\x03"),
        Column("label", CType.UTF8, data=[0, 1],
               dict_id="labels", dict_version=9),           # version unknown
        Column("payload", CType.UTF8,
               buffer=BufferRef("payload_buf", 10 ** 12, 2)),  # corrupt offset
    ])
    return schema, [batch0, batch1, batch2], dictionaries, buffers


def main():
    schema, batches, dictionaries, buffers = build_demo_table()

    errors = validate(schema, batches, dictionaries, buffers)
    print(f"== validation report: {len(errors)} errors ==")
    for e in errors:
        loc = f"batch={e.batch}"
        if e.row is not None:
            loc += f" row={e.row}"
        col = f" col={e.column}" if e.column else ""
        ev = f" evidence={e.evidence}" if e.evidence else ""
        print(f"[{e.severity.value:7s}] {e.kind:12s} {loc}{col}: {e.message}{ev}")
    print(f"earliest bad row: {earliest_bad_row(errors)}")
    corrupt = sum(1 for e in errors if e.severity is Severity.CORRUPT)
    missing = sum(1 for e in errors if e.severity is Severity.MISSING)
    print(f"corrupt: {corrupt}, missing-data: {missing}")

    # arbitrary chunking + pause/resume must report the identical error set
    one_shot = sorted(e.key() for e in errors)
    v = Validator(schema, dictionaries, buffers)
    for batch in batches:
        v.feed(batch)
        blob = pickle.dumps(v.snapshot())            # suspend after every batch
        v = Validator.restore(schema, pickle.loads(blob), dictionaries, buffers)
    resumed = sorted(e.key() for e in v.errors())
    print(f"pause/resume error set identical: {resumed == one_shot}")


if __name__ == "__main__":
    main()
