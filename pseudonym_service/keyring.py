"""Caller-provided, versioned HMAC keys.

Keys are supplied by the caller, held only in memory, never written to
logs, audit entries, or persistence files. __repr__ redacts key material.
"""
from __future__ import annotations

MIN_KEY_BYTES = 16  # 128 bits minimum


class KeyRing:
    def __init__(self, initial_key: bytes, version: int = 1):
        self._keys: dict[int, bytes] = {}
        self._active = -1
        self.add_version(initial_key, version)
        self.activate(version)

    @staticmethod
    def _validate(key: bytes) -> bytes:
        key = bytes(key)
        if len(key) < MIN_KEY_BYTES:
            raise ValueError(f"key too short: need >= {MIN_KEY_BYTES} bytes")
        return key

    @property
    def active_version(self) -> int:
        return self._active

    def next_version(self) -> int:
        return max(self._keys) + 1

    def add_version(self, key: bytes, version: int) -> int:
        if version in self._keys:
            raise ValueError(f"key version already exists: {version}")
        self._keys[version] = self._validate(key)
        return version

    def activate(self, version: int) -> None:
        if version not in self._keys:
            raise KeyError(f"unknown key version: {version}")
        self._active = version

    def key_for(self, version: int) -> bytes:
        if version not in self._keys:
            raise KeyError(f"unknown key version: {version}")
        return self._keys[version]

    def has_version(self, version: int) -> bool:
        return version in self._keys

    @property
    def versions(self) -> tuple[int, ...]:
        return tuple(sorted(self._keys))

    def __repr__(self) -> str:
        return f"KeyRing(versions={list(self.versions)}, active={self._active}, keys=<redacted>)"
