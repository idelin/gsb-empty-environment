from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass, field
from typing import Any, Optional


@dataclass(frozen=True)
class ChangeEvent:
    """One change record from the local fixture (no messaging platform involved)."""

    tx_id: str
    table: str
    pk: str
    before: Optional[dict]
    after: Optional[dict]
    commit_seq: int  # monotonic commit sequence number (LSN-like)

    @classmethod
    def from_dict(cls, raw: dict) -> "ChangeEvent":
        missing = {"tx_id", "table", "pk", "commit_seq"} - raw.keys()
        if missing:
            raise ValueError(f"change event missing fields: {sorted(missing)}")
        if not isinstance(raw["commit_seq"], int) or raw["commit_seq"] < 1:
            raise ValueError(f"invalid commit_seq: {raw['commit_seq']!r}")
        if raw.get("before") is None and raw.get("after") is None:
            raise ValueError("change event must carry before and/or after values")
        return cls(
            tx_id=str(raw["tx_id"]),
            table=str(raw["table"]),
            pk=str(raw["pk"]),
            before=raw.get("before"),
            after=raw.get("after"),
            commit_seq=raw["commit_seq"],
        )

    @property
    def idempotency_id(self) -> str:
        """Deterministic output-side idempotency ID for this change."""
        key = f"{self.tx_id}|{self.table}|{self.pk}|{self.commit_seq}"
        return hashlib.sha256(key.encode("utf-8")).hexdigest()[:32]

    def to_output_payload(self) -> str:
        return json.dumps(
            {
                "idempotency_id": self.idempotency_id,
                "tx_id": self.tx_id,
                "table": self.table,
                "pk": self.pk,
                "before": self.before,
                "after": self.after,
                "commit_seq": self.commit_seq,
            },
            sort_keys=True,
        )


@dataclass
class OutboxRecord:
    idempotency_id: str
    payload: str
    status: str  # 'pending' | 'acked'
    commit_seq: int
