from __future__ import annotations

from typing import Iterable, Mapping, Sequence

from .core import (
    AlignmentResult,
    AnchorAdded,
    AnchorRetracted,
    Sample,
    SampleAdded,
    SampleRetracted,
    WatermarkAdvanced,
)


def reference_snapshot(
    events: Iterable[object],
    auxiliary_sources: Sequence[str],
    tolerance: int | Mapping[str, int],
) -> dict[str, AlignmentResult]:
    sources = tuple(dict.fromkeys(auxiliary_sources))
    if isinstance(tolerance, Mapping):
        tolerances = {source: int(tolerance[source]) for source in sources}
    else:
        tolerances = {source: int(tolerance) for source in sources}
    max_tolerance = max(tolerances.values())

    samples: dict[str, Sample] = {}
    active: dict[str, tuple[int, int, dict[str, str | None]]] = {}
    frozen: dict[str, tuple[int, int, dict[str, str | None]]] = {}
    watermark: int | None = None

    def choose(source: str, anchor_timestamp: int) -> str | None:
        limit = tolerances[source]
        candidates = [
            sample
            for sample in samples.values()
            if sample.source == source and abs(sample.timestamp - anchor_timestamp) <= limit
        ]
        candidates.sort(
            key=lambda sample: (
                abs(sample.timestamp - anchor_timestamp),
                sample.timestamp,
                sample.seq,
                sample.sample_id,
            )
        )
        return candidates[0].sample_id if candidates else None

    def recompute(state: tuple[int, int, dict[str, str | None]]) -> None:
        anchor_timestamp, _, matches = state
        for source in sources:
            matches[source] = choose(source, anchor_timestamp)

    for event in events:
        if isinstance(event, SampleAdded):
            samples[event.sample_id] = Sample(
                event.sample_id,
                event.source,
                event.timestamp,
                event.seq,
            )
            for state in active.values():
                recompute(state)
        elif isinstance(event, SampleRetracted):
            samples.pop(event.sample_id, None)
            for state in active.values():
                recompute(state)
        elif isinstance(event, AnchorAdded):
            if watermark is not None and event.timestamp <= watermark:
                continue
            if event.anchor_id in active or event.anchor_id in frozen:
                continue
            matches: dict[str, str | None] = {}
            state = (event.timestamp, event.seq, matches)
            active[event.anchor_id] = state
            recompute(state)
        elif isinstance(event, AnchorRetracted):
            active.pop(event.anchor_id, None)
        elif isinstance(event, WatermarkAdvanced):
            if watermark is not None and event.timestamp <= watermark:
                continue
            watermark = event.timestamp
            for anchor_id, (anchor_timestamp, _, matches) in list(active.items()):
                if anchor_timestamp + max_tolerance <= watermark:
                    frozen[anchor_id] = active.pop(anchor_id)

    snapshot: dict[str, AlignmentResult] = {}
    all_anchors: dict[str, tuple[int, int, Mapping[str, str | None]]] = {}
    all_anchors.update(active)
    all_anchors.update(frozen)
    for anchor_id, (anchor_timestamp, seq, matches) in all_anchors.items():
        ordered = tuple((source, matches.get(source)) for source in sources)
        missing = tuple(source for source in sources if matches.get(source) is None)
        snapshot[anchor_id] = AlignmentResult(
            anchor_id,
            seq,
            anchor_timestamp,
            ordered,
            missing,
            0,
            anchor_id in frozen,
        )
    return dict(
        sorted(
            snapshot.items(),
            key=lambda item: (item[1].anchor_timestamp, item[1].anchor_seq, item[0]),
        )
    )
