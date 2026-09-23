"""Synthetic reference-graph replay validation across policy versions."""
from __future__ import annotations

from dataclasses import dataclass, replace

_MISSING = object()


@dataclass(frozen=True)
class ReplayReport:
    ok: bool
    versions: tuple
    nodes: int
    edges: int
    failures: tuple


def _resolve(registry, version, token):
    try:
        return registry.resolve(version, token)
    except KeyError:
        return _MISSING


def _strip_invisible(event):
    """Drop redacted (None-valued) fields from a masked event."""
    def strip(row):
        if row is None:
            return None
        return {k: v for k, v in row.items() if v is not None}

    return replace(
        event,
        pk=strip(event.pk),
        before=strip(event.before),
        after=strip(event.after),
    )


def validate_replay(masked_events, store, registry, strip_invisible=False):
    """Build the synthetic reference graph and check that every FK token
    resolves to a raw value that exists as a PK of the referenced table,
    across policy versions. Invisible (redacted) fields never participate
    in node identity or edges."""
    failures = []
    parent_raws = {}
    edges = []
    nodes = 0
    versions = set()
    events = (
        [_strip_invisible(ev) for ev in masked_events]
        if strip_invisible
        else list(masked_events)
    )
    for ev in events:
        pv = store.get(ev.policy_version)
        versions.add(pv.version)
        tpol = pv.tables[ev.table]
        nodes += 1
        for name in tpol.primary_key:
            token = ev.pk.get(name)
            if token is None:
                continue
            raw = _resolve(registry, pv.version, token)
            if raw is _MISSING:
                failures.append(
                    f"unresolvable pk token: table={ev.table} field={name} "
                    f"tx={ev.tx_id} seq={ev.commit_seq}"
                )
            else:
                parent_raws.setdefault(ev.table, set()).add(raw)
        for row in (ev.before, ev.after):
            if not row:
                continue
            for fk in tpol.foreign_keys:
                token = row.get(fk.field)
                if token is None:
                    continue
                raw = _resolve(registry, pv.version, token)
                edges.append((ev, fk, raw))
    for ev, fk, raw in edges:
        if raw is _MISSING:
            failures.append(
                f"unresolvable fk token: table={ev.table} field={fk.field} "
                f"tx={ev.tx_id} seq={ev.commit_seq}"
            )
        elif raw not in parent_raws.get(fk.ref_table, set()):
            failures.append(
                f"dangling reference: table={ev.table} field={fk.field} "
                f"-> {fk.ref_table}.{fk.ref_field} "
                f"tx={ev.tx_id} seq={ev.commit_seq}"
            )
    return ReplayReport(
        ok=not failures,
        versions=tuple(sorted(versions)),
        nodes=nodes,
        edges=len(edges),
        failures=tuple(failures),
    )


def check_invisible_non_interference(masked_events, store, registry):
    """Replay with and without invisible fields; reports must be identical."""
    plain = validate_replay(masked_events, store, registry)
    stripped = validate_replay(
        masked_events, store, registry, strip_invisible=True
    )
    return plain == stripped


class VersionedOutput:
    """Publishes masked outputs per policy version; a failed validation keeps
    the previously published output version."""

    def __init__(self):
        self._outputs = {}
        self.current_version = None

    def publish(self, version, masked_events, report):
        if not report.ok:
            return False
        self._outputs[version] = list(masked_events)
        self.current_version = version
        return True

    def output(self, version=None):
        return self._outputs.get(
            self.current_version if version is None else version
        )
