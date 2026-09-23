"""CDC pseudonymization engine.

Processes local fixture events (txid, table, pk, before, after, commit_seq),
applies field policies consistently to before/after images, keeps stable
PK/FK mappings, resolves deletes against the historical policy in effect at
the event's commit_seq, and migrates mappings atomically on policy hot reload.
"""

from __future__ import annotations

from dataclasses import dataclass

from .errors import MigrationError, PolicyError
from .policy import PolicyHistory, PolicySet
from .pseudonymizer import Pseudonymizer
from .store import MappingStore

_DROP = object()


@dataclass(frozen=True)
class CdcEvent:
    txid: str
    table: str
    pk: dict
    before: dict | None
    after: dict | None
    commit_seq: int

    @property
    def op(self) -> str:
        if self.before is None:
            return "insert"
        if self.after is None:
            return "delete"
        return "update"


class SafeLogger:
    """Collects error records containing only codes and safe context
    (table/field names, versions, seq numbers) - never raw values."""

    def __init__(self) -> None:
        self.records: list = []

    def error(self, code: str, **context) -> None:
        self.records.append({"code": code, **context})


class Engine:
    def __init__(self, policy_set: PolicySet, secret: bytes, effective_seq: int = 0):
        self._secret = secret
        self._history = PolicyHistory()
        self._history.append(effective_seq, policy_set)
        self._pseudonymizers = {
            policy_set.version: Pseudonymizer(policy_set.version, secret)
        }
        self._stores = {policy_set.version: MappingStore(policy_set.version)}
        self.output: list = []
        self.journal: list = []  # (event, output|None) for replay validation
        self.output_version = policy_set.version
        self.logger = SafeLogger()

    # -- policy hot reload -------------------------------------------------

    def reload_policy(self, policy_set: PolicySet, effective_seq: int) -> bool:
        """Hot reload: atomically migrate PK/FK and before/after value
        mappings to the new pseudonym version. On failure keep the old
        store, old policy, and old output version."""
        new_pseudonymizer = Pseudonymizer(policy_set.version, self._secret)
        current_version = self.output_version
        try:
            new_store = self._stores[current_version].migrate(new_pseudonymizer)
        except MigrationError:
            self.logger.error("migration_failed", version=policy_set.version)
            return False
        self._pseudonymizers[policy_set.version] = new_pseudonymizer
        self._stores[policy_set.version] = new_store
        self._history.append(effective_seq, policy_set)
        self.output_version = policy_set.version
        return True

    # -- event processing --------------------------------------------------

    def process(self, event: CdcEvent):
        policy = self._history.at(event.commit_seq)
        if policy is None:
            self.logger.error(
                "no_policy", table=event.table, commit_seq=event.commit_seq
            )
            self.journal.append((event, None))
            return None
        present = set(event.pk)
        present.update((event.before or {}).keys())
        present.update((event.after or {}).keys())
        try:
            policy.validate_event(event.table, present)
        except PolicyError as exc:
            # Log only table/field names - never the raw value.
            self.logger.error(
                "policy_violation", table=exc.table, field=exc.field
            )
            self.journal.append((event, None))
            return None
        store = self._stores[policy.version]
        pseudonymizer = self._pseudonymizers[policy.version]
        out = {
            "txid": event.txid,
            "table": event.table,
            "op": event.op,
            "commit_seq": event.commit_seq,
            "policy_version": policy.version,
            "pk": self._mask_image(policy, store, pseudonymizer, event.table, event.pk),
        }
        if event.before is not None:
            out["before"] = self._mask_image(
                policy, store, pseudonymizer, event.table, event.before
            )
        if event.after is not None:
            out["after"] = self._mask_image(
                policy, store, pseudonymizer, event.table, event.after
            )
        self.output.append(out)
        self.journal.append((event, out))
        return out

    def process_all(self, events) -> list:
        return [self.process(e) for e in events]

    # -- internals ---------------------------------------------------------

    def _mask_image(self, policy, store, pseudonymizer, table, image) -> dict:
        masked = {}
        for name, value in image.items():
            result = self._mask_field(
                policy, store, pseudonymizer, table, name, value
            )
            if result is not _DROP:
                masked[name] = result
        return masked

    def _mask_field(self, policy, store, pseudonymizer, table, name, value):
        field_policy = policy.field_policy(table, name)
        if field_policy is None:  # validated earlier; defensive only
            raise PolicyError(
                f"no policy for field '{name}' on table '{table}'",
                table=table,
                field=name,
            )
        strategy = field_policy.strategy
        if strategy == "invisible":
            return _DROP
        if strategy == "redact":
            return "REDACTED"
        if strategy == "plain":
            return value
        scope = field_policy.scope(table, name)
        return store.get_or_create(pseudonymizer, scope, value)

    # -- replay support ----------------------------------------------------

    def replay_clone(self) -> "Engine":
        """Independent clone with the same policy history and secret but
        fresh empty mapping stores (pseudonyms are deterministic)."""
        clone = Engine.__new__(Engine)
        clone._secret = self._secret
        clone._history = self._history.copy()
        clone._pseudonymizers = {
            version: Pseudonymizer(version, self._secret)
            for version in self._history.versions
        }
        clone._stores = {
            version: MappingStore(version) for version in self._history.versions
        }
        clone.output = []
        clone.journal = []
        clone.output_version = self.output_version
        clone.logger = SafeLogger()
        return clone

    def commit_replay(self, events, references):
        """Replay events on a throwaway clone, validate the synthetic
        reference graph, and only then swap output. On validation failure
        the old output and old output version are kept."""
        from .replay import validate_replay

        trial = self.replay_clone()
        trial.process_all(events)
        report = validate_replay(trial, events, references)
        if report.ok:
            self.output = trial.output
            self.output_version = trial.output_version
        else:
            self.logger.error(
                "replay_validation_failed", failures=len(report.failures)
            )
        return report
