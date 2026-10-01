"""Layered disk-backed BFS frontier.

Each BFS layer's frontier is persisted as its own file, so memory only
ever holds the layer currently being expanded and checkpoint/resume can
reload any layer boundary exactly.
"""
from __future__ import annotations

import os
from pathlib import Path


class LayeredFrontier:
    def __init__(self, directory):
        self.dir = Path(directory)
        self.dir.mkdir(parents=True, exist_ok=True)

    def _path(self, layer: int) -> Path:
        return self.dir / f"layer_{layer:06d}.frontier"

    def write_layer(self, layer: int, entries) -> Path:
        """entries: iterable of (canonical_key_bytes, recovery_bytes)."""
        path = self._path(layer)
        tmp = path.with_suffix(".tmp")
        with open(tmp, "w", encoding="ascii") as handle:
            for key, recovery in entries:
                handle.write(f"{key.hex()} {recovery.hex()}\n")
        os.replace(tmp, path)  # atomic publish of the layer file
        return path

    def read_layer(self, layer: int) -> list[tuple[bytes, bytes]]:
        entries = []
        with open(self._path(layer), encoding="ascii") as handle:
            for line in handle:
                line = line.rstrip("\n")
                if not line:
                    continue
                key_hex, _, recovery_hex = line.partition(" ")
                entries.append((bytes.fromhex(key_hex), bytes.fromhex(recovery_hex)))
        return entries

    def has_layer(self, layer: int) -> bool:
        return self._path(layer).exists()
