"""Independent full-scan reference for cross-checking the Aligner.

Deliberately naive: plain dicts, linear scans, no sorted indexes.  Given the
same event sequence it must yield the same final table as an AlignmentTable
fed by Aligner output events.
"""
from __future__ import annotations

from aligner import (
    MAIN,
    NEG_INF,
    RetractAux,
    RetractMain,
    UpsertAux,
    UpsertMain,
    Watermark,
)


def reference_table(events, aux_sources, tolerance):
    """Replay `events` and return {anchor_id: (anchor_time, {source: sid|None})}."""
    anchors = {}                       # live anchors: id -> Anchor
    aux = {s: {} for s in aux_sources}  # source -> {sample_id: Sample}
    wm = {MAIN: NEG_INF, **{s: NEG_INF for s in aux_sources}}
    table = {}                         # anchor_id -> (time, {source: sid|None})

    def scan(t):
        row = {}
        for s in aux_sources:
            best = None
            for smp in aux[s].values():
                d = abs(smp.time - t)
                if d > tolerance:
                    continue
                key = (d, smp.time, smp.seq)
                if best is None or key < best[0]:
                    best = (key, smp)
            row[s] = best[1].sample_id if best else None
        return row

    def finalize_due():
        for aid, a in list(anchors.items()):
            if a.time <= wm[MAIN] and all(
                a.time + tolerance <= wm[s] for s in aux_sources
            ):
                table[aid] = (a.time, scan(a.time))  # freeze forever
                del anchors[aid]

    for ev in events:
        if isinstance(ev, UpsertMain):
            if ev.anchor.anchor_id not in table:
                anchors[ev.anchor.anchor_id] = ev.anchor
        elif isinstance(ev, RetractMain):
            anchors.pop(ev.anchor_id, None)  # frozen rows are not in `anchors`
        elif isinstance(ev, UpsertAux):
            aux[ev.sample.source][ev.sample.sample_id] = ev.sample
        elif isinstance(ev, RetractAux):
            aux[ev.source].pop(ev.sample_id, None)
        elif isinstance(ev, Watermark):
            wm[ev.source] = max(wm[ev.source], ev.time)
            finalize_due()
        else:
            raise TypeError(f"unknown event: {ev!r}")

    for aid, a in anchors.items():  # everything still open freezes at the end
        table[aid] = (a.time, scan(a.time))
    return table
