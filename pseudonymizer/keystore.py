"""Versioned, caller-provided key storage with rotation support.

Keys are supplied by the caller, held only in memory, and are never
written to logs. ``repr`` of every object in this module is redacted.
"""

from __future__ import annotations

import threading

from .errors import DuplicateKeyVersion, PseudonymError

MIN_KEY_BYTES = 16


class KeyVersion:
    """A single key version. Key material is never exposed via repr."""

    __slots__ = ("version", "_key")

    def __init__(self, version: str, key: bytes):
        if not isinstance(key, (bytes, bytearray)):
            raise TypeError("key must be bytes")
        if len(key) < MIN_KEY_BYTES:
            raise ValueError(
                f"key too short: need >= {MIN_KEY_BYTES} bytes "
                "(32 bytes recommended)"
            )
        self.version = str(version)
        self._key = bytes(key)

    @property
    def key(self) -> bytes:
        return self._key

    def __repr__(self) -> str:  # never leak key material
        return f"KeyVersion(version={self.version!r}, key=<redacted>)"


class KeyStore:
    """Thread-safe registry of key versions with an active pointer."""

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._keys: dict[str, KeyVersion] = {}
        self._active: KeyVersion | None = None

    def add(self, version: str, key: bytes) -> KeyVersion:
        kv = KeyVersion(version, key)
        with self._lock:
            if kv.version in self._keys:
                raise DuplicateKeyVersion(f"key version {kv.version!r} exists")
            self._keys[kv.version] = kv
            if self._active is None:
                self._active = kv
        return kv

    def rotate(self, version: str, key: bytes) -> KeyVersion:
        """Register ``version`` and make it the active key."""
        kv = KeyVersion(version, key)
        with self._lock:
            if kv.version in self._keys:
                raise DuplicateKeyVersion(f"key version {kv.version!r} exists")
            self._keys[kv.version] = kv
            self._active = kv
        return kv

    def active(self) -> KeyVersion:
        with self._lock:
            if self._active is None:
                raise PseudonymError("no active key version registered")
            return self._active

    def get(self, version: str) -> KeyVersion:
        with self._lock:
            try:
                return self._keys[str(version)]
            except KeyError:
                raise PseudonymError(f"unknown key version {version!r}")

    @property
    def versions(self) -> tuple[str, ...]:
        with self._lock:
            return tuple(self._keys)

    def __repr__(self) -> str:  # never leak key material
        return f"KeyStore(versions={list(self.versions)!r}, keys=<redacted>)"
