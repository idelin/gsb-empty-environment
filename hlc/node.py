"""A single HLC node: physical clock, durable log, outbox and inbox.

Durability protocol for every published tag:

1. tick the volatile HLC and stage ``tag`` (+``send``/``seen`` records);
2. fsync them atomically (``commit_sync``) -- the tag is now published-safe;
3. perform the observable side effect (emit into the network);
4. fsync the ``ack`` clearing the outbox entry.

Crash windows exercised by the tests:

* ``BEFORE_SYNC``   -> nothing durable; restart re-mints in a new epoch and
                       no published tag is ever reused;
* ``AFTER_SYNC``    -> tag durable but side effect unseen; restart republishes
                       the exact same tag (reuse-safe, idempotent on peers);
* ``AFTER_PUBLISH`` -> ack lost; restart republishes; peers dedupe on
                       ``msg_id`` backed by their durable ``seen`` set.
"""
from __future__ import annotations

import os
from typing import Optional

from .clock import HybridLogicalClock, is_far_future
from .network import Envelope, Network
from .persistence import (
    CrashPoint,
    InjectedCrash,
    RecoveryState,
    SqliteDurableStore,
)
from .tag import Tag
from .time_model import Duration, Instant, OffsetTable, SyntheticClock, WallReading
from .trace import EventRecord, TraceCollector


