"""Atomic sign-off: the receipt row and its notification outbox row are
committed by exactly one persistent decision (single SQLite transaction)."""
from __future__ import annotations

import json
import time
from dataclasses import dataclass, field

from evidence.receiver import load_current_manifest
from evidence.store import Store
from evidence.validator import ValidationError, validate_manifest


class PublishCrash(Exception):
    """Test hook: simulates a process crash after validation, before commit."""


@dataclass
class SignoffResult:
    ok: bool
    signoff_id: int | None = None
    errors: list[ValidationError] = field(default_factory=list)
    idempotent_replay: bool = False


def sign_off(store: Store, manifest_id: str, decision: str = "accepted",
             before_commit=None) -> SignoffResult:
    manifest = load_current_manifest(store, manifest_id)
    report = validate_manifest(store, manifest)
    if not report.ok:
        return SignoffResult(ok=False, errors=report.errors)

    existing = store.conn.execute(
        "SELECT id FROM signoffs WHERE manifest_id=? AND manifest_version=? "
        "AND manifest_digest=? AND status='valid'",
        (manifest_id, manifest.version, manifest.digest),
    ).fetchone()
    if existing is not None:
        return SignoffResult(ok=True, signoff_id=existing["id"],
                             idempotent_replay=True)

    with store.atomic():
        cursor = store.conn.execute(
            "INSERT INTO signoffs (manifest_id, manifest_version,"
            " manifest_digest, decision, status, created_at)"
            " VALUES (?,?,?,?, 'valid', ?)",
            (manifest_id, manifest.version, manifest.digest,
             decision, time.time()),
        )
        signoff_id = cursor.lastrowid
        store.conn.execute(
            "INSERT INTO outbox (signoff_id, kind, payload, created_at)"
            " VALUES (?, 'signoff_notification', ?, ?)",
            (signoff_id, json.dumps({
                "manifest_id": manifest_id,
                "version": manifest.version,
                "digest": manifest.digest,
                "decision": decision,
            }), time.time()),
        )
        if before_commit is not None:
            before_commit()  # raising rolls back the shared decision
    return SignoffResult(ok=True, signoff_id=signoff_id)


def pending_outbox(store: Store) -> list[dict]:
    rows = store.conn.execute(
        "SELECT id, signoff_id, kind, payload FROM outbox "
        "WHERE delivered=0 ORDER BY id"
    ).fetchall()
    return [dict(row) for row in rows]


def confirm_delivery(store: Store, outbox_id: int) -> bool:
    """Mark a notification delivered. Returns False if it was already done:
    repeated confirmation is idempotent."""
    row = store.conn.execute(
        "SELECT delivered FROM outbox WHERE id=?", (outbox_id,)
    ).fetchone()
    if row is None:
        raise KeyError(outbox_id)
    if row["delivered"]:
        return False
    store.conn.execute("UPDATE outbox SET delivered=1 WHERE id=?", (outbox_id,))
    return True


@dataclass
class RecoveryReport:
    anomalies: list[dict]
    pending: int
    delivered: int


def recover(store: Store) -> RecoveryReport:
    """Startup invariant: every signoff notification must reference a valid
    sign-off. Orphaned rows can never exist because of the shared transaction;
    if one is ever found it is reported so the caller can quarantine it."""
    anomalies = [
        dict(row) for row in store.conn.execute(
            "SELECT o.id AS outbox_id, o.signoff_id, o.kind, s.status "
            "FROM outbox o LEFT JOIN signoffs s ON o.signoff_id = s.id "
            "WHERE o.kind='signoff_notification' AND s.id IS NULL"
        ).fetchall()
    ]
    pending = store.conn.execute(
        "SELECT COUNT(*) AS n FROM outbox WHERE delivered=0"
    ).fetchone()["n"]
    delivered = store.conn.execute(
        "SELECT COUNT(*) AS n FROM outbox WHERE delivered=1"
    ).fetchone()["n"]
    return RecoveryReport(anomalies=anomalies, pending=pending,
                          delivered=delivered)
