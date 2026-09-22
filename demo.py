"""Runnable end-to-end demo: pairing, index updates, peak queries.

Run:  python3 demo.py
"""

from occupancy import OccupancyTracker, Result


def show_peak(tr, lo, hi, label):
    r = tr.peak(lo, hi)
    print(f"\n[{label}] peak in [{lo}, {hi}) = {r.peak}")
    for p in r.periods:
        ids = ", ".join(sorted(p.occupancy_ids))
        print(f"    peak period [{p.start}, {p.end})  <-  ids: {{{ids}}}")
    if not r.periods:
        print("    (no active occupancy in range)")
    return r


def main():
    tr = OccupancyTracker()

    print("== 1. Out-of-order pairing: ends arrive before starts ==")
    print("end(A,5)   ->", tr.record_end("A", 5).name)     # orphan
    print("start(A,0) ->", tr.record_start("A", 0).name)  # pairs [0,5)
    print("end(B,9)   ->", tr.record_end("B", 9).name)
    print("start(B,5) ->", tr.record_start("B", 5).name)  # pairs [5,9)
    print("intervals:", tr.intervals())
    print("orphans:  ", tr.orphans())
    show_peak(tr, 0, 10, "A=[0,5), B=[5,9): boundary at 5 does not overlap")

    print("\n== 2. Add C=[4,6): peak rises to 2, then retract C ==")
    print("start(C,4) ->", tr.record_start("C", 4).name)
    print("end(C,6)   ->", tr.record_end("C", 6).name)
    show_peak(tr, 0, 10, "with C=[4,6)")
    print("retract(C) ->", tr.retract("C").name)
    show_peak(tr, 0, 10, "after retracting C: restored")

    print("\n== 3. Corrections move B out of the query range ==")
    print("correct_end(B,25)   ->", tr.correct_end("B", 25).name)   # [5,25)
    print("correct_start(B,20) ->", tr.correct_start("B", 20).name)  # [20,25)
    print("intervals:", tr.intervals())
    r = show_peak(tr, 0, 10, "B moved to [20,25): old contribution fully revoked")
    assert all("B" not in p.occupancy_ids for p in r.periods)
    print("count_at(6) =", tr.count_at(6), "(no residue from B's old [5,9) interval)")

    print("\n== 4. Invalid end (end < start) must not corrupt prior state ==")
    print("start(D,3) ->", tr.record_start("D", 3).name)
    print("end(D,1)   ->", tr.record_end("D", 1).name, " (stored, not indexed)")
    print("orphans:  ", tr.orphans())
    print("correct_end(B,2) ->", tr.correct_end("B", 2).name, " (rejected, B unchanged)")
    print("intervals:", tr.intervals())
    print("correct_end(D,8) ->", tr.correct_end("D", 8).name, " (D recovers as [3,8))")
    print("intervals:", tr.intervals())

    print("\n== 5. Duplicate / conflicting ends have explicit results ==")
    print("end(A,5) again ->", tr.record_end("A", 5).name, " (ignored)")
    print("end(A,7)       ->", tr.record_end("A", 7).name, " (needs correct_end)")
    print("retract(UNKNOWN) ->", tr.retract("UNKNOWN").name)

    print("\n== 6. Orphans never create phantom occupancy ==")
    print("start(E,2) ->", tr.record_start("E", 2).name, " (lone start, no fake end padded)")
    print("orphans:  ", tr.orphans())
    show_peak(tr, 0, 10, "final state")


if __name__ == "__main__":
    main()
