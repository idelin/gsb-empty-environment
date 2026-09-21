"""Consumer example: collects the revocable match stream into tables.

Output contract (see Collector):

    on_match_proposed(match, late)   tentative result (+)
    on_match_revoked(match, cause)   cancellation of a tentative result (-)
    on_match_final(match)            watermark-sealed, immutable result (#)
    on_side_report(report)           duplicates / out-of-scope late /
                                     late-but-incorporated / failed withdraws

Run:  python3 -m streamcep.consumer
"""

from __future__ import annotations

from collections import Counter
from typing import Dict, List

from .matcher import (
    Collector, Event, Match, MatchId, SideReport, StreamMatcher,
    Watermark, Withdraw,
)


def _tag(m: Match) -> str:
    return "%s[A@%d(%s,%d) -> B@%d(%s,%d)]" % (
        m.key, m.a.time, m.a.source, m.a.seq,
        m.b.time, m.b.source, m.b.seq)


class PrintingCollector(Collector):
    """Prints every notification and keeps live/final tables."""

    def __init__(self, verbose: bool = True) -> None:
        self.verbose = verbose
        self.pending: Dict[MatchId, Match] = {}
        self.final: List[Match] = []
        self.reports: List[SideReport] = []
        self.counts = Counter()

    def _say(self, text: str) -> None:
        if self.verbose:
            print(text)

    def on_match_proposed(self, match: Match, late: bool = False) -> None:
        self.counts["proposed"] += 1
        self.pending[match.mid] = match
        self._say("  + PROPOSED%s %s" %
                  (" (late revision)" if late else "", _tag(match)))

    def on_match_revoked(self, match: Match, cause: str,
                         late: bool = False, blocker=None) -> None:
        self.counts["revoked"] += 1
        self.pending.pop(match.mid, None)
        extra = ""
        if blocker is not None:
            extra = " by C@%d(%s,%d)" % (
                blocker.time, blocker.source, blocker.seq)
        self._say("  - REVOKED  %s cause=%s%s%s" %
                  (_tag(match), cause, extra,
                   " (late revision)" if late else ""))

    def on_match_final(self, match: Match) -> None:
        self.counts["final"] += 1
        self.pending.pop(match.mid, None)
        self.final.append(match)
        self._say("  # FINAL    %s" % _tag(match))

    def on_side_report(self, report: SideReport) -> None:
        self.counts[report.kind] += 1
        self.reports.append(report)
        self._say("  ~ %-24s %s" % (report.kind, report.detail))


def _run(title: str, records, expect=None):
    print("\n=== %s ===" % title)
    sink = PrintingCollector()
    m = StreamMatcher(sink)
    for rec in records:
        if isinstance(rec, Watermark):
            print("-- watermark -> %d ; state: %s" % (rec.time, m.stats()))
        elif isinstance(rec, Withdraw):
            print(">> WITHDRAW (%s,%d)" % (rec.source, rec.seq))
        else:
            late = m.watermark is not None and rec.time < m.watermark
            print(">> %s@%d key=%s (%s,%d)%s" %
                  (rec.etype, rec.time, rec.key, rec.source, rec.seq,
                   "  [LATE]" if late else ""))
        m.push(rec)
    print("FINAL results: %s" %
          sorted(_tag(x) for x in sink.final))
    print("counts: %s" % dict(sink.counts))
    if expect is not None:
        got = {(x.key, x.a.time, x.b.time) for x in sink.final}
        assert got == expect, "expected %s got %s" % (expect, got)
        print("assert OK: %s" % expect)
    return sink


