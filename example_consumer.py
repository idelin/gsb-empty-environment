from __future__ import annotations

import json

from correctable_resampler import CorrectableResampler, Mode


INDICES = range(0, 5)


def consume(resampler, snapshot):
    revisions = resampler.drain_revisions()
    print(f"consumed {len(revisions)} revision(s)")
    for revision in revisions:
        before = "unknown" if not revision.before.valid else str(revision.before.value)
        after = "unknown" if not revision.after.valid else str(revision.after.value)
        print(f"  i={revision.index}: {before} -> {after}")
        snapshot[revision.index] = revision.after
    return revisions


print("=== correction stream ===")
resampler = CorrectableResampler(0, 1, Mode.LINEAR, 10)
snapshot = {point.index: point for point in resampler.watch(INDICES)}
print("subscribed grid points:", sorted(snapshot))

resampler.ingest("left", 0, 0)
consume(resampler, snapshot)

resampler.ingest("right-late", 4, 8)
consume(resampler, snapshot)
print("late arrival fills i=2:", snapshot[2].value, snapshot[2].sample_ids)

resampler.add_breakpoint(2)
consume(resampler, snapshot)
print("breakpoint invalidates i=2:", snapshot[2].reason.value)

resampler.retract("right-late")
consume(resampler, snapshot)

print("recovered final grid:")
for point in (snapshot[index] for index in INDICES):
    print(json.dumps(point.as_dict(), sort_keys=True))

print()
print("=== release a closed prefix ===")
closed = CorrectableResampler(0, 1, Mode.LINEAR, 10)
closed.ingest("a", 0, 0)
closed.ingest("b", 2, 4)
closed.ingest("c", 5, 10)
print("working before release:", closed.retained_sample_ids())
result = closed.release_through(2)
print("release result:", result)
print("working after release:", closed.retained_sample_ids())
print("audit history:", closed.history_sample_ids())
print("future i=3 uses:", closed.point(3).sample_ids, closed.point(3).value)
