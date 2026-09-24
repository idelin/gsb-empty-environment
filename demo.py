"""Runnable sample for batchbuf.  Run: python3.11 demo.py"""

import threading

from batchbuf import (Batch, DType, Ledger, QuotaExceededError,
                      ResourceClosedError, Schema)


def show(tag, ledger):
    live = ledger.live_buffers()
    print(f"[{tag}] used={ledger.used}B peak={ledger.peak}B "
          f"live_buffers={len(live)}")


def main():
    schema = Schema([("id", DType.INT64), ("val", DType.FLOAT64)])
    rows = [{"id": i, "val": None if i == 2 else i + 0.5} for i in range(8)]

    ledger = Ledger(quota=1024)
    batch = Batch.from_rows(schema, rows, ledger)
    show("batch built (8 rows x 2 cols)", ledger)

    # --- zero-copy slicing: sharing counts once -------------------------
    left = batch.slice(0, 4)
    right = batch.slice(4, 4)
    nested = left.slice(1, 2)
    show("3 slices taken (zero-copy)", ledger)
    print("  nested id values:", nested.column("id").to_list())

    # --- copy-on-write ---------------------------------------------------
    left.column("id").set(0, 999)
    show("COW write on left slice", ledger)
    print("  original id[0] =", batch.column("id").get(0),
          "| left id[0] =", left.column("id").get(0))

    # --- parent close keeps slice leases --------------------------------
    batch.close()
    print("  parent closed; right slice still reads:",
          right.column("val").to_list())
    show("parent batch closed", ledger)

    # --- null vs NaN, exact integers -------------------------------------
    print("  right val[0..1]:", right.column("val").to_list()[:2],
          "(None is null, not NaN)")
    big = Batch.from_rows(Schema([("n", DType.INT64)]),
                          [{"n": 2**62}, {"n": 2**62}], ledger)
    print("  exact int sum 2^62 + 2^62 =", big.sum("n"))
    big.close()

    # --- quota rejection happens before any state change -----------------
    ledger.set_quota(ledger.used + 1)
    try:
        Batch.from_rows(schema, rows, ledger)
    except QuotaExceededError as exc:
        print("  quota rejected cleanly:", exc)
    show("after rejected build", ledger)
    ledger.set_quota(1024)  # restore headroom for the rest of the demo

    # --- threads borrowing while refs drop --------------------------------
    victim = Batch.from_rows(schema, rows[:2], ledger)
    buf = victim.column("id")._buffer
    entered, proceed = threading.Event(), threading.Event()

    def borrower():
        with buf.borrowed():
            entered.set()
            proceed.wait(5)

    t = threading.Thread(target=borrower)
    t.start()
    entered.wait(5)
    victim.close()  # last reference dropped while borrowed
    print(f"  borrowed during close: reclaimed={buf.reclaimed} (must be False)")
    proceed.set()
    t.join(5)
    print(f"  after borrower left:   reclaimed={buf.reclaimed} (must be True)")

    # --- close everything; repeated close is safe -------------------------
    for b in (left, right, nested):
        for _ in range(3):
            b.close()
    show("all views closed", ledger)
    try:
        left.column("id")
    except ResourceClosedError as exc:
        print("  access after close denied:", exc)


if __name__ == "__main__":
    main()
