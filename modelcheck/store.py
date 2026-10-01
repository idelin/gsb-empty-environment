"""Safety-preserving compressed visited-state store.

Canonical state keys are stored zlib-compressed in slots indexed by a
64-bit hash.  On a hash hit the *full original key bytes* are decompressed
and compared, so two distinct states are never merged: compression here
saves memory but is lossless and collision-safe (unlike bitstate hashing,
which can hide states and therefore break safety).
"""
from __future__ import annotations

import hashlib
import zlib
from pathlib import Path


class CompressedStateStore:
    def __init__(self, level: int = 6):
        self._slots: dict[int, list[bytes]] = {}
        self._level = level
        self.count = 0

    @staticmethod
    def hash_key(key: bytes) -> int:
        digest = hashlib.blake2b(key, digest_size=8).digest()
        return int.from_bytes(digest, "big")

    def add(self, key: bytes) -> bool:
        """Insert key; return True iff it was not already present."""
        slot_hash = self.hash_key(key)
        slot = self._slots.get(slot_hash)
        if slot is not None:
            for blob in slot:
                if zlib.decompress(blob) == key:
                    return False
        else:
            slot = self._slots[slot_hash] = []
        slot.append(zlib.compress(key, self._level))
        self.count += 1
        return True

    def __contains__(self, key: bytes) -> bool:
        slot = self._slots.get(self.hash_key(key))
        return slot is not None and any(zlib.decompress(blob) == key for blob in slot)

    def __len__(self) -> int:
        return self.count

    def keys(self):
        """Iterate over all full canonical keys (decompressed)."""
        for slot in self._slots.values():
            for blob in slot:
                yield zlib.decompress(blob)

    def save(self, path) -> None:
        with open(path, "w", encoding="ascii") as handle:
            for slot_hash, slot in self._slots.items():
                for blob in slot:
                    handle.write(f"{slot_hash:016x} {blob.hex()}\n")

    @classmethod
    def load(cls, path) -> "CompressedStateStore":
        store = cls()
        with open(path, encoding="ascii") as handle:
            for line in handle:
                line = line.strip()
                if not line:
                    continue
                hash_hex, blob_hex = line.split(" ")
                store._slots.setdefault(int(hash_hex, 16), []).append(bytes.fromhex(blob_hex))
                store.count += 1
        return store
