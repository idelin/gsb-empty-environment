from __future__ import annotations

from disordered_counter import CounterAnalyzer, Sample, SourceSpec


def show(title: str, engine: CounterAnalyzer) -> None:
    print(f"\n{title}")
    for interval in engine.active_intervals:
        print(
            f"  {interval.interval_id}: status={interval.status}, "
            f"delta={interval.delta}, config={interval.config_version}"
        )
    summary = engine.summary()
    source = summary.sources[0]
    print(
        f"  known_delta={source.known_delta}, exact={source.exact}, "
        f"unknown={source.unknown_interval_ids}, resets={source.reset_boundaries}"
    )


def main() -> None:
    engine = CounterAnalyzer(
        {"dev": SourceSpec(width=8, max_gap=10)},
        config_version="counter-config-2026-09",
    )

    engine.add_samples(
        [
            Sample("s-250", "dev", event_time=0, count=250),
            Sample("s-3", "dev", event_time=4, count=3),
        ]
    )
    show("250 -> 3: decidable 8-bit wrap", engine)

    engine.add_sample(Sample("s-reset", "dev", event_time=9, count=3, reset=True))
    show("Same value with an explicit reset", engine)

    revisions = engine.add_sample(Sample("s-late-reset", "dev", event_time=12, count=5))
    print("\nLate sample revisions:")
    for revision in revisions:
        interval_id = revision.interval.interval_id if revision.interval else None
        print(
            f"  #{revision.revision_id} {revision.action}: {interval_id}, "
            f"supersedes={revision.supersedes}, trigger={revision.trigger_sample_id}"
        )

    engine.add_sample(Sample("s-far", "dev", event_time=100, count=80))
    show("Long gap: incomplete determinable total", engine)

    engine.add_samples(
        [
            Sample("s-mid-1", "dev", event_time=20, count=20),
            Sample("s-mid-2", "dev", event_time=30, count=40),
        ]
    )
    show("Two valid samples still leave unknown sub-intervals", engine)


if __name__ == "__main__":
    main()
