"""Loading and validation of analysis specifications (JSON).

A specification has:
  relations:  {name: {"columns": {col: {"domain": [ints...]}}}}
  statistics: list of statistic objects (see README)
  query:      select or join query object
  config:     optional {"max_cells": int, "node_budget": int}

NULL is represented as JSON null / Python None. Every column domain is a
finite list of integers; NULL is implicitly an extra possible value.
"""
from __future__ import annotations

import hashlib
import json


class SpecError(ValueError):
    """Raised when a specification is malformed."""


def load_spec(path):
    with open(path, "r", encoding="utf-8") as fh:
        spec = json.load(fh)
    validate_spec(spec)
    return spec


def validate_spec(spec):
    if not isinstance(spec, dict):
        raise SpecError("spec must be a JSON object")
    relations = spec.get("relations")
    if not isinstance(relations, dict) or not relations:
        raise SpecError("spec.relations must be a non-empty object")
    for rel, rdef in relations.items():
        cols = rdef.get("columns")
        if not isinstance(cols, dict) or not cols:
            raise SpecError(f"relation {rel!r} must define columns")
        for col, cdef in cols.items():
            dom = cdef.get("domain")
            if (
                not isinstance(dom, list)
                or not dom
                or any(not isinstance(v, int) or isinstance(v, bool) for v in dom)
            ):
                raise SpecError(f"{rel}.{col}: domain must be a non-empty list of ints")
            if len(set(dom)) != len(dom):
                raise SpecError(f"{rel}.{col}: domain has duplicate values")
    stats = spec.get("statistics", [])
    if not isinstance(stats, list):
        raise SpecError("spec.statistics must be a list")
    seen_ids = set()
    for st in stats:
        sid = st.get("id")
        if not sid or sid in seen_ids:
            raise SpecError("every statistic needs a unique 'id'")
        seen_ids.add(sid)
        rel = st.get("relation")
        if rel not in relations:
            raise SpecError(f"statistic {sid}: unknown relation {rel!r}")
        stype = st.get("type")
        cols = relations[rel]["columns"]
        if stype == "row_count":
            _require_nonneg_int(st, "count", sid)
        elif stype == "histogram":
            col = st.get("column")
            if col not in cols:
                raise SpecError(f"statistic {sid}: unknown column {col!r}")
            counts = st.get("counts")
            if not isinstance(counts, dict):
                raise SpecError(f"statistic {sid}: counts must be an object")
            domain = set(cols[col]["domain"])
            for key, val in counts.items():
                try:
                    iv = int(key)
                except (TypeError, ValueError):
                    raise SpecError(f"statistic {sid}: bad histogram key {key!r}")
                if iv not in domain:
                    raise SpecError(f"statistic {sid}: value {iv} outside domain")
                if not isinstance(val, int) or val < 0:
                    raise SpecError(f"statistic {sid}: counts must be non-negative ints")
        elif stype == "null_count":
            if st.get("column") not in cols:
                raise SpecError(f"statistic {sid}: unknown column")
            _require_nonneg_int(st, "count", sid)
        elif stype == "joint_count":
            jcols = st.get("columns")
            if not isinstance(jcols, list) or len(jcols) < 2:
                raise SpecError(f"statistic {sid}: joint_count needs >= 2 columns")
            for c in jcols:
                if c not in cols:
                    raise SpecError(f"statistic {sid}: unknown column {c!r}")
            entries = st.get("counts")
            if not isinstance(entries, list):
                raise SpecError(f"statistic {sid}: counts must be a list")
            for ent in entries:
                vals = ent.get("values")
                if not isinstance(vals, list) or len(vals) != len(jcols):
                    raise SpecError(f"statistic {sid}: values must match columns")
                _require_nonneg_int(ent, "count", sid)
        else:
            raise SpecError(f"statistic {sid}: unknown type {stype!r}")
    query = spec.get("query")
    if not isinstance(query, dict):
        raise SpecError("spec.query is required")
    qtype = query.get("type")
    if qtype == "select":
        if query.get("from") not in relations:
            raise SpecError("select query: unknown 'from' relation")
    elif qtype == "join":
        if query.get("left") not in relations or query.get("right") not in relations:
            raise SpecError("join query: unknown relation")
        on = query.get("on")
        if not isinstance(on, list) or not on:
            raise SpecError("join query: 'on' must be a non-empty list of [leftCol, rightCol]")
        for pair in on:
            if not isinstance(pair, list) or len(pair) != 2:
                raise SpecError("join query: each 'on' entry must be [leftCol, rightCol]")
    else:
        raise SpecError("query.type must be 'select' or 'join'")
    # every relation used by the query must have an exact row_count statistic
    used = [query["from"]] if qtype == "select" else [query["left"], query["right"]]
    for rel in used:
        if not any(
            s.get("type") == "row_count" and s.get("relation") == rel for s in stats
        ):
            raise SpecError(f"relation {rel!r} needs an exact row_count statistic")


def _require_nonneg_int(obj, key, sid):
    v = obj.get(key)
    if not isinstance(v, int) or isinstance(v, bool) or v < 0:
        raise SpecError(f"statistic {sid}: {key} must be a non-negative int")


def spec_fingerprint(spec):
    """Stable hash over everything a certificate depends on.

    Changing any statistic, domain, or the query invalidates previously
    issued bounds and witnesses; callers must re-run / re-verify.
    """
    blob = json.dumps(
        {
            "relations": spec.get("relations"),
            "statistics": spec.get("statistics"),
            "query": spec.get("query"),
        },
        sort_keys=True,
        separators=(",", ":"),
    )
    return hashlib.sha256(blob.encode("utf-8")).hexdigest()


def relation_column_names(spec, rel):
    return list(spec["relations"][rel]["columns"].keys())


def relation_domains(spec, rel):
    cols = spec["relations"][rel]["columns"]
    return [list(cols[c]["domain"]) for c in cols]
