"""Pseudonym store with atomic publication, rotation and recovery checks.

Design notes
------------
* State is an immutable-by-convention snapshot. Every mutation builds a
  full successor state and publishes it with a single assignment under
  one lock, so a new pseudonym and all of its indexes (reverse owner
  map, collision counters, reference index, rotation links) become
  visible atomically. Readers never observe a half-updated table.
* Collisions are detected via the reverse owner map and resolved
  deterministically: the derivation counter is incremented until a free
  pseudonym is found, and the counter is persisted so the resolution is
  reproducible after restart or rotation. Collision injection for tests
  is done by passing a custom ``derive_fn``.
* Cancellation is cooperative: pass a ``threading.Event``; when it is
  set the operation raises :class:`OperationCancelled` before any commit
  and the store is left unchanged.
* Raw subjects are never logged; audit records carry pseudonyms only.
"""

from __future__ import annotations

import threading
from dataclasses import dataclass
from typing import Callable, Dict, FrozenSet, Optional, Tuple

from .audit import AuditLog
from .crypto import DEFAULT_PID_BYTES, derive_pseudonym
from .errors import (
    ConsistencyError,
    OperationCancelled,
    PseudonymError,
    UnknownPseudonym,
)
from .keystore import KeyStore

DomainSubject = Tuple[str, str]
DomainPid = Tuple[str, str]

# (key, domain, subject, counter) -> pseudonym
DeriveFn = Callable[[bytes, str, str, int], str]


@dataclass(frozen=True)
class State:
    """Immutable snapshot of every table; swapped atomically on commit."""

    version: str
    assignments: Dict[DomainSubject, str]      # (domain, subject) -> pid
    owners: Dict[DomainPid, str]               # (domain, pid) -> subject
    counters: Dict[DomainSubject, int]         # collision counter used
    links: Dict[DomainPid, str]                # (domain, old pid) -> new pid
    refs: Dict[DomainPid, FrozenSet[str]]      # (domain, pid) -> ref ids


def _empty_state(version: str) -> State:
    return State(
        version=version,
        assignments={},
        owners={},
        counters={},
        links={},
        refs={},
    )


def _check_cancel(cancel_event: Optional[threading.Event]) -> None:
    if cancel_event is not None and cancel_event.is_set():
        raise OperationCancelled("operation cancelled before commit")


