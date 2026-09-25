"""Deterministic in-memory network for the replayable sample.

Messages land in per-destination inboxes and are only moved when the
scenario script asks for it, which makes schedules fully deterministic.
Every delivery records how many attempts the envelope has made, so the
receiver's duplicate handling is observable.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Optional

from .tag import Tag


@dataclass
class Envelope:
    msg_id: str
    source: str
    target: str
    tag: Tag
    payload: str = ""
    attempts: int = 0
    injected: bool = False

    def clone_for_retry(self) -> "Envelope":
        return Envelope(
            msg_id=self.msg_id,
            source=self.source,
            target=self.target,
            tag=self.tag,
            payload=self.payload,
            attempts=self.attempts,
            injected=self.injected,
        )


class Network:
    def __init__(self) -> None:
        self._inboxes: dict[str, list[Envelope]] = {}

    def publish(self, envelope: Envelope) -> None:
        envelope.attempts += 1
        self._inboxes.setdefault(envelope.target, []).append(envelope)

    def inject(self, envelope: Envelope) -> None:
        """Insert a hostile/adversarial envelope (far-future tag)."""
        envelope.injected = True
        envelope.attempts += 1
        self._inboxes.setdefault(envelope.target, []).append(envelope)

    def redeliver(self, target: str, index: int = 0) -> bool:
        """Repeat an envelope already in an inbox (duplicate message)."""
        inbox = self._inboxes.get(target, [])
        if not inbox:
            return False
        idx = index if index < len(inbox) else len(inbox) - 1
        original = inbox[idx]
        retry = original.clone_for_retry()
        self.publish(retry)
        return True

    def next_for(self, target: str) -> Optional[Envelope]:
        inbox = self._inboxes.get(target, [])
        if not inbox:
            return None
        return inbox.pop(0)

    def pending(self, target: str) -> int:
        return len(self._inboxes.get(target, []))
