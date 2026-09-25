"""Run sample: damaged table, missing-vs-corrupt, chunk invariance, resume."""
import copy

from batchval import Severity, StreamValidator
from batchval.mutate import base_batches, base_registry, base_schema, mutations


def print_report(title, errors):
    print(f"--- {title}: {len(errors)} error(s) ---")
    for e in errors:
        where = f"batch={e.batch}"
        if e.row is not None:
            where += f" global_row={e.row}"
        if e.column:
            where += f" column={e.column}"
        print(f"  [{e.severity.value:7s}] {e.kind:30s} {where}")
        print(f"           {e.message}")
        if e.evidence is not None and e.occurrences == 1:
            print(f"           evidence={e.evidence!r}")
        if e.occurrences > 1:
            print(f"           occurrences={e.occurrences} (earliest row shown)")
    print()


def main():
    schema = base_schema()

    # 1) a damaged table: four independent defects at once
    batches = copy.deepcopy(base_batches())
    next(c for c in batches[1].columns if c.spec.name == "id").values[0] = 0
    next(c for c in batches[1].columns if c.spec.name == "city").values[2] = 99
    city2 = next(c for c in batches[2].columns if c.spec.name == "city")
    city2.values = city2.values[:-1]
    next(c for c in batches[0].columns if c.spec.name == "qty").values[2] = 7.5
    sv = StreamValidator(schema, base_registry())
    all_errors = []
    for b in batches:
        all_errors.extend(sv.feed_batch(b))
    print_report("damaged table, streamed batch by batch (no full materialization)",
                 all_errors)

    # 2) missing data vs definite corruption
    for name, kind, severity, mutant in mutations():
        if kind in ("BUFFER_MISSING", "DICT_VERSION_UNKNOWN",
                    "DICT_INDEX_OUT_OF_RANGE"):
            es = []
            s = StreamValidator(schema, base_registry())
            for b in mutant:
                es.extend(s.feed_batch(b))
            e = next(x for x in es if x.kind == kind)
            print(f"--- {name}: {e.kind} -> severity={e.severity.value}")
    print()

    # 3) arbitrary chunking must report the same set
    from tests.test_validator import make_batches
    from batchval import pack_bitmap
    full = pack_bitmap([1] * 12)
    columns = [
        ([0, 1, 2, 3, 4, 0, 6, 7, 8, 9, 10, 11], None),
        ([float(i) for i in range(12)], full),
        ([0, 0, 1, 1, 1, 2, 99, 2, 2, 3, 3, 3], full),
        ([12 - i for i in range(12)], full),
    ]
    sets = {}
    for sizes in ([12], [6, 6], [4, 4, 4], [1] * 12):
        s = StreamValidator(schema, base_registry())
        es = []
        for b in make_batches(schema, columns, sizes):
            es.extend(s.feed_batch(b))
        sets[tuple(sizes)] = {(e.kind, e.column, e.row) for e in es}
    print("--- chunk invariance (same 12 rows, different chunking) ---")
    ref = next(iter(sets.values()))
    for sizes, ks in sets.items():
        print(f"  chunks={list(sizes)}: {len(ks)} errors, identical={ks == ref}")
    print(f"  error set={sorted(ref)}")
    print()

    # 4) pause/resume after the first batch
    batches3 = make_batches(schema, columns, [4, 4, 4])
    sv = StreamValidator(schema, base_registry())
    before = sv.feed_batch(batches3[0])
    state = sv.snapshot()
    sv2 = StreamValidator(schema, base_registry(), state=state)
    resumed = before + sv2.feed_batch(batches3[1]) + sv2.feed_batch(batches3[2])
    sv_ref = StreamValidator(schema, base_registry())
    uninterrupted = sv_ref.feed_batch(batches3[0]) + \
        sv_ref.feed_batch(batches3[1]) + sv_ref.feed_batch(batches3[2])
    print("--- pause/resume after batch 1 ---")
    print(f"  identical error set = "
          f"{ {e.key() for e in resumed} == {e.key() for e in uninterrupted} }")


if __name__ == "__main__":
    main()
