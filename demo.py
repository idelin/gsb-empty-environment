"""End-to-end walkthrough of the correctable coverage ledger.

Run with:  PYTHONPATH=. python3 demo.py
"""

from coverage_ledger import (
    Ledger, query, reconcile, revision_impact,
    save_checkpoint, load_checkpoint,
)


def show(title, result):
    print(f"  {title}: known={result.known_coverage_length} "
          f"window={result.window_length} rate={result.coverage_rate} "
          f"evidence={list(result.evidence_ids())}")


def main():
    led = Ledger()

    # Rule version 1: A and B are members from t=0, indefinitely.
    led.add_membership("A", 0, None, rule_id="mem-A-1", summary="A joins")
    led.add_membership("B", 0, None, rule_id="mem-B-1", summary="B joins")
    led.add_observation("A", 0, 6, observation_id="obs-A")
    led.add_observation("B", 4, 8, observation_id="obs-B")

    print("== initial reports over [0,10) ==")
    show("at least one (union)      ", query(led.snapshot(), (0, 10), "union"))
    show("at least two               ", query(led.snapshot(), (0, 10), "at_least_k", k=2))
    show("all members (intersection) ", query(led.snapshot(), (0, 10), "intersection"))

    # Publish answers; they get pinned to the current revision.
    led.publish_report("R-union", (0, 10), "union")
    led.publish_report("R-k2", (0, 10), "at_least_k", k=2)

    # B leaves the group at t=5 (new membership rule version).
    leave = led.end_membership("B", 5, rule_id="mem-B-2", summary="B leaves at 5")

    # Withdraw B's old interval and enter the corrected version.
    retract = led.retract_observation("obs-B", "B", summary="withdraw old B report")
    corrected = led.add_observation("B", 3, 8, observation_id="obs-B-v2",
                                   summary="corrected B report [3,8)")

    print("\n== after B leaves and B's interval is corrected ==")
    show("at least one (current)", query(led.snapshot(), (0, 10), "union"))
    show("at least two (current) ", query(led.snapshot(), (0, 10), "at_least_k", k=2))

    print("\n== reversible deltas vs the published answers ==")
    for report_id in ("R-union", "R-k2"):
        _, _, delta = reconcile(led, report_id)
        print(f"  {report_id}: {delta.old_length} -> {delta.new_length} "
              f"(difference {delta.difference:+d}) gained={list(delta.gained_segments)} "
              f"lost={list(delta.lost_segments)} conserving={delta.is_conserving}")
        inverse = delta.inverse()
        print(f"    inverse (current -> published): {inverse.old_length} -> {inverse.new_length} "
              f"conserving={inverse.is_conserving}")

    print("\n== old observation snapshot keeps old records and old rules ==")
    published_view = led.report_snapshot("R-union")
    show("published union (pinned)", query(published_view, (0, 10), "union"))

    print("\n== actual revised range per revision (within [0,10)) ==")
    for seq in (leave.seq, retract.seq, corrected.seq):
        impact = revision_impact(led, seq, (0, 10))
        print(f"  r{seq} [{impact['kind']}/{impact['action']}] {impact['summary']}")
        if impact["kind"] == "membership":
            print(f"      devices={impact['devices']} "
                  f"old_rule_removed={impact['removed_ranges']} "
                  f"new_rule_added={impact['added_ranges']}")
        else:
            print(f"      devices={impact['devices']} raw={impact['raw_changed_ranges']} "
                  f"effective(while member)={impact['effective_changed_ranges']}")

    print("\n== checkpoint round trip ==")
    save_checkpoint(led, "/tmp/coverage_ledger_demo.json")
    restored = load_checkpoint("/tmp/coverage_ledger_demo.json")
    show("restored current union", query(restored.snapshot(), (0, 10), "union"))
    print(f"  head restored: {restored.head} == {led.head}")


if __name__ == "__main__":
    main()
