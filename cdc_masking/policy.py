"""Policy model, schema registry, versioned pseudonym keys and atomic hot updates."""
from __future__ import annotations

import hashlib
import hmac
import secrets
from dataclasses import dataclass

PSEUDONYM = "pseudonym"
REDACT = "redact"
PASSTHROUGH = "passthrough"
_KINDS = frozenset({PSEUDONYM, REDACT, PASSTHROUGH})


class PolicyError(Exception):
    """Policy violation. Messages carry table/field names only, never raw values."""


@dataclass(frozen=True)
class FieldPolicy:
    kind: str

    def __post_init__(self):
        if self.kind not in _KINDS:
            raise PolicyError(f"unknown field policy kind: {self.kind}")


@dataclass(frozen=True)
class ForeignKey:
    field: str
    ref_table: str
    ref_field: str


@dataclass(frozen=True)
class TablePolicy:
    primary_key: tuple
    fields: dict
    foreign_keys: tuple = ()

    def domain_for(self, table, field_name):
        """Pseudonym domain: FK fields share the referenced PK domain so that
        primary/foreign key tokens stay joinable."""
        for fk in self.foreign_keys:
            if fk.field == field_name:
                return (fk.ref_table, fk.ref_field)
        return (table, field_name)


class PolicyVersion:
    """Immutable policy set with its own pseudonym key (version)."""

    def __init__(self, version, tables, key=None):
        self.version = version
        self.tables = tables
        self.key = key if key is not None else secrets.token_bytes(32)

    def token(self, domain, raw):
        table, field_name = domain
        msg = f"{table}|{field_name}|{type(raw).__name__}|{raw}".encode("utf-8")
        digest = hmac.new(self.key, msg, hashlib.sha256).hexdigest()
        return f"v{self.version}-{digest[:20]}"


class SchemaRegistry:
    """Known fields per table and whether they are sensitive (schema evolution)."""

    def __init__(self, tables=None):
        self._tables = dict(tables or {})

    def evolve(self, table, fields):
        """Register new/changed fields. fields: {name: sensitive_bool}."""
        current = self._tables.setdefault(table, {})
        for name, sensitive in fields.items():
            current[name] = {"sensitive": bool(sensitive)}

    def is_sensitive(self, table, field_name):
        return bool(self._tables.get(table, {}).get(field_name, {}).get("sensitive"))

    def fields(self, table):
        return dict(self._tables.get(table, {}))


def _norm(raw):
    try:
        hash(raw)
    except TypeError:
        return repr(raw)
    return raw


class MappingRegistry:
    """Explicit raw->token mappings per policy version.

    PK/FK and before/after value mappings live here so a policy hot update can
    migrate them atomically: the new version's map is fully built first and
    installed in one step; any failure leaves the old version untouched.
    """

    def __init__(self):
        self._by_version = {}

    def versions(self):
        return list(self._by_version)

    def install(self, version, mapping):
        self._by_version[version] = dict(mapping)

    def get_or_create(self, policy_version, domain, raw):
        mapping = self._by_version.setdefault(policy_version.version, {})
        key = (domain, _norm(raw))
        token = mapping.get(key)
        if token is None:
            token = policy_version.token(domain, raw)
            mapping[key] = token
        return token

    def resolve(self, version, token):
        """Trusted-boundary lookup: token -> raw value (used by replay checks)."""
        for (domain, raw), tok in self._by_version.get(version, {}).items():
            if tok == token:
                return raw
        raise KeyError(f"token not found in policy version {version}")

    def snapshot(self, version):
        return dict(self._by_version.get(version, {}))


class PolicyStore:
    """Timeline of policy versions keyed by commit sequence, plus hot updates."""

    def __init__(self, initial, schemas, strict=True):
        self.schemas = schemas
        self.strict = strict
        self._timeline = [(0, initial)]

    @property
    def active(self):
        return self._timeline[-1][1]

    def get(self, version):
        for _, pv in self._timeline:
            if pv.version == version:
                return pv
        raise PolicyError(f"unknown policy version: {version}")

    def resolve(self, commit_seq):
        """Policy version in effect at a given commit sequence (event time),
        never 'whatever key is current now'."""
        chosen = self._timeline[0][1]
        for seq, pv in self._timeline:
            if commit_seq >= seq:
                chosen = pv
            else:
                break
        return chosen

    def hot_update(self, new_version, registry, valid_from_seq):
        """Atomically migrate PK/FK and value mappings and activate the new
        version. On any failure the old version stays active and no mapping
        is mutated."""
        if any(pv.version == new_version.version for _, pv in self._timeline):
            raise PolicyError(f"duplicate policy version: {new_version.version}")
        if self.strict:
            missing = []
            for table, tpol in new_version.tables.items():
                for fname, meta in self.schemas.fields(table).items():
                    if meta.get("sensitive") and fname not in tpol.fields:
                        missing.append(f"{table}.{fname}")
            if missing:
                raise PolicyError(
                    "strict mode: missing policy for sensitive fields: "
                    + ", ".join(sorted(missing))
                )
        known = set()
        for version in registry.versions():
            known.update(registry.snapshot(version))
        new_map = {}
        for domain, raw in known:
            new_map[(domain, raw)] = new_version.token(domain, raw)
        registry.install(new_version.version, new_map)
        self._timeline.append((valid_from_seq, new_version))
        self._timeline.sort(key=lambda item: item[0])
        return new_version
