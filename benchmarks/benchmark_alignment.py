from __future__ import annotations

import argparse
import time

from sample_alignment import AlignmentStore, AnchorAdded, SampleAdded, WatermarkAdvanced


def benchmark(count: int, width: int, batch_size: int) -> dict[str, float]:
    store = AlignmentStore(["alt"], width)
    events = []

    for timestamp in range(count):
        if timestamp % 2 == 0:
            events.append(SampleAdded(f"s{timestamp}", "alt", timestamp, timestamp))
        events.append(AnchorAdded(f"a{timestamp}", timestamp, timestamp))
        if timestamp % 64 == 31:
            events.append(WatermarkAdvanced(timestamp - width))

    started = time.perf_counter()
    emitted = 0
    active_peak = 0
    for offset in range(0, len(events), batch_size):
        emitted += len(store.process_events(events[offset : offset + batch_size]))
        active_peak = max(active_peak, store.active_anchor_count)
    emitted += len(store.advance_watermark(count + width))
    elapsed = time.perf_counter() - started

    return {
        "input_events": len(events) + 1,
        "alignment_events": emitted,
        "seconds": elapsed,
        "active_anchors_peak": float(active_peak),
        "retained_candidates": store.retained_candidate_count,
    }


def benchmark_fan_in(count: int) -> dict[str, float]:
    store = AlignmentStore(["alt"], count + 1)
    for index in range(count):
        store.add_anchor(f"a{index}", index, index)

    started = time.perf_counter()
    emitted = len(store.add_sample("shared", "alt", count // 2, 0))
    elapsed = time.perf_counter() - started

    return {
        "input_events": count + 1,
        "alignment_events": emitted,
        "seconds": elapsed,
        "active_anchors_peak": float(store.active_anchor_count),
        "retained_candidates": store.retained_candidate_count,
    }


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--count", type=int, default=2000)
    parser.add_argument("--width", type=int, default=8)
    parser.add_argument("--batch-size", type=int, default=32)
    args = parser.parse_args()

    stream = benchmark(args.count, args.width, args.batch_size)
    fan_in = benchmark_fan_in(min(args.count, 1000))

    print("stream:")
    for key, value in stream.items():
        print(f"  {key}: {value}")
    print("fan_in:")
    for key, value in fan_in.items():
        print(f"  {key}: {value}")


if __name__ == "__main__":
    main()
