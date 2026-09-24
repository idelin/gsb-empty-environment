"""Execution engine: cursors that actually run compiled plans, with
page-at-a-time iteration and resumable tokens.

A cursor pins a snapshot (read version) for its whole lifetime; index views
and table rows are always read at that same version, so concurrent updates /
key migrations never affect an open cursor.  A token binds (plan fingerprint,
params fingerprint, snapshot id, position, stats); resuming with a different
plan/params or a released snapshot raises.
"""
from __future__ import annotations

import heapq
import json

from .compiler import plan_fingerprint
from .predicates import eval_count
from .storage import SnapshotReclaimed, span_of


class TokenError(Exception):
    pass


class PlanMismatch(TokenError):
    pass


def _new_stats():
    return {"index_entries": 0, "row_fetches": 0, "compares": 0, "pages": 0}


class Cursor:
    def __init__(self, db, plan, params=None, snapshot_id=None, page_size=100,
                 _pos=None, _stats=None):
        if snapshot_id is None:
            snapshot_id = db.begin_snapshot()
            self._owns_snapshot = True
        else:
            self._owns_snapshot = False
        self.db = db
        self.plan = plan
        self.params = params or {}
        self.sid = snapshot_id
        # raises SnapshotReclaimed if the snapshot is gone
        self.version = db.snapshot_version(snapshot_id)
        self.page_size = page_size
        self.kind = plan.kind
        self.stats = _stats if _stats is not None else _new_stats()

        if self.kind == "range":
            self.view = plan.index.view(self.version)
            self.spans = [span_of(self.view, iv) for iv in plan.intervals]
            if _pos is None:
                self.iv_idx = 0
                self.pos = self.spans[0][0] if self.spans else 0
            else:
                self.iv_idx, self.pos = _pos
        elif self.kind == "union":
            self.emit = None  # lazily materialized [(rowid, values)]
            self.pos = _pos if _pos is not None else 0
        elif self.kind == "full":
            self.rids = db.visible_rowids(self.version)
            self.pos = _pos if _pos is not None else 0
        else:
            raise ValueError(f"unknown plan kind {self.kind!r}")

    # ---- public API ----
    def fetch_page(self):
        out = []
        if self.kind == "range":
            self._fetch_range(out)
        elif self.kind == "union":
            self._fetch_union(out)
        else:
            self._fetch_full(out)
        self.stats["pages"] += 1
        return out

    def token(self):
        if self.kind == "range":
            pos = [self.iv_idx, self.pos]
        else:
            pos = self.pos
        return json.dumps({
            "v": 1,
            "kind": self.kind,
            "fp": plan_fingerprint(self.plan, self.params),
            "sid": self.sid,
            "pos": pos,
            "stats": self.stats,
        }, sort_keys=True)

    @classmethod
    def resume(cls, db, token, plan, params=None, page_size=100):
        try:
            d = json.loads(token)
        except (TypeError, json.JSONDecodeError) as e:
            raise TokenError(f"malformed token: {e}") from None
        fp = plan_fingerprint(plan, params or {})
        if d.get("fp") != fp or d.get("kind") != plan.kind:
            raise PlanMismatch("token does not match the supplied plan/params")
        # raises SnapshotReclaimed if the pinned snapshot was released
        return cls(db, plan, params, snapshot_id=d["sid"], page_size=page_size,
                   _pos=d["pos"], _stats=d.get("stats"))

    def close(self):
        if self._owns_snapshot:
            self.db.release_snapshot(self.sid)
            self._owns_snapshot = False

    # ---- range scan ----
    def _fetch_range(self, out):
        view = self.view
        spans = self.spans
        residual = self.plan.residual
        while len(out) < self.page_size and self.iv_idx < len(spans):
            s, e = spans[self.iv_idx]
            i = self.pos if self.pos > s else s
            while i < e and len(out) < self.page_size:
                _key, rid = view[i]
                self.stats["index_entries"] += 1
                vals = self.db.get(rid, self.version)
                self.stats["row_fetches"] += 1
                assert vals is not None, "index/heap version skew"
                r, n = eval_count(residual, vals, self.params)
                self.stats["compares"] += n
                if r is True:
                    out.append((rid, vals))
                i += 1
            if i >= e:
                self.iv_idx += 1
                self.pos = spans[self.iv_idx][0] if self.iv_idx < len(spans) else i
            else:
                self.pos = i
        return out

    # ---- index union ----
    def _materialize_union(self):
        child_rids = []
        for child in self.plan.children:
            view = child.index.view(self.version)
            rids = set()
            for iv in child.intervals:
                s, e = span_of(view, iv)
                for i in range(s, e):
                    rids.add(view[i][1])
                    self.stats["index_entries"] += 1
            child_rids.append(rids)
        # fetch each candidate row exactly once, even if several branches hit it
        all_rids = set().union(*child_rids) if child_rids else set()
        values = {}
        for rid in all_rids:
            values[rid] = self.db.get(rid, self.version)
            self.stats["row_fetches"] += 1
        matched = []
        for child, rids in zip(self.plan.children, child_rids):
            ok = set()
            for rid in rids:
                r, n = eval_count(child.residual, values[rid], self.params)
                self.stats["compares"] += n
                if r is True:
                    ok.add(rid)
            matched.append(sorted(ok))
        seen = set()
        emit = []
        for rid in heapq.merge(*matched):
            if rid not in seen:
                seen.add(rid)
                emit.append((rid, values[rid]))
        self.emit = emit

    def _fetch_union(self, out):
        if self.emit is None:
            self._materialize_union()
        while len(out) < self.page_size and self.pos < len(self.emit):
            out.append(self.emit[self.pos])
            self.pos += 1
        return out

    # ---- full scan ----
    def _fetch_full(self, out):
        residual = self.plan.residual
        while len(out) < self.page_size and self.pos < len(self.rids):
            rid = self.rids[self.pos]
            self.pos += 1
            vals = self.db.get(rid, self.version)
            self.stats["row_fetches"] += 1
            r, n = eval_count(residual, vals, self.params)
            self.stats["compares"] += n
            if r is True:
                out.append((rid, vals))
        return out
