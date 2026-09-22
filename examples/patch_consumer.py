"""Patch consumer example: maintain a segment view without the raw events.

Run: python3 examples/patch_consumer.py
"""

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from rle_history import StateHistory, apply_patch


class SegmentView:
    """Downstream replica fed only by the initial snapshot plus patches."""

    def __init__(self, initial):
        self._segments = list(initial)

    def consume(self, patch):
        self._segments = apply_patch(self._segments, patch)

    def segments(self):
        return list(self._segments)


def show(title, segments):
    print(title)
    for seg in segments:
        end = "inf" if seg.end is None else seg.end
        who = ", ".join(f"{m.source}@{m.time}#{m.seq}" for m in seg.members)
        print(f"  [{seg.start}, {end}) {seg.state}  <- {who}")
    if not segments:
        print("  <empty>")


def main():
    history = StateHistory()
    view = SegmentView(history.segments())

    def edit(description, fn):
        patch = fn()
        view.consume(patch)
        print(f"\n== {description} ==")
        print(f"   patch: -{len(patch.removed)} +{len(patch.added)} segment(s)")
        show("   view:", view.segments())

    edit("insert A@0", lambda: history.insert("dev", 1, 0, "A"))
    edit("insert A@2", lambda: history.insert("dev", 2, 2, "A"))
    edit("insert B@5", lambda: history.insert("dev", 3, 5, "B"))
    edit("insert B@1 (splits the A segment)", lambda: history.insert("dev", 4, 1, "B"))
    edit("retract B@1 (A segment heals, A@2 evidence kept)",
         lambda: history.retract("dev", 4))
    edit("correct B@5 -> C@5", lambda: history.correct("dev", 3, state="C"))

    assert view.segments() == history.segments()
    print("\nview matches direct query: OK")


if __name__ == "__main__":
    main()
