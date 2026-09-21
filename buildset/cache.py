"""Build cache with two isolated partitions.

Host tools live under ``<root>/host/<host_os>-<host_arch>/...`` and are
shared by every target built on this host. Target artifacts live under
``<root>/target/<target_os>-<target_arch>/...`` and never mix with host
tools or with artifacts for other targets.
"""
from __future__ import annotations

import json
from pathlib import Path

MARKER = "artifact.json"


class BuildCache:
    def __init__(self, root):
        self.root = Path(root)

    def path_for(self, node) -> Path:
        name, version, os_name, arch = node.cache_key
        return self.root / node.partition / f"{os_name}-{arch}" / name / version

    def lookup(self, node):
        path = self.path_for(node)
        return path if (path / MARKER).is_file() else None

    def store(self, node) -> Path:
        path = self.path_for(node)
        path.mkdir(parents=True, exist_ok=True)
        (path / MARKER).write_text(
            json.dumps(
                {
                    "name": node.name,
                    "version": node.version,
                    "kind": node.kind,
                    "partition": node.partition,
                    "cache_key": list(node.cache_key),
                },
                indent=2,
            )
            + "\n",
            encoding="utf-8",
        )
        return path

    def plan(self, nodes):
        """Split nodes into (hits, misses) without writing anything."""
        hits, misses = [], []
        for node in nodes:
            (hits if self.lookup(node) else misses).append(node)
        return hits, misses