class PseudonymStore:
    def __init__(
        self,
        keystore: KeyStore,
        *,
        audit: Optional[AuditLog] = None,
        derive_fn: Optional[DeriveFn] = None,
        pid_length: int = DEFAULT_PID_BYTES,
    ) -> None:
        self._keystore = keystore
        self._audit = audit if audit is not None else AuditLog()
        if derive_fn is None:
            def derive_fn(key: bytes, domain: str, subject: str, counter: int) -> str:
                return derive_pseudonym(
                    key, domain, subject, length=pid_length, counter=counter
                )
        self._derive = derive_fn
        self._lock = threading.Lock()
        self._state = _empty_state(keystore.active().version)

    # ------------------------------------------------------------------ #
    # internal helpers (must be called with self._lock held)
    # ------------------------------------------------------------------ #

    def _resolve_free_pid(
        self, key: bytes, domain: str, subject: str, owners: Dict[DomainPid, str]
    ) -> Tuple[str, int]:
        """Deterministic collision resolution: bump counter until free."""
        counter = 0
        while True:
            candidate = self._derive(key, domain, subject, counter)
            owner = owners.get((domain, candidate))
            if owner is None or owner == subject:
                return candidate, counter
            counter += 1

    # ------------------------------------------------------------------ #
    # public API
    # ------------------------------------------------------------------ #

    @property
    def state(self) -> State:
        """Current immutable snapshot (safe to read without the lock)."""
        return self._state

    def get_or_create(
        self,
        domain: str,
        subject: str,
        *,
        cancel_event: Optional[threading.Event] = None,
    ) -> str:
        """Return the stable pseudonym for (domain, subject), creating it
        on first use. Idempotent under concurrency: concurrent callers
        with the same subject always observe the same pseudonym."""
        _check_cancel(cancel_event)
        with self._lock:
            state = self._state
            existing = state.assignments.get((domain, subject))
            if existing is not None:
                return existing
            _check_cancel(cancel_event)
            key = self._keystore.active().key
            pid, counter = self._resolve_free_pid(
                key, domain, subject, state.owners
            )
            _check_cancel(cancel_event)
            # Atomic publication: assignment, reverse index and collision
            # counter become visible in one state swap.
            self._state = State(
                version=state.version,
                assignments={**state.assignments, (domain, subject): pid},
                owners={**state.owners, (domain, pid): subject},
                counters={**state.counters, (domain, subject): counter},
                links=state.links,
                refs=state.refs,
            )
            if counter > 0:
                self._audit.record(
                    "collision_resolved",
                    domain=domain,
                    pid=pid,
                    version=state.version,
                    counter=counter,
                )
            else:
                self._audit.record(
                    "assign", domain=domain, pid=pid, version=state.version
                )
            return pid

    def add_reference(
        self,
        domain: str,
        pid: str,
        ref_id: str,
        *,
        cancel_event: Optional[threading.Event] = None,
    ) -> None:
        """Attach an external reference id to a live pseudonym.

        Referencing an unknown pseudonym is rejected so references can
        never dangle from the start."""
        _check_cancel(cancel_event)
        with self._lock:
            state = self._state
            if (domain, pid) not in state.owners:
                raise UnknownPseudonym(
                    f"no live pseudonym {pid!r} in domain {domain!r}"
                )
            current = state.refs.get((domain, pid), frozenset())
            new_refs = {**state.refs, (domain, pid): current | {ref_id}}
            _check_cancel(cancel_event)
            self._state = State(
                version=state.version,
                assignments=state.assignments,
                owners=state.owners,
                counters=state.counters,
                links=state.links,
                refs=new_refs,
            )
            self._audit.record(
                "reference_added", domain=domain, pid=pid, ref=ref_id
            )

    def remove_reference(self, domain: str, pid: str, ref_id: str) -> None:
        with self._lock:
            state = self._state
            current = state.refs.get((domain, pid), frozenset())
            if ref_id not in current:
                raise UnknownPseudonym(
                    f"reference {ref_id!r} not attached to {pid!r}"
                )
            new_refs = dict(state.refs)
            remaining = current - {ref_id}
            if remaining:
                new_refs[(domain, pid)] = remaining
            else:
                new_refs.pop((domain, pid), None)
            self._state = State(
                version=state.version,
                assignments=state.assignments,
                owners=state.owners,
                counters=state.counters,
                links=state.links,
                refs=new_refs,
            )
            self._audit.record(
                "reference_removed", domain=domain, pid=pid, ref=ref_id
            )

    def rotate(
        self,
        new_version: str,
        new_key: bytes,
        *,
        cancel_event: Optional[threading.Event] = None,
    ) -> None:
        """Rotate to a caller-provided key version.

        Every pseudonym is re-derived under the new key, the old->new
        link table is extended, and the reference index is migrated.
        The new pseudonyms, links and migrated references are published
        in a single atomic commit; a cancellation before commit leaves
        both the store and the key store untouched. Old pseudonyms stay
        explicitly linkable via :meth:`resolve_link` but are never mixed
        into new lookups.
        """
        _check_cancel(cancel_event)
        with self._lock:
            state = self._state
            old_version = state.version

            new_assignments: Dict[DomainSubject, str] = {}
            new_owners: Dict[DomainPid, str] = {}
            new_counters: Dict[DomainSubject, int] = {}
            new_links = dict(state.links)

            for (domain, subject), old_pid in state.assignments.items():
                _check_cancel(cancel_event)
                pid, counter = self._resolve_free_pid(
                    new_key, domain, subject, new_owners
                )
                new_assignments[(domain, subject)] = pid
                new_owners[(domain, pid)] = subject
                new_counters[(domain, subject)] = counter
                new_links[(domain, old_pid)] = pid

            # Migrate the reference index; a ref pointing at a pid with
            # no assignment is a dangling reference and aborts rotation.
            new_refs: Dict[DomainPid, FrozenSet[str]] = {}
            for (domain, old_pid), refset in state.refs.items():
                target = new_links.get((domain, old_pid))
                if target is None:
                    raise ConsistencyError(
                        [
                            f"dangling reference set on ({domain!r}, "
                            f"{old_pid!r}); rotation aborted"
                        ]
                    )
                new_refs[(domain, target)] = refset

            _check_cancel(cancel_event)
            # Commit: key store and pseudonym store move together.
            self._keystore.rotate(new_version, new_key)
            self._state = State(
                version=new_version,
                assignments=new_assignments,
                owners=new_owners,
                counters=new_counters,
                links=new_links,
                refs=new_refs,
            )
            self._audit.record(
                "rotate",
                from_version=old_version,
                to_version=new_version,
                remapped=len(new_assignments),
            )

    def resolve_link(self, domain: str, old_pid: str) -> str:
        """Follow the old->new link chain to the current pseudonym.

        This is the *only* sanctioned way to relate pseudonyms across
        key versions; the mapping is explicit and auditable."""
        with self._lock:
            state = self._state
            seen = set()
            pid = old_pid
            while (domain, pid) in state.links:
                if (domain, pid) in seen:
                    raise ConsistencyError([f"link cycle at {pid!r}"])
                seen.add((domain, pid))
                pid = state.links[(domain, pid)]
            if not seen:
                raise UnknownPseudonym(
                    f"no link from {old_pid!r} in domain {domain!r}"
                )
            return pid

    def check_consistency(self) -> bool:
        """Recovery check: verify every invariant of the current state.

        * every assignment has a matching reverse owner entry
        * every owner entry has a matching assignment
        * every reference points at a live pseudonym (no dangling refs)
        * every rotation link chain resolves to a live pseudonym
        """
        with self._lock:
            state = self._state
            problems = []
            for (domain, subject), pid in state.assignments.items():
                if state.owners.get((domain, pid)) != subject:
                    problems.append(
                        f"assignment ({domain!r}, {subject!r}) -> {pid!r} "
                        "has no matching owner entry"
                    )
            for (domain, pid), subject in state.owners.items():
                if state.assignments.get((domain, subject)) != pid:
                    problems.append(
                        f"owner entry ({domain!r}, {pid!r}) has no "
                        "matching assignment"
                    )
            for domain, pid in state.refs:
                if (domain, pid) not in state.owners:
                    problems.append(
                        f"dangling reference set on ({domain!r}, {pid!r})"
                    )
            for (domain, old_pid), new_pid in state.links.items():
                # A link is valid if its chain terminates at a live
                # pseudonym; intermediate hops belong to older versions.
                seen = set()
                cursor = new_pid
                resolved = False
                while True:
                    if (domain, cursor) in state.owners:
                        resolved = True
                        break
                    hop = state.links.get((domain, cursor))
                    if hop is None or (domain, cursor) in seen:
                        break
                    seen.add((domain, cursor))
                    cursor = hop
                if not resolved:
                    problems.append(
                        f"link ({domain!r}, {old_pid!r}) -> {new_pid!r} "
                        "does not resolve to a live pseudonym"
                    )
            if problems:
                raise ConsistencyError(problems)
            return True

    def __repr__(self) -> str:
        state = self._state
        return (
            f"PseudonymStore(version={state.version!r}, "
            f"subjects={len(state.assignments)}, "
            f"refs={sum(len(r) for r in state.refs.values())})"
        )
