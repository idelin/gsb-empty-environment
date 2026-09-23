"""Per-owner key state with idempotent apply and epoch fencing."""
from __future__ import annotations

from typing import Any, Optional

from .events import ChangeEvent, Key


class StaleOwnerError(Exception):
    """A stale owner tried to accept an event for a key it no longer owns."""


class KeySlice:
    """A transferable copy of one key's state (value + dedup watermark + epoch)."""

    def __init__(
        self,
        value: Optional[dict[str, Any]],
        watermark: Optional[tuple[int, str]],
        epoch: Optional[int],
    ):
        self.value = value
        self.watermark = watermark
        self.epoch = epoch


class KeyStateStore:
    """State owned by exactly one partition owner.

    Idempotency: each key tracks a (lsn, txid) watermark; deliveries at or
    below the watermark are dropped, so duplicates never duplicate state.
    Fencing: each key tracks the highest ownership epoch seen; deliveries
    stamped with an older epoch are rejected.
    """

    def __init__(self, owner_id: str):
        self.owner_id = owner_id
        self.state: dict[Key, dict[str, Any]] = {}
        self.watermarks: dict[Key, tuple[int, str]] = {}
        self.epochs: dict[Key, int] = {}

    def apply(self, event: ChangeEvent, epoch: int) -> bool:
        key = event.key
        fenced = self.epochs.get(key)
        if fenced is not None and epoch < fenced:
            raise StaleOwnerError(
                f"{self.owner_id}: stale epoch {epoch} < {fenced} for {key}"
            )
        self.epochs[key] = epoch if fenced is None else max(fenced, epoch)
        mark = (event.lsn, event.txid)
        seen = self.watermarks.get(key)
        if seen is not None and mark <= seen:
            return False  # duplicate delivery: do not apply twice
        self.watermarks[key] = mark
        if event.after is None:
            self.state.pop(key, None)  # delete
        else:
            self.state[key] = dict(event.after)  # insert/update
        return True

    def export_key(self, key: Key) -> KeySlice:
        value = self.state.get(key)
        return KeySlice(
            value=dict(value) if value is not None else None,
            watermark=self.watermarks.get(key),
            epoch=self.epochs.get(key),
        )

    def import_key(self, key: Key, slice_: KeySlice) -> bool:
        """Merge a transferred slice. Re-importing equal/older state is a no-op,
        so repeated migration never duplicates state."""
        if slice_.watermark is None and slice_.value is None and slice_.epoch is None:
            return False
        seen = self.watermarks.get(key)
        if (
            seen is not None
            and slice_.watermark is not None
            and seen >= slice_.watermark
        ):
            return False
        if slice_.value is not None:
            self.state[key] = dict(slice_.value)
        if slice_.watermark is not None:
            self.watermarks[key] = slice_.watermark
        if slice_.epoch is not None:
            self.epochs[key] = max(slice_.epoch, self.epochs.get(key, 0))
        return True

    def remove_key(self, key: Key) -> None:
        self.state.pop(key, None)
        self.watermarks.pop(key, None)
        self.epochs.pop(key, None)
