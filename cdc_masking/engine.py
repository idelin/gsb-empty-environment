"""CDC masking engine: applies the event-time policy version to change events."""
from __future__ import annotations

from dataclasses import dataclass

from .policy import PASSTHROUGH, PSEUDONYM, REDACT


class MaskingError(Exception):
    """Sanitized processing error. Never carries raw field values."""


@dataclass(frozen=True)
class ChangeEvent:
    tx_id: str
    table: str
    op: str  # insert | update | delete
    pk: dict
    before: dict | None
    after: dict | None
    commit_seq: int

    @classmethod
    def from_dict(cls, data):
        return cls(
            tx_id=data["tx_id"],
            table=data["table"],
            op=data["op"],
            pk=dict(data.get("pk") or {}),
            before=data.get("before"),
            after=data.get("after"),
            commit_seq=data["commit_seq"],
        )


@dataclass(frozen=True)
class MaskedEvent:
    tx_id: str
    table: str
    op: str
    commit_seq: int
    policy_version: int
    pk: dict
    before: dict | None
    after: dict | None


class MaskingEngine:
    def __init__(self, store, registry, error_log=None):
        self.store = store
        self.registry = registry
        self.error_log = error_log if error_log is not None else []

    def process(self, event):
        # Resolve the policy version in effect at the event's commit sequence.
        # A delete carrying only old keys is therefore interpreted with the
        # policy of its own time, not with the currently active key.
        pv = self.store.resolve(event.commit_seq)
        try:
            return self._process(pv, event)
        except MaskingError as exc:
            self._log(event, str(exc))
            raise
        except Exception:
            safe = MaskingError(
                f"masking failed: table={event.table} tx={event.tx_id} "
                f"seq={event.commit_seq}"
            )
            self._log(event, str(safe))
            raise safe from None

    def _log(self, event, reason):
        self.error_log.append(
            {
                "table": event.table,
                "tx_id": event.tx_id,
                "commit_seq": event.commit_seq,
                "reason": reason,
            }
        )

    def _process(self, pv, event):
        tpol = pv.tables.get(event.table)
        if tpol is None:
            raise MaskingError(f"no policy for table {event.table}")
        return MaskedEvent(
            tx_id=event.tx_id,
            table=event.table,
            op=event.op,
            commit_seq=event.commit_seq,
            policy_version=pv.version,
            pk=self._mask_row(pv, event, tpol, event.pk),
            before=self._mask_row(pv, event, tpol, event.before)
            if event.before is not None
            else None,
            after=self._mask_row(pv, event, tpol, event.after)
            if event.after is not None
            else None,
        )

    def _mask_row(self, pv, event, tpol, row):
        out = {}
        for name, value in row.items():
            fpol = tpol.fields.get(name)
            if fpol is None:
                if self.store.schemas.is_sensitive(event.table, name):
                    if self.store.strict:
                        raise MaskingError(
                            f"strict mode: no policy for sensitive field "
                            f"{event.table}.{name} (tx={event.tx_id} "
                            f"seq={event.commit_seq})"
                        )
                    out[name] = None
                else:
                    out[name] = value
            elif fpol.kind == PSEUDONYM:
                domain = tpol.domain_for(event.table, name)
                out[name] = self.registry.get_or_create(pv, domain, value)
            elif fpol.kind == REDACT:
                out[name] = None
            elif fpol.kind == PASSTHROUGH:
                out[name] = value
        return out
