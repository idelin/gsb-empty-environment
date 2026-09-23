"""Local test harness: schedules messages, scan batches, the logical clock
and faults. No message service involved.

Crash semantics: restore from the last Checkpoint, then replay the script
prefix (producers redeliver at-least-once; engine dedups build messages by
seq and scan batches by id; the consumer dedups outputs by stable ID).
Because the engine is deterministic, replay reproduces the pre-crash state.
"""
from __future__ import annotations

from dataclasses import dataclass

from .consumer import TestConsumer
from .engine import JoinEngine
from .filters import default_hash


@dataclass
class Deliver:
    msg: object  # BuildData | BuildBarrier


@dataclass
class Submit:
    batch_id: str
    rows: list   # [(key, payload), ...]


@dataclass
class Rebuild:
    partition: int
    epoch: int


@dataclass
class Checkpoint:
    pass


@dataclass
class Crash:
    pass


@dataclass
class AckAll:
    pass


@dataclass
class Poll:
    pass


class Harness:
    def __init__(self, engine_kwargs=None, tmpdir=None):
        self.engine_kwargs = dict(engine_kwargs or {})
        self.hash_fn = self.engine_kwargs.get("hash_fn") or default_hash
        self.tmpdir = tmpdir
        self.engine = JoinEngine(tmpdir=tmpdir, **self.engine_kwargs)
        self.consumer = TestConsumer()
        self.last_ckpt = None
        self.output_log = []  # [(output_id, rows)] in delivery order

    def run(self, events):
        events = list(events)
        index = 0
        steps = 0
        while index < len(events):
            steps += 1
            if steps > 1_000_000:
                raise RuntimeError("harness is not making progress")
            event = events[index]
            if isinstance(event, Submit):
                if not self.engine.submit_scan_batch(event.batch_id, event.rows):
                    if index + 1 >= len(events):
                        raise RuntimeError(
                            "backpressure with no events left to drain buffers")
                    # defer the submit behind the next event, keep order
                    events[index], events[index + 1] = events[index + 1], event
                    continue
            else:
                self._apply(event, events, index)
            index += 1
        self._poll()
        return self

    def _apply(self, event, events, index):
        if isinstance(event, Deliver):
            self.engine.deliver(event.msg)
        elif isinstance(event, Rebuild):
            self.engine.rebuild(event.partition, event.epoch)
        elif isinstance(event, Checkpoint):
            self.last_ckpt = self.engine.checkpoint()
        elif isinstance(event, Poll):
            self._poll()
        elif isinstance(event, AckAll):
            self._poll()
            self.consumer.ack_all(self.engine)
        elif isinstance(event, Crash):
            self._crash_and_replay(events, index)
        else:
            raise TypeError(f"unknown event {event!r}")

    def _crash_and_replay(self, events, index):
        assert self.last_ckpt is not None, "Crash without a preceding Checkpoint"
        self.engine.close()
        self.engine = JoinEngine.restore(self.last_ckpt, hash_fn=self.hash_fn)
        for event in events[:index]:
            if isinstance(event, (Checkpoint, Crash)):
                continue
            if isinstance(event, Submit):
                if not self.engine.submit_scan_batch(event.batch_id, event.rows):
                    raise RuntimeError(
                        "backpressure during crash replay; "
                        "give fault-injection tests generous quotas")
            else:
                self._apply(event, events, -1)

    def _poll(self):
        for output in self.engine.poll():
            self.consumer.record(output)
            self.output_log.append(
                (output.batch_id, [tuple(row) for row in output.rows]))