class Node:
    def __init__(
        self,
        node_id: str,
        datadir: str,
        network: Network,
        trace: TraceCollector,
        start: WallReading,
        table: OffsetTable,
        max_logical: int = 0xFFFF,
        allowed_skew: Duration = Duration.seconds(60),
        fresh: bool = False,
    ) -> None:
        self.node_id = node_id
        self.network = network
        self.trace = trace
        self.max_logical = max_logical
        self.allowed_skew = allowed_skew
        self.clock_driver = SyntheticClock(start, table)
        os.makedirs(datadir, exist_ok=True)
        self.store_path = os.path.join(datadir, f"{node_id}.db")
        if fresh:
            for suffix in ("", "-wal", "-shm"):
                try:
                    os.remove(self.store_path + suffix)
                except FileNotFoundError:
                    pass
        self.store = SqliteDurableStore(self.store_path)
        self.alive = True
        recovery = self.store.recover()
        self.pending_sends = dict(recovery.pending_sends)
        self.seen_messages = set(recovery.seen_messages)
        self.epoch = recovery.epoch
        self._write_incarnation()
        self.hlc = HybridLogicalClock(
            node=node_id, epoch=self.epoch, max_logical=max_logical
        )
        self.hlc.start_incarnation(
            epoch=self.epoch,
            wall=self.clock_driver.now(),
            physical_floor=recovery.physical_floor,
        )
        self._record_restart(recovery)
        self._republish_pending()

    # -- boot helpers -----------------------------------------------------
    def _write_incarnation(self) -> None:
        self.store.commit_sync(
            [{"kind": "incarnation", "epoch": self.epoch}]
        )

    def _record_restart(self, recovery: RecoveryState) -> None:
        self.trace.add(
            EventRecord(
                node=self.node_id,
                kind="restart",
                tag=self.hlc.current(),
                wall=self.clock_driver.now().micros,
                detail=(
                    "initial incarnation"
                    if recovery.incarnation_count == 0
                    else f"recovered incarnation #{recovery.incarnation_count}"
                ),
            )
        )

    def _republish_pending(self) -> None:
        for msg_id, pending in list(self.pending_sends.items()):
            self.network.publish(
                Envelope(
                    msg_id=msg_id,
                    source=self.node_id,
                    target=pending.target,
                    tag=pending.tag,
                    payload=pending.payload,
                )
            )

    # -- physical clock controls -----------------------------------------
    def advance_raw(self, raw_delta: int) -> None:
        self.clock_driver.advance_raw(raw_delta)

    def install_table(self, table: OffsetTable) -> None:
        self.clock_driver.install_table(table)

    def wall_now(self) -> Instant:
        return self.clock_driver.now()

    # -- events -----------------------------------------------------------
    def local_event(self, crash: Optional[CrashPoint] = None) -> Tag:
        self._require_alive()
        wall = self.wall_now()
        tag = self.hlc.local_tick(wall)
        try:
            self.store.commit_sync([{"kind": "tag", "tag": tag.to_dict()}], crash)
        except InjectedCrash as exc:
            self.trace.add(
                EventRecord(
                    node=self.node_id,
                    kind="crash",
                    tag=tag,
                    wall=wall.micros,
                    detail=(
                        f"{exc.point.value} "
                        f"({'durable, unpublished' if exc.point is CrashPoint.AFTER_SYNC else 'tag never durable'})"
                        ": local_event"
                    ),
                )
            )
            self._die(exc, tag, record=False, detail="local_event")
            raise
        self.trace.add(
            EventRecord(
                node=self.node_id, kind="local", tag=tag, wall=wall.micros
            )
        )
        return tag

    def send(
        self,
        msg_id: str,
        target: str,
        payload: str = "",
        crash: Optional[CrashPoint] = None,
    ) -> Tag:
        self._require_alive()
        wall = self.wall_now()
        tag = self.hlc.local_tick(wall)
        try:
            self.store.commit_sync(
                [
                    {"kind": "tag", "tag": tag.to_dict()},
                    {
                        "kind": "send",
                        "msg_id": msg_id,
                        "tag": tag.to_dict(),
                        "target": target,
                        "payload": payload,
                    },
                ],
                crash,
            )
        except InjectedCrash as exc:
            if exc.point is CrashPoint.AFTER_SYNC:
                # Durable and publication is guaranteed via the outbox, so
                # the send exists causally; only the local notification died.
                self.trace.add(
                    EventRecord(
                        node=self.node_id,
                        kind="send",
                        tag=tag,
                        msg_id=msg_id,
                        peer=target,
                        wall=wall.micros,
                        payload=payload,
                    )
                )
                self.trace.add(
                    EventRecord(
                        node=self.node_id,
                        kind="crash",
                        tag=tag,
                        msg_id=msg_id,
                        peer=target,
                        wall=wall.micros,
                        detail="after_sync (durable, unpublished; outbox will republish)",
                    )
                )
            else:
                self.trace.add(
                    EventRecord(
                        node=self.node_id,
                        kind="crash",
                        tag=tag,
                        msg_id=msg_id,
                        peer=target,
                        wall=wall.micros,
                        detail="before_sync (tag never durable)",
                    )
                )
            self._die(exc, tag, record=False, detail=f"send {msg_id}")
            raise
        envelope = Envelope(msg_id, self.node_id, target, tag, payload)
        self.network.publish(envelope)
        if crash is CrashPoint.AFTER_PUBLISH:
            self.trace.add(
                EventRecord(
                    node=self.node_id,
                    kind="send",
                    tag=tag,
                    msg_id=msg_id,
                    peer=target,
                    wall=wall.micros,
                    payload=payload,
                )
            )
            self.trace.add(
                EventRecord(
                    node=self.node_id,
                    kind="crash",
                    tag=tag,
                    msg_id=msg_id,
                    peer=target,
                    wall=wall.micros,
                    detail="after_publish (ack never synced)",
                )
            )
            self._die(
                InjectedCrash(CrashPoint.AFTER_PUBLISH, "ack lost"),
                tag,
                record=False,
                detail=f"send {msg_id}",
            )
            raise InjectedCrash(CrashPoint.AFTER_PUBLISH, "ack lost")
        try:
            self.store.commit_sync([{"kind": "ack", "msg_id": msg_id}])
        except InjectedCrash as exc:  # pragma: no cover - not injected
            self._die(exc, tag, detail=f"ack {msg_id}")
            raise
        self.pending_sends.pop(msg_id, None)
        self.trace.add(
            EventRecord(
                node=self.node_id,
                kind="send",
                tag=tag,
                msg_id=msg_id,
                peer=target,
                wall=wall.micros,
                payload=payload,
            )
        )
        return tag

    def deliver(self) -> Optional[EventRecord]:
        self._require_alive()
        envelope = self.network.next_for(self.node_id)
        if envelope is None:
            return None
        wall = self.wall_now()
        if envelope.msg_id in self.seen_messages:
            record = EventRecord(
                node=self.node_id,
                kind="duplicate",
                tag=envelope.tag,
                msg_id=envelope.msg_id,
                peer=envelope.source,
                attempts=envelope.attempts,
                wall=wall.micros,
                detail="message already consumed durably",
            )
            self.trace.add(record)
            return record
        if is_far_future(envelope.tag, wall, self.hlc.current(), self.allowed_skew):
            record = EventRecord(
                node=self.node_id,
                kind="quarantine",
                tag=envelope.tag,
                msg_id=envelope.msg_id,
                peer=envelope.source,
                attempts=envelope.attempts,
                wall=wall.micros,
                detail=(
                    f"tag physical {envelope.tag.physical} exceeds wall "
                    f"{wall.micros} + skew {self.allowed_skew.micros}"
                ),
            )
            self.trace.add(record)
            return record
        tag = self.hlc.receive_tick(wall, envelope.tag)
        self.store.commit_sync(
            [
                {"kind": "tag", "tag": tag.to_dict()},
                {"kind": "seen", "msg_id": envelope.msg_id},
            ]
        )
        self.seen_messages.add(envelope.msg_id)
        record = EventRecord(
            node=self.node_id,
            kind="receive",
            tag=tag,
            msg_id=envelope.msg_id,
            peer=envelope.source,
            attempts=envelope.attempts,
            wall=wall.micros,
            payload=envelope.payload,
        )
        self.trace.add(record)
        return record

    # -- failure handling -------------------------------------------------
    def _require_alive(self) -> None:
        if not self.alive:
            raise RuntimeError(f"node {self.node_id} is dead; restart it")

    def _die(
        self,
        exc: InjectedCrash,
        tag: Optional[Tag] = None,
        record: bool = True,
        detail: str = "",
    ) -> None:
        if record and exc.point is CrashPoint.AFTER_SYNC:
            self.trace.add(
                EventRecord(
                    node=self.node_id,
                    kind="crash",
                    tag=tag,
                    wall=self.clock_driver.now().micros,
                    detail=f"{exc.point.value} (durable, unpublished): {detail}",
                )
            )
        self.alive = False
        if self.store is not None:
            self.store.close()
            self.store = None  # type: ignore[assignment]

    @classmethod
    def restart(
        cls,
        node_id: str,
        datadir: str,
        network: Network,
        trace: TraceCollector,
        start: WallReading,
        table: OffsetTable,
        max_logical: int = 0xFFFF,
        allowed_skew: Duration = Duration.seconds(60),
    ) -> "Node":
        return cls(
            node_id=node_id,
            datadir=datadir,
            network=network,
            trace=trace,
            start=start,
            table=table,
            max_logical=max_logical,
            allowed_skew=allowed_skew,
            fresh=False,
        )
