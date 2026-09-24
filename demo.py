"""End-to-end demo: shared buffers, slicing, COW, borrows, ledger audit."""
import threading

from slicedbuf import (
    FLOAT64,
    INT64,
    Batch,
    Ledger,
    QuotaExceeded,
    Schema,
    assert_consistent,
)


def show(title, snap):
    print(f"{title:<58} live={snap.live_bytes:>4}B peak={snap.peak_bytes:>4}B "
          f"buffers={len(snap.live)} freed={snap.freed_count}")


def main():
    ledger = Ledger(quota_bytes=4096)
    schema = Schema([("id", INT64), ("score", FLOAT64)])

    print("== build batch (2 typed columns, null vs NaN) ==")
    batch = Batch.from_columns(
        ledger, schema, [[1, 2, 3, 4], [9.5, None, float("nan"), 7.25]]
    )
    show("after build", ledger.snapshot())

    print("\n== zero-copy slices share physical buffers (counted once) ==")
    left = batch.slice(0, 2)
    right = batch.slice(2, 2)
    nested = right.slice(0, 1)
    show("after 3 slices", ledger.snapshot())
    print("nested slice reads:", nested.column("id").to_list(),
          nested.column("score").to_list())

    print("\n== releasing the parent batch keeps slices valid ==")
    batch.release()
    batch.release()
    show("after parent close (x2, idempotent)", ledger.snapshot())
    print("left still reads:", left.column("id").to_list())

    print("\n== copy-on-write: temp copy is accounted, parent unaffected ==")
    right.column("id").set(0, 99)
    show("after COW set on right", ledger.snapshot())
    print("right id:", right.column("id").to_list(),
          "| left id:", left.column("id").to_list())

    print("\n== quota: insufficient quota rejected before any state change ==")
    tight = Ledger(quota_bytes=36)
    donor = Batch.from_columns(tight, Schema([("a", INT64)]), [[1, 2, 3, 4]])
    view = donor.slice(0, 4)
    try:
        view.column("a").set(0, 5)
    except QuotaExceeded as exc:
        print("rejected:", exc)
    print("state unchanged:", view.column("a").to_list(),
          "live:", tight.snapshot().live_bytes, "B")
    donor.release()
    view.release()

    print("\n== threads borrow buffers; no reclaim during borrow ==")
    results = []
    barrier = threading.Barrier(5)

    def worker():
        barrier.wait()
        with left.borrow():
            results.append(sum(left.column("id").to_list()))

    threads = [threading.Thread(target=worker) for _ in range(4)]
    for thread in threads:
        thread.start()
    barrier.wait()
    for thread in threads:
        thread.join()
    print("4 borrowers each summed id:", results)

    print("\n== independent traversal re-audits the ledger ==")
    snap = assert_consistent(ledger, [batch, left, right, nested])
    show("audit ok", snap)

    print("\n== release everything; ledger drains to zero ==")
    left.release()
    right.release()
    nested.release()
    snap = assert_consistent(ledger, [batch, left, right, nested])
    show("final", snap)
    ledger.close()
    ledger.close()
    print("ledger closed twice safely")
    print("\nALL CHECKS PASSED")


if __name__ == "__main__":
    main()
