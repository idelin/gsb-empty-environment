from __future__ import annotations

from sample_alignment import AlignmentOperation, AlignmentStore, SampleAdded, AnchorAdded, WatermarkAdvanced


class AlignmentConsumer:
    def __init__(self) -> None:
        self.table: dict[str, dict[str, object]] = {}

    def apply(self, event) -> None:
        if event.operation == AlignmentOperation.DELETE:
            self.table.pop(event.result.anchor_id, None)
            return

        result = event.result
        self.table[result.anchor_id] = {
            "version": result.version,
            "finalized": result.finalized,
            "alt": result.sample_id("alt"),
            "missing": list(result.missing_sources),
        }

    def show(self, title: str) -> None:
        print(title)
        for anchor_id, row in self.table.items():
            print(f"  {anchor_id}: {row}")


def main() -> None:
    store = AlignmentStore(["alt"], 2)
    consumer = AlignmentConsumer()

    def receive(event) -> None:
        for emitted in store.process_event(event):
            consumer.apply(emitted)
            print(
                f"event={emitted.event_seq} op={emitted.operation.value} "
                f"reason={emitted.reason.value} v{emitted.previous_version}->v{emitted.result.version} "
                f"alt={emitted.result.sample_id('alt')} finalized={emitted.result.finalized}"
            )

    print("main anchor=10, tolerance=2; candidates 8 and 12 are an equal-distance tie")
    receive(SampleAdded("s8", "alt", 8, 1))
    receive(AnchorAdded("m10", 10, 1))
    receive(SampleAdded("s12", "alt", 12, 2))
    consumer.show("after equal-distance later sample arrives (no correction):")

    print("\nlater sample at 9 is strictly closer")
    receive(SampleAdded("s9", "alt", 9, 3))
    consumer.show("after replacement correction:")

    print("\nretracting s9 emits another correction and falls back to s8")
    from sample_alignment import SampleRetracted

    receive(SampleRetracted("s9"))
    consumer.show("after retraction correction:")

    print("\nwatermark closes the anchor; later input cannot rewrite it")
    receive(WatermarkAdvanced(12))
    consumer.show("after finalization:")

    late_events = store.process_events(
        [SampleAdded("s10", "alt", 10, 4), SampleRetracted("s8")]
    )
    print(f"\npost-watermark changes emitted: {len(late_events)}")

    replayed = AlignmentConsumer()
    for event in store.event_log:
        replayed.apply(event)

    assert replayed.table == consumer.table
    consumer.show("replaying only emitted alignment events restores:")


if __name__ == "__main__":
    main()
