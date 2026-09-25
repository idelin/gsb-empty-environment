"""Sample run: merge dictionary-encoded batches, then verify semantics.

Run with:  python3.11 demo.py
"""

import os
import shutil
import tempfile

from dictmerge import (
    FLOAT,
    INT,
    STR,
    Batch,
    CancelController,
    LocalDictionary,
    MergeCancelled,
    MergeConfig,
    Merger,
    Store,
    decode_rows,
)


def main():
    workdir = tempfile.mkdtemp(prefix="dictmerge-demo-")
    store = Store(os.path.join(workdir, "store"))
    schema = [("word", STR), ("count", INT), ("ratio", FLOAT)]

    # Two batches whose local dictionaries disagree: code 0 is 'apple'
    # in the first and 'banana' in the second.
    d1 = LocalDictionary()
    d1.add("apple")
    d1.add("banana")
    d2 = LocalDictionary()
    d2.add("banana")
    d2.add("apple")
    d2.add("cherry")
    d2.add("")

    b1 = Batch.from_rows(
        schema,
        [("apple", 2**70 + 1, 0.5), ("banana", 7, None), (None, 3, float("nan"))],
        {"word": d1},
    )
    b2 = Batch.from_rows(
        schema,
        [("banana", 11, 1.25), ("cherry", 12, None), ("apple", 13, 2.5), ("", 0, 0.0)],
        {"word": d2},
    )
    batches = [b1, b2]

    print("== local dictionaries ==")
    print("batch 1: code 0 ->", d1.decode(0), "| code 1 ->", d1.decode(1))
    print("batch 2: code 0 ->", d2.decode(0), "| code 1 ->", d2.decode(1))

    # --- cancelled first attempt, then resume ---------------------------
    controller = CancelController()
    calls = {"n": 0}

    def cancel_soon(phase, index):
        if phase == "encode":
            calls["n"] += 1
            if calls["n"] == 1:
                controller.cancel()

    cfg = MergeConfig(
        memory_budget_bytes=256,  # tiny budget: forces spill files
        controller=controller,
        progress_callback=cancel_soon,
    )
    try:
        Merger(store, cfg).merge(batches, job_id="demo")
    except MergeCancelled:
        print("\n== merge cancelled after first encoded batch; resuming ==")

    result = Merger(store, MergeConfig(memory_budget_bytes=256)).merge(
        batches, job_id="demo"
    )

    print("\n== merged dictionary (deterministic order) ==")
    payload = store.load_dict(result.version)
    for col, spec in payload["columns"].items():
        print(f"column {col!r}: {spec['terms']}")

    print("\n== byte accounting ==")
    for key, val in result.accounting.items():
        print(f"{key:>14}: {val} bytes")

    print("\n== merged + decoded rows ==")
    merged = list(store.iter_rows(result.version))
    for row in merged:
        print(row)

    reference = decode_rows(b1) + decode_rows(b2)

    def eq(a, b):
        import math

        for ra, rb in zip(a, b):
            for x, y in zip(ra, rb):
                if isinstance(x, float) and isinstance(y, float):
                    if math.isnan(x) and math.isnan(y):
                        continue
                if x != y:
                    return False
        return len(a) == len(b)

    print("\nmerge-then-decode == decode-then-concat:", eq(merged, reference))
    shutil.rmtree(workdir, ignore_errors=True)


if __name__ == "__main__":
    main()
