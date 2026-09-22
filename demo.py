"""Demo: correction events let a consumer recover the current alignment table.

Scenario from the spec: main sensor samples at t=10; aux has data at t=8 and
t=12; tolerance=2 -> pick 8. When t=9 arrives late, the pairing switches to 9.
Then the aux sample at t=9 is retracted and the engine falls back.
"""

from stream_align import Aligner, align_batch, replay


def show(tag, events, table, finalized):
    for ev in events:
        print(f"  event: {ev!r}")
    rows = ", ".join(
        f"{aid}->v{r.version}{r.selections}" for aid, r in sorted(table.items())
    )
    print(f"  {tag}: table={{{rows}}} finalized={sorted(finalized)}\n")


def main():
    al = Aligner(tolerance=2, sources=["aux"])
    log = []
    table, finalized = set(), None

    def apply(events, tag):
        nonlocal table, finalized
        log.extend(events)
        table, finalized = replay(log)  # consumer folds the full event log
        show(tag, events, table, finalized)

    print("== stream of corrections ==")
    apply(al.add_anchor("m@10", time=10), "anchor arrives (no aux yet)")
    apply(al.add_sample("aux", "aux@8", time=8), "aux@8 arrives")
    apply(al.add_sample("aux", "aux@12", time=12), "aux@12 arrives (tie, 8 stays)")
    apply(al.add_sample("aux", "aux@9", time=9), "late aux@9 replaces 8")
    apply(al.retract_sample("aux", "aux@9"), "aux@9 retracted, fall back to 8")
    apply(al.advance_watermark("aux", 12), "watermark finalizes m@10")
    apply(al.add_sample("aux", "aux@10", time=10), "late aux@10 cannot rewrite")

    print("== independent full-scan reference agrees ==")
    # Reference sees the data that survived up to finalization:
    # aux@9 was retracted; aux@10 arrived after the watermark and is ignored.
    ref = align_batch(
        [("m@10", 10, 0)],
        {"aux": [("aux@8", 8, 0), ("aux@12", 12, 0)]},
        tolerance=2,
    )
    print(f"  reference: {ref['m@10'].selections}")
    print(f"  consumer : {table['m@10'].selections}")
    assert table["m@10"].selections == ref["m@10"].selections
    print("  match: OK")


if __name__ == "__main__":
    main()
