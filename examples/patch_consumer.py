"""Demo: a downstream replica that consumes patches instead of re-querying.

Scenario (from the spec):
    A@0, A@2, B@5  -> two segments
    insert B@1     -> the A segment splits
    retract B@1    -> original structure restored, A@2 still indexed

Run:  python3.11 examples/patch_consumer.py
"""

from status_rle import StatusHistory, apply_patch


class Replica:
    """Maintains a segment snapshot purely from patches."""

    def __init__(self):
        self.snapshot = []

    def consume(self, patch, label):
        self.snapshot = apply_patch(self.snapshot, patch)
        print(f"\n== {label} ==")
        print("patch:")
        print(patch)
        print("replica snapshot:")
        for seg in self.snapshot:
            print(f"  {seg}")


def main():
    history = StatusHistory()
    replica = Replica()

    replica.consume(history.insert("sensor", 1, 0, "A"), "insert A@0")
    replica.consume(history.insert("sensor", 2, 2, "A"), "insert A@2 (merges)")
    replica.consume(history.insert("sensor", 3, 5, "B"), "insert B@5")
    replica.consume(history.insert("sensor", 4, 1, "B"), "insert B@1 (splits A)")
    replica.consume(history.retract("sensor", 4), "retract B@1 (restores)")

    print("\n== provenance check ==")
    seg = history.segment_at(3)
    print("segment covering t=3:", seg)
    print("source index (refs):", seg.refs())
    assert ("sensor", 2) in seg.refs(), "A@2 must remain in the source index"

    assert replica.snapshot == history.segments()
    print("\nreplica == direct query: OK")


if __name__ == "__main__":
    main()