def main() -> None:
    # 1) Headline story: A@0 A@1 B@3 -> two overlapping tentative matches;
    #    C@2 then arrives (still within allowed lateness) and revokes BOTH.
    #    After the watermark closes, zero final matches.
    _run("headline: A@0 A@1 B@3 then C@2 (open) -> both revoked", [
        Event("A", 0, "k", "s1", 1),
        Event("A", 1, "k", "s1", 2),
        Event("B", 3, "k", "s1", 3),
        Watermark(2),              # nothing sealed yet (B@3 > 2)
        Event("C", 2, "k", "s1", 9),   # late vs W=2? 2>=2 on time; arrives 4th
        Watermark(10),
    ], expect=set())

    # 2) Same story but C@2 arrives only AFTER B@3 has been sealed: finals
    #    stay unchanged and the C is reported separately.
    _run("late C after closure: finals immutable, C reported separately", [
        Event("A", 0, "k", "s1", 1),
        Event("A", 1, "k", "s1", 2),
        Event("B", 3, "k", "s1", 3),
        Watermark(4),              # seals B@3 -> 2 finals
        Event("C", 2, "k", "s1", 9),   # 2+5 >= 4 structurally in scope,
                                       # but no open match left to revise
        Watermark(10),
    ], expect={("k", 0, 3), ("k", 1, 3)})

    # 3) Even later C (out of scope) is dropped with a side report.
    _run("out-of-scope late C: side report only", [
        Event("A", 0, "k", "s1", 1),
        Event("B", 3, "k", "s1", 2),
        Watermark(10),
        Event("C", 1, "k", "s1", 3),   # 1+5 < 10 -> out of scope
        Watermark(20),
    ], expect={("k", 0, 3)})

    # 4) Window endpoint: B@5 matches A@0 (d=5 inclusive); B@6 misses A@0,
    #    matches A@1 only.
    _run("window endpoint: A@0 A@1, B@5, B@6", [
        Event("A", 0, "k", "s", 1),
        Event("A", 1, "k", "s", 2),
        Event("B", 5, "k", "s", 3),
        Event("B", 6, "k", "s", 4),
        Watermark(10),
    ], expect={("k", 0, 5), ("k", 1, 5), ("k", 1, 6)})

    # 5) Same-timestamp tie-break A < C < B; "between" is strict.
    _run("same time: A,C,B@7 blocked; A,B@8 matched", [
        Event("A", 7, "k", "s", 1),
        Event("C", 7, "k", "s", 2),
        Event("B", 7, "k", "s", 3),
        Event("A", 8, "k", "s", 4),
        Event("B", 8, "k", "s", 5),
        Watermark(20),
    ], expect={("k", 8, 8)})

    # 6) Duplicates by (source,seq) are counted once.
    _run("dedup: repeated (s,1) ignored", [
        Event("A", 0, "k", "s", 1),
        Event("A", 0, "k", "s", 1),
        Event("B", 3, "k", "s", 2),
        Watermark(10),
    ], expect={("k", 0, 3)})

    # 7) Withdraw an A before closure: its pending matches are revoked.
    _run("withdraw A@1 before closure", [
        Event("A", 0, "k", "s", 1),
        Event("A", 1, "k", "s", 2),
        Event("B", 3, "k", "s", 3),
        Withdraw("s", 2),
        Watermark(10),
    ], expect={("k", 0, 3)})

    # 8) Withdraw after finalization: side report only, finals unchanged.
    _run("withdraw after finalization: side report only", [
        Event("A", 0, "k", "s", 1),
        Event("B", 3, "k", "s", 2),
        Watermark(10),
        Withdraw("s", 1),
        Watermark(20),
    ], expect={("k", 0, 3)})

    # 9) Late B is never incorporated (it would change sealed results).
    _run("late B: reported separately, never matched retroactively", [
        Event("A", 0, "k", "s", 1),
        Watermark(4),
        Event("B", 3, "k", "s", 2),
        Watermark(10),
    ], expect=set())

    # 10) Long quiet gap: pending/unmatched state is bounded by watermark.
    print("\n=== bounded pending state across a long gap (no B) ===")
    sink = PrintingCollector()
    m = StreamMatcher(sink)
    m.push(Event("A", 0, "k", "s", 1))
    m.push(Event("A", 1, "k", "s", 2))
    print("after As:        %s" % m.stats())
    for w in (2, 4, 6, 100):
        m.advance(w)
        print("watermark=%3d -> %s" % (w, m.stats()))


if __name__ == "__main__":
    main()
