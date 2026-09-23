"""Stream validation: processor vs. independent oracle.

Three outcomes are kept strictly separate:

* ``invalid_stream``  - the delivery stream itself is malformed
  (conflicting duplicates, partial transactions, broken before/after
  chains, duplicate commit sequences).  Raised as ``InvalidStreamError``
  internally; the processor is never run.
* ``processing_error`` - the processor raised while consuming a
  well-formed stream.
* ``mismatch``        - the processor finished but its final state differs
  from the independent full-state oracle.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Callable

from .model import ChangeEvent, CrashPoint, DeliveryItem
from .oracle import full_state


class InvalidStreamError(ValueError):
    """The delivery stream violates well-formedness rules."""


class ProcessingError(RuntimeError):
    """The processor raised while handling a well-formed stream."""


def validate_stream(items: list[DeliveryItem]) -> None:
    """Check well-formedness; raise InvalidStreamError on any violation."""
    by_id: dict[int, ChangeEvent] = {}
    for item in items:
        if isinstance(item, CrashPoint):
            continue
        if not isinstance(item, ChangeEvent):
            raise InvalidStreamError(f"unknown delivery item: {item!r}")
        prior = by_id.get(item.event_id)
        if prior is None:
            by_id[item.event_id] = item
        elif prior != item:
            raise InvalidStreamError(
                f"event #{item.event_id} re-delivered with conflicting content"
            )

    events = list(by_id.values())

    seqs = [ev.commit_seq for ev in events]
    if len(seqs) != len(set(seqs)):
        raise InvalidStreamError("commit_seq values must be unique")

    by_tx: dict[int, list[ChangeEvent]] = {}
    for ev in events:
        by_tx.setdefault(ev.tx_id, []).append(ev)
    for tx_id, tx_events in by_tx.items():
        declared = {ev.tx_size for ev in tx_events}
        if len(declared) != 1:
            raise InvalidStreamError(f"tx {tx_id}: inconsistent tx_size")
        if len(tx_events) != declared.pop():
            raise InvalidStreamError(
                f"tx {tx_id}: partial transaction "
                f"({len(tx_events)} of {tx_events[0].tx_size} events)"
            )

    by_key: dict[tuple[str, str], list[ChangeEvent]] = {}
    for ev in events:
        by_key.setdefault(ev.key, []).append(ev)
    for key, key_events in by_key.items():
        key_events.sort(key=lambda e: e.commit_seq)
        for prev, cur in zip(key_events, key_events[1:]):
            if cur.before != prev.after:
                raise InvalidStreamError(
                    f"{key}: broken before/after chain at seq {cur.commit_seq}"
                )


@dataclass
class ValidationResult:
    status: str  # "ok" | "invalid_stream" | "processing_error" | "mismatch"
    detail: str = ""
    missing: dict = field(default_factory=dict)   # in oracle, not in processor
    unexpected: dict = field(default_factory=dict)  # in processor, not in oracle
    differing: dict = field(default_factory=dict)  # key -> (processor, oracle)

    @property
    def ok(self) -> bool:
        return self.status == "ok"

    @property
    def failed(self) -> bool:
        return self.status in ("processing_error", "mismatch")


def run_and_validate(
    items: list[DeliveryItem],
    processor_factory: Callable[[], Any],
) -> ValidationResult:
    """Run the processor over a delivery stream and check against the oracle."""
    try:
        validate_stream(items)
    except InvalidStreamError as exc:
        return ValidationResult(status="invalid_stream", detail=str(exc))

    processor = processor_factory()
    try:
        processor.run(items)
    except Exception as exc:  # noqa: BLE001 - any processor failure counts
        return ValidationResult(
            status="processing_error", detail=f"{type(exc).__name__}: {exc}"
        )

    expected = full_state(
        ev for ev in _unique_events(items)
    )
    actual = processor.state()
    if actual == expected:
        return ValidationResult(status="ok")

    missing = {k: v for k, v in expected.items() if k not in actual}
    unexpected = {k: v for k, v in actual.items() if k not in expected}
    differing = {
        k: (actual[k], expected[k])
        for k in actual.keys() & expected.keys()
        if actual[k] != expected[k]
    }
    return ValidationResult(
        status="mismatch",
        detail=f"{len(missing)} missing, {len(unexpected)} unexpected, "
        f"{len(differing)} differing keys",
        missing=missing,
        unexpected=unexpected,
        differing=differing,
    )


def _unique_events(items: list[DeliveryItem]) -> list[ChangeEvent]:
    seen: dict[int, ChangeEvent] = {}
    for item in items:
        if isinstance(item, ChangeEvent) and item.event_id not in seen:
            seen[item.event_id] = item
    return list(seen.values())
