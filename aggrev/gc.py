"""Garbage collection of old revisions on the publisher side.

A revision may only be collected when *every* registered downstream has
acknowledged a version that descends from it - the ack is the sole evidence.
Collected deltas are folded into the base snapshot (the current value is
unchanged) and id tombstones are kept so a late redelivery of a collected
revision is still recognised as a duplicate, never re-applied.
"""
from __future__ import annotations

from .models import GENESIS_VERSION


def collect(publisher) -> list[str]:
    """Fold ack-covered revisions into the base snapshot. Returns collected ids."""

    def mutate(state: dict) -> list[str]:
        acks = state["acks"]
        if not acks:
            return []
        versions = state["versions"]

        def strict_ancestors(version_ids) -> set[str]:
            seen: set[str] = set()
            stack = [p for vid in version_ids for p in versions.get(vid, {}).get("parents", [])]
            while stack:
                vid = stack.pop()
                if vid in seen:
                    continue
                seen.add(vid)
                stack.extend(versions.get(vid, {}).get("parents", []))
            return seen

        per_downstream = [strict_ancestors(a["versions"]) for a in acks.values()]
        collectable = set.intersection(*per_downstream) if per_downstream else set()
        collectable.discard(GENESIS_VERSION)
        collectable &= set(versions)  # only versions still live

        collected: list[str] = []
        for vid in sorted(collectable):
            rid = versions[vid]["revision_id"]
            if rid is None:
                continue
            revision = state["revisions"].pop(rid)
            for table, delta in revision["correction"]["deltas"].items():
                state["base"][table] = state["base"].get(table, 0) + delta
            state["retired_revisions"][rid] = revision["fingerprint"]
            state["retired_versions"].append(vid)
            del versions[vid]
            collected.append(rid)
        return collected

    return publisher.store.commit(mutate)
