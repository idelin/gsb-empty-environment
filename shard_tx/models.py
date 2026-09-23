"""Domain models for cross-shard business transactions."""

from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass, field
from enum import Enum
from typing import Any, Optional


class TxState(Enum):
    """Lifecycle of a transaction known to the coordinator."""

    COLLECTING = "collecting"  # waiting for shards
    PENDING = "pending"        # timed out, marked undecided; never partially committed
    COMMITTED = "committed"    # manifest complete, published atomically


@dataclass(frozen=True)
class Change:
    """A single row mutation carried by a shard."""

    tx_id: str
    table: str
    primary_key: str
    before: Optional[dict]
    after: Optional[dict]
    commit_seq: int


@dataclass(frozen=True)
class Manifest:
    """Declares which table shards a transaction must assemble before publish."""

    tx_id: str
    tables: frozenset
    ttl_seconds: float

    @property
    def digest(self) -> str:
        """Canonical digest of the manifest content (tx id + expected tables)."""
        canonical = json.dumps(
            {"tx_id": self.tx_id, "tables": sorted(self.tables)},
            sort_keys=True,
            separators=(",", ":"),
        )
        return hashlib.sha256(canonical.encode("utf-8")).hexdigest()


@dataclass(frozen=True)
class Shard:
    """A batch of changes for one table of one transaction, from a local fixture."""

    tx_id: str
    table: str
    changes: tuple
    manifest_digest: str

    def __post_init__(self):
        for change in self.changes:
            if change.tx_id != self.tx_id or change.table != self.table:
                raise ValueError(
                    f"change {change!r} does not belong to shard "
                    f"tx={self.tx_id} table={self.table}"
                )
