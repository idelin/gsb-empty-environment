"""Durable coordinator kernel.

Wraps the pure :mod:`wmcoord.engine` with:

* a write-ahead journal (the command is durable before its effects; effects
  are durable before anything is published);
* stable decision IDs.  Publishing is a separate journaled action, so a crash
  after sealing but before/after publishing re-sends the *same* ID;
* CRC checkpoints recording gaps, acks, handoff state, unconfirmed decisions
  and the journal position;
* torn-tail truncation on open and hard rejection of middle corruption.

Crash hooks raise :class:`CrashNow`; the harness treats it as immediate
process death (in-flight memory is discarded) and reopens the kernel.
"""
from __future__ import annotations

from typing import Callable

from . import engine
from .journal import (
    BaseLog,
    R_COMMAND,
    R_DECISION,
    R_HANDOFF_DONE,
    read_checkpoint,
    write_checkpoint,
)
from .model import (
    Command,
    ProtocolError,
    PublishDecision,
    ConfirmDecision,
    SubmitResult,
)

CrashHook = Callable[[], None]


class CrashNow(Exception):
    """Injected process death."""


def _effect_key(kind: str, payload: dict) -> tuple:
    if kind == R_DECISION:
        return (R_DECISION, payload["id"], payload["boundary"])
    if kind == R_HANDOFF_DONE:
        return (
            R_HANDOFF_DONE,
            payload["id"],
            payload["parent"],
            payload["boundary"],
            tuple(payload["children"]),
        )
    raise ProtocolError(f"unknown effect kind {kind}")


class Kernel:
    def __init__(
        self,
        log: BaseLog,
        checkpoint_path: str | None = None,
        crash_hook: CrashHook | None = None,
    ) -> None:
        self.log = log
        self.checkpoint_path = checkpoint_path
        self.crash_hook = crash_hook
        self.state = engine.new_state()

    # -------------------------------------------------------------- recovery

    @classmethod
    def open(
        cls,
        log: BaseLog,
        checkpoint_path: str | None = None,
        crash_hook: CrashHook | None = None,
    ) -> "Kernel":
        kernel = cls(log, checkpoint_path, crash_hook)
        kernel._recover()
        return kernel

    def _recover(self) -> None:
        records = self.log.read_records()
        snapshot = None
        resume_after = 0
        if self.checkpoint_path:
            raw = read_checkpoint(self.checkpoint_path)
            if (
                raw is not None
                and raw.get("version") == 1
                and raw.get("log_size") is not None
            ):
                # Trust checkpoint only if its log prefix is fully present.
                if not records or raw["log_size"] <= records[-1][3]:
                    snapshot = raw["state"]
                    resume_after = raw["log_size"]
        if snapshot is not None:
            self.state = engine.restore_state(snapshot)
        else:
            self.state = engine.new_state()
            resume_after = 0
        self._replay(records, resume_after)

    def _replay(self, records, resume_after: int) -> None:
        """Replay command transactions, validating and repairing effects."""
        grouped: dict[int, list] = {}
        order: list[int] = []
        for txn, rec, start, end in records:
            if end <= resume_after:
                continue
            if txn not in grouped:
                grouped[txn] = []
                order.append(txn)
            grouped[txn].append(rec)
        for txn in order:
            recs = grouped[txn]
            first = recs[0]
            if first["k"] != R_COMMAND:
                raise ProtocolError("journal transaction missing command")
            command = first["p"]
            applied = engine.apply(self.state, command)
            expected = {
                _effect_key(kind, payload)
                for kind, payload in applied.effects
            }
            present = set()
            for extra in recs[1:]:
                present.add(_effect_key(extra["k"], extra["p"]))
            if present - expected:
                raise ProtocolError("journal contains unexpected effects")
            for kind, payload in applied.effects:
                if _effect_key(kind, payload) not in present:
                    # Torn tail between command and its effect: repair it with
                    # the deterministic effect the command would have written.
                    self.log.append_effect(kind, txn, payload)

    # ------------------------------------------------------------ submission

    def submit(self, command: Command) -> SubmitResult:
        """Validate, journal and apply one input command.

        The command is applied to the in-memory state first: invalid commands
        raise before anything is journaled, so recovery never replays a
        command the engine would reject.  A crash between apply and the
        journal write simply loses the command (the process died; the driver
        may retry), which is indistinguishable from the command never having
        arrived.
        """
        applied = engine.apply(self.state, command)
        self._crash("before_command_write")
        txn, _ = self.log.append_command(command)
        self._crash("after_command_write")
        for kind, payload in applied.effects:
            if kind == R_HANDOFF_DONE:
                self._crash("before_handoff_write")
            self.log.append_effect(kind, txn, payload)
            if kind == R_HANDOFF_DONE:
                self._crash("after_handoff_write")
            if kind == R_DECISION:
                self._crash("after_decision_write")
        return applied.to_result()

    def publish(self, decision_id: str, boundary: int) -> SubmitResult:
        """Mark a sealed decision as published (durable bookkeeping)."""
        command = PublishDecision(decision_id=decision_id, boundary=boundary)
        applied = engine.apply(self.state, command)
        self._crash("before_publish_write")
        self.log.append_command(command)
        self._crash("after_publish_write")
        return applied.to_result()

    def confirm(self, decision_id: str) -> SubmitResult:
        command = ConfirmDecision(decision_id)
        applied = engine.apply(self.state, command)
        self.log.append_command(command)
        return applied.to_result()

    def _crash(self, point: str) -> None:
        if self.crash_hook is not None:
            self.crash_hook(point)

    # -------------------------------------------------------------- queries

    @property
    def boundary(self) -> int:
        return self.state["boundary"]

    def pending_decisions(self) -> list[tuple[str, int, str]]:
        """Decisions durable but not yet confirmed: ``(id, boundary, status)``.

        On restart the coordinator re-publishes exactly these, preserving
        stable IDs so the downstream can de-duplicate.
        """
        return [
            (did, info["boundary"], info["status"])
            for did, info in self.state["decisions"].items()
            if info["status"] != "confirmed"
        ]

    def save_checkpoint(self) -> int:
        """Durably persist a snapshot; returns the covered log size."""
        if self.checkpoint_path is None:
            raise ProtocolError("no checkpoint path configured")
        log_size = self.log.size
        write_checkpoint(
            self.checkpoint_path,
            {
                "version": 1,
                "log_size": log_size,
                "state": engine.snapshot_state(self.state),
            },
        )
        return log_size
