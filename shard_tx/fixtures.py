"""Loads transaction fixtures from local JSON files (no message platform)."""

from __future__ import annotations

import json
from pathlib import Path

from .models import Change, Manifest, Shard


def load_fixture(path: str | Path) -> tuple[Manifest, list[Shard]]:
    """Read one fixture file into its manifest and shards.

    Fixture format:
        {
          "manifest": {"tx_id": ..., "tables": [...], "ttl_seconds": ...},
          "shards": [
            {"table": ..., "changes": [
              {"primary_key": ..., "before": ..., "after": ..., "commit_seq": ...}
            ]}
          ]
        }
    """
    raw = json.loads(Path(path).read_text(encoding="utf-8"))
    manifest = Manifest(
        tx_id=raw["manifest"]["tx_id"],
        tables=frozenset(raw["manifest"]["tables"]),
        ttl_seconds=float(raw["manifest"]["ttl_seconds"]),
    )
    shards = []
    for raw_shard in raw.get("shards", []):
        changes = tuple(
            Change(
                tx_id=manifest.tx_id,
                table=raw_shard["table"],
                primary_key=str(raw_change["primary_key"]),
                before=raw_change.get("before"),
                after=raw_change.get("after"),
                commit_seq=int(raw_change["commit_seq"]),
            )
            for raw_change in raw_shard.get("changes", [])
        )
        shards.append(
            Shard(
                tx_id=manifest.tx_id,
                table=raw_shard["table"],
                changes=changes,
                manifest_digest=manifest.digest,
            )
        )
    return manifest, shards
