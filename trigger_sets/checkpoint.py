"""Checkpoints bind query results to the exact version of every rule set."""

from __future__ import annotations

import hashlib
from dataclasses import dataclass


class CheckpointMismatch(RuntimeError):
    """Raised when a rule set changed after the checkpoint was captured."""


def _canonical(stream) -> str:
    pauses = ",".join(f"[{p.start},{p.end})" for p in sorted(stream.pauses))
    exceptions = ",".join(str(e) for e in sorted(stream.exceptions))
    switches = ",".join(f"@{sw.at}->{sw.phase}" for sw in stream.switches)
    return (
        f"{stream.name}|v{stream.version}|p={stream.period}|phi={stream.phase}"
        f"|pauses={pauses}|exceptions={exceptions}|switches={switches}"
    )


@dataclass(frozen=True)
class Checkpoint:
    """Binds every stream's rule version plus a digest of the rule content."""

    versions: tuple[tuple[str, int], ...]
    digest: str

    @classmethod
    def capture(cls, streams) -> "Checkpoint":
        ordered = sorted(streams, key=lambda s: s.name)
        digest = hashlib.sha256()
        for stream in ordered:
            digest.update(_canonical(stream).encode("utf-8"))
            digest.update(b"\x00")
        versions = tuple((s.name, s.version) for s in ordered)
        return cls(versions, digest.hexdigest())

    def verify(self, streams) -> None:
        current = Checkpoint.capture(streams)
        if current != self:
            raise CheckpointMismatch(
                "rule versions changed since checkpoint: "
                f"{self.versions} -> {current.versions}"
            )
