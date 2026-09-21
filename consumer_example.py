from __future__ import annotations

from ab_pattern import Event, Output, PatternMatcher


class MatchCollector:
    def __init__(self, name: str) -> None:
        self.name = name
        self.provisional = set()
        self.final = set()
        self.corrections = []
        self.out_of_scope = []

    def receive(self, outputs: list[Output]) -> None:
        for output in outputs:
            if output.status == "PROVISIONAL_MATCH":
                self.provisional.add(output.match_id)
            elif output.status == "REVOKED":
                self.provisional.discard(output.match_id)
            elif output.status == "FINAL_MATCH":
                self.provisional.discard(output.match_id)
                self.final.add(output.match_id)
            elif output.status == "LATE_MATCH":
                self.final.add(output.match_id)
                self.corrections.append(output)
            elif output.status == "LATE_NO_MATCH":
                self.final.discard(output.match_id)
                self.corrections.append(output)
            elif output.status == "OUT_OF_SCOPE_LATE":
                self.out_of_scope.append(output)

    def show(self) -> None:
        print(f"[{self.name}]")
        print("  pending:", _short_set(self.provisional))
        print("  final:  ", _short_set(self.final))
        for output in self.corrections:
            print(f"  correction: {output.status} {_short_id(output.match_id)} @{output.trigger_time}")
        for output in self.out_of_scope:
            print(f"  out-of-scope: {output.reason} {output.a_id} event_time={output.event_time}")


def _short_id(match_id):
    if match_id is None:
        return "-"
    _, a_id, b_id = match_id
    return f"{a_id[1]}->{b_id[1]}"


def _short_set(values):
    return sorted(_short_id(value) for value in values)


def run_overlapping_matches() -> None:
    matcher = PatternMatcher(gap_ms=5)
    sink = MatchCollector("overlap without C")
    for event in [Event("A", "k", 0, "s", 1), Event("A", "k", 1, "s", 2), Event("B", "k", 6, "s", 3)]:
        sink.receive(matcher.ingest(event))
    sink.receive(matcher.advance_watermark(6))
    sink.show()


def run_late_correction() -> None:
    matcher = PatternMatcher(gap_ms=5, allowed_lateness_ms=2)
    sink = MatchCollector("late C correction")
    for event in [Event("A", "k", 0, "s", 1), Event("B", "k", 3, "s", 2)]:
        sink.receive(matcher.ingest(event))
    sink.receive(matcher.advance_watermark(5))
    sink.show()

    sink.receive(matcher.ingest(Event("C", "k", 3, "late", 1)))
    sink.show()

    sink.receive(matcher.advance_watermark(8))
    sink.receive(matcher.ingest(Event("C", "k", 0, "too-late", 1)))
    sink.show()


def run_out_of_order_c() -> None:
    matcher = PatternMatcher(gap_ms=5)
    sink = MatchCollector("out-of-order C stays a blocker")
    for event in [
        Event("A", "k", 0, "s", 1),
        Event("A", "k", 1, "s", 2),
        Event("B", "k", 3, "s", 3),
        Event("C", "k", 2, "s", 4),
        Event("B", "k", 6, "s", 5),
    ]:
        sink.receive(matcher.ingest(event))
    sink.receive(matcher.advance_watermark(6))
    sink.show()


if __name__ == "__main__":
    run_overlapping_matches()
    run_late_correction()
    run_out_of_order_c()
