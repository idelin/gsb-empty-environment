"""Load local JSON fixtures: policy versions + CDC event stream.

Fixture format:
{
  "secret": "hex-or-text",
  "policies": [
    {"version": 1, "effective_seq": 1, "strict": true,
     "schemas": {"customer": {"sensitive": ["id", "name"]}},
     "fields": {"customer": {"id": "pseudonym", "name": "pseudonym"}}}
  ],
  "events": [
    {"txid": "t1", "table": "customer", "pk": {"id": "C-1"},
     "before": null, "after": {"id": "C-1", "name": "Ann"}, "commit_seq": 1}
  ]
}
"""

from __future__ import annotations

import json

from .engine import CdcEvent
from .policy import FieldPolicy, PolicySet, TableSchema


def load_fixture(path: str):
    with open(path, "r", encoding="utf-8") as fh:
        raw = json.load(fh)
    secret = raw.get("secret", "cdc-pseudonym-secret").encode("utf-8")
    policies = [_parse_policy(p) for p in raw.get("policies", [])]
    events = [
        CdcEvent(
            txid=e["txid"],
            table=e["table"],
            pk=e["pk"],
            before=e.get("before"),
            after=e.get("after"),
            commit_seq=e["commit_seq"],
        )
        for e in raw.get("events", [])
    ]
    return secret, policies, events


def _parse_policy(raw: dict):
    policy_set = PolicySet(
        version=raw["version"],
        strict=raw.get("strict", True),
        fields={
            table: {f: _parse_field(spec) for f, spec in fields.items()}
            for table, fields in raw.get("fields", {}).items()
        },
        schemas={
            table: TableSchema(frozenset(spec.get("sensitive", [])))
            for table, spec in raw.get("schemas", {}).items()
        },
    )
    return raw.get("effective_seq", 0), policy_set


def _parse_field(spec) -> FieldPolicy:
    if isinstance(spec, str):
        return FieldPolicy(strategy=spec)
    return FieldPolicy(strategy=spec["strategy"], domain=spec.get("domain"))
