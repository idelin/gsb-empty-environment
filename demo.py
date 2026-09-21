"""Demo: how correction events let a consumer maintain the current alignment.

Scenario from the requirements: main sensor samples at t=10, aux sensor has
t=8 and t=12, tolerance=2 -> pick 8; a late sample at t=9 replaces it with 9.
"""
from aligner import (
    MAIN,
    Aligner,
    AlignmentTable,
    Anchor,
    Sample,
    RetractAux,
    RetractMain,
    UpsertAux,
    UpsertMain,
    Watermark,
)


def show(table):
    if not table.rows:
        print("    table: <empty>")
        return
    for aid, row in sorted(table.rows.items()):
        cells = ", ".join(
            f"{m.source}={m.sample_id or 'MISSING'}" for m in row.matches
        )
        flag = " [FINAL]" if aid in table.finalized else ""
        print(f"    table: {aid}@t={row.anchor_time:g} v{row.version} -> {cells}{flag}")


def main():
    aligner = Aligner(aux_sources=["aux"], tolerance=2)
    table = AlignmentTable()          # the downstream consumer
    event_log = []                    # what a broker/queue would persist

    def step(label, events):
        print(f"-> {label}")
        out = aligner.apply_all(events)
        event_log.extend(out)         # consumer sees ONLY these output events
        table.apply_all(out)
        for ev in out:
            print(f"    emit: {ev}")
        show(table)

    step("anchor A arrives at t=10 (no aux data yet)",
         [UpsertMain(Anchor("A", time=10, seq=1))])

    step("aux samples x8@t=8 and x12@t=12 arrive (tie: distance 2 both)",
         [UpsertAux(Sample("aux", "x8", time=8, seq=1)),
          UpsertAux(Sample("aux", "x12", time=12, seq=2))])

    step("LATE better sample x9@t=9 arrives -> replacement (v3)",
         [UpsertAux(Sample("aux", "x9", time=9, seq=3))])

    step("x9 is retracted -> fall back to next-best x8 (v4)",
         [RetractAux("aux", "x9")])

    step("second anchor B@t=9 reuses the same aux sample x8",
         [UpsertMain(Anchor("B", time=9, seq=2))])

    step("watermarks close both anchors (aux>=13, main>=11)",
         [Watermark("aux", 13), Watermark(MAIN, 11)])

    step("late perfect sample x10@t=10: ignored, rows are final",
         [UpsertAux(Sample("aux", "x10", time=10, seq=4))])

    step("retracting anchor B after finalization: ignored",
         [RetractMain("B")])

    print("\n-- a fresh consumer replaying only the output event log --")
    replayed = AlignmentTable()
    replayed.apply_all(event_log)
    assert replayed.as_dict() == table.as_dict()
    print(f"    replayed {len(event_log)} output events -> identical table")
    show(replayed)


if __name__ == "__main__":
    main()
