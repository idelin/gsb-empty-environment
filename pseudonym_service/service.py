"""Thread-safe pseudonym service.

Properties:
- Same (domain, subject) under the same key version -> stable pseudonym.
- Different domains -> different pseudonyms (HMAC domain separation).
- Collisions are detected against the registry and resolved deterministically
  by incrementing the derivation epoch; a test-only hook can inject them.
- Key rotation stages all new records and the old->new mapping locally, then
  commits in a single critical section: publish is atomic, and a cancellation
  or failure before commit leaves no partial state.
- Raw subjects and keys are never written to the audit log or to disk.
"""
from __future__ import annotations

import json
import threading
from typing import Callable

from .audit import AuditLog
from .crypto import derive_digest, format_pseudonym
from .keyring import KeyRing
from .registry import Record, Registry


class CancelledError(Exception):
    """Raised by a cancel_check callback to abort a rotation before commit."""


CollisionHook = Callable[[str, str, int], bool]
CancelCheck = Callable[[], None]


class PseudonymService:
    def __init__(
        self,
        keyring: KeyRing,
        audit: AuditLog | None = None,
        collision_hook: CollisionHook | None = None,
    ):
        self._keyring = keyring
        self._registry = Registry()
        self._audit = audit if audit is not None else AuditLog()
        self._collision_hook = collision_hook
        self._lock = threading.RLock()

    # -- issuance ---------------------------------------------------------

    def get_pseudonym(self, domain: str, subject: str) -> str:
        """Stable pseudonym for (domain, subject) under the active key version."""
        with self._lock:
            version = self._keyring.active_version
            existing = self._registry.lookup_subject(version, domain, subject)
            if existing is not None:
                return existing.pseudonym
            key = self._keyring.key_for(version)
            taken = set(self._registry.records)
            record = self._mint(version, key, domain, subject, taken)
            self._registry.add(record)
            self._audit.record("issue", record.pseudonym, domain=domain, key_version=version)
            return record.pseudonym

    def _mint(self, version: int, key: bytes, domain: str, subject: str,
              taken: set[str]) -> Record:
        """Derive a collision-free pseudonym; resolution is deterministic."""
        epoch = 0
        while True:
            forced = (
                self._collision_hook is not None
                and self._collision_hook(domain, subject, epoch)
            )
            digest = derive_digest(key, domain, subject, epoch)
            pseudonym = format_pseudonym(version, digest)
            if not forced and pseudonym not in taken:
                if epoch:
                    self._audit.record(
                        "collision_resolved", pseudonym,
                        domain=domain, key_version=version, epoch=epoch,
                    )
                return Record(pseudonym, domain, subject, version, epoch)
            epoch += 1

    # -- rotation ---------------------------------------------------------

    def rotate_key(self, new_key: bytes, cancel_check: CancelCheck | None = None) -> int:
        """Rotate to a new caller-provided key.

        Stages new-version records for every known subject plus an explicit
        old->new mapping, then commits atomically. cancel_check() may raise
        CancelledError at checkpoints; the observable state is then unchanged.
        """
        with self._lock:
            old_version = self._keyring.active_version
            new_version = self._keyring.next_version()
            new_key = KeyRing._validate(new_key)

            taken = set(self._registry.records)
            staged_records: list[Record] = []
            staged_map: dict[str, str] = {}
            for domain, subject in self._registry.subjects_for_version(old_version):
                if cancel_check is not None:
                    cancel_check()
                record = self._mint(new_version, new_key, domain, subject, taken)
                taken.add(record.pseudonym)
                old_record = self._registry.lookup_subject(old_version, domain, subject)
                staged_map[old_record.pseudonym] = record.pseudonym
                staged_records.append(record)
            if cancel_check is not None:
                cancel_check()

            # Commit: single critical section, no partial state is visible.
            self._keyring.add_version(new_key, new_version)
            self._keyring.activate(new_version)
            for record in staged_records:
                self._registry.add(record)
            self._registry.rotation_map.update(staged_map)
            self._audit.record(
                "rotate", None,
                from_version=old_version, to_version=new_version,
                subjects=len(staged_records),
            )
            return new_version

    # -- resolution -------------------------------------------------------

    def resolve(self, pseudonym: str) -> Record:
        """Direct lookup; old-version references never dangle after rotation."""
        record = self._registry.owner(pseudonym)
        if record is None:
            raise KeyError(f"unknown pseudonym: {pseudonym}")
        return record

    def resolve_current(self, pseudonym: str) -> Record:
        """Follow the rotation map to the current-version record."""
        seen = set()
        current = pseudonym
        while current in self._registry.rotation_map:
            if current in seen:
                raise ValueError("rotation map cycle detected")
            seen.add(current)
            current = self._registry.rotation_map[current]
        return self.resolve(current)

    # -- persistence & consistency ---------------------------------------

    def save(self, path: str) -> None:
        """Persist registry state. Keys are never persisted."""
        with self._lock:
            payload = {
                "active_version": self._keyring.active_version,
                "known_versions": list(self._keyring.versions),
                "registry": self._registry.to_dict(),
            }
            with open(path, "w", encoding="utf-8") as fh:
                json.dump(payload, fh, indent=2, sort_keys=True)

    @classmethod
    def load(cls, path: str, keyring: KeyRing, **kwargs) -> "PseudonymService":
        """Restore state into a service backed by a caller-supplied keyring."""
        with open(path, "r", encoding="utf-8") as fh:
            payload = json.load(fh)
        service = cls(keyring, **kwargs)
        service._registry = Registry.from_dict(payload["registry"])
        if keyring.active_version != payload["active_version"]:
            raise ValueError("keyring active version does not match saved state")
        for version in payload["known_versions"]:
            if not keyring.has_version(version):
                raise ValueError(f"keyring is missing saved key version: {version}")
        service.verify_consistency()
        return service

    def verify_consistency(self) -> None:
        """Invariant check used after recovery and in tests."""
        with self._lock:
            for key, pseudonym in self._registry.by_subject.items():
                assert self._registry.records[pseudonym].key_version == key[0]
                assert self._registry.records[pseudonym].domain == key[1]
                assert self._registry.records[pseudonym].subject == key[2]
            for record in self._registry.records.values():
                assert self._keyring.has_version(record.key_version), (
                    f"record references unknown key version {record.key_version}"
                )
            for old, new in self._registry.rotation_map.items():
                assert old in self._registry.records, f"dangling old reference: {old}"
                assert new in self._registry.records, f"dangling new reference: {new}"

    # -- introspection (no secrets) ---------------------------------------

    @property
    def audit_log(self) -> AuditLog:
        return self._audit

    @property
    def active_version(self) -> int:
        return self._keyring.active_version

    def rotation_map(self) -> dict[str, str]:
        with self._lock:
            return dict(self._registry.rotation_map)
