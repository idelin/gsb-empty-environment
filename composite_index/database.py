"""Database facade: MVCC writes, snapshots, paged query execution.

A query is compiled once against a pinned snapshot; the returned page
carries an opaque resume token.  Tokens are HMAC-signed and bind the
compiled plan (hence the predicate and all its parameters), the snapshot
id and the exact cursor position.  Reusing a token after the snapshot was
reclaimed, with a different query, or after the cursor advanced, is
rejected with TokenError.
"""
from __future__ import annotations

import base64
import hashlib
import hmac
import json
from dataclasses import dataclass

from . import predicates as P
from .compiler import CostModel, compile_plan
from .executor import Executor
from .mvcc import Index, Mvcc, Table, normalize_row


class TokenError(Exception):
    pass


@dataclass
class Page:
    rows: list          # [(rid, row_dict), ...]
    next_token: object  # str or None when the plan is exhausted
    plan: dict          # chosen plan report incl. cost breakdown
    executed: dict      # cumulative executed counts so far


class Database:
    def __init__(self, columns, costs=None, secret=b"composite-index-demo"):
        self.columns = tuple(columns)
        self.costs = costs or CostModel()
        self.mvcc = Mvcc()
        self.table = Table(self.columns)
        self.indexes = {}
        self._snapshots = {}
        self._sid_seq = 0
        self._cursors = {}
        self._cur_seq = 0
        self._secret = secret

    # -- schema & writes --------------------------------------------------

    def create_index(self, name, specs):
        self.indexes[name] = Index(name, specs)

    def insert(self, row):
        row = normalize_row(row)
        version = self.mvcc.bump()
        rid = self.table.insert(row, version)
        for ix in self.indexes.values():
            ix.add(ix.key_of(row), rid, version)
        return rid

    def update(self, rid, new_row):
        new_row = normalize_row(new_row)
        version = self.mvcc.bump()
        old = self.table.read(rid, version)
        if old is None:
            raise KeyError(rid)
        self.table.update(rid, new_row, version)
        for ix in self.indexes.values():
            ix.remove(ix.key_of(old), rid, version)
            ix.add(ix.key_of(new_row), rid, version)

    def delete(self, rid):
        version = self.mvcc.bump()
        old = self.table.read(rid, version)
        if old is None:
            raise KeyError(rid)
        self.table.delete(rid, version)
        for ix in self.indexes.values():
            ix.remove(ix.key_of(old), rid, version)

    # -- snapshots ---------------------------------------------------------

    def begin_snapshot(self):
        self._sid_seq += 1
        sid = f"s{self._sid_seq}"
        self._snapshots[sid] = self.mvcc.version
        return sid

    def release_snapshot(self, sid):
        self._snapshots.pop(sid, None)
        for cid in [c for c, (_, s, _) in self._cursors.items() if s == sid]:
            del self._cursors[cid]

    # -- queries -----------------------------------------------------------

    def compile(self, pred, budget=8):
        return compile_plan(self, P.nnf(pred), budget, self.costs)

    def execute(self, pred, page_size=100, budget=8):
        sid = self.begin_snapshot()
        plan, candidates = self.compile(pred, budget)
        cursor = Executor(self, plan, self._snapshots[sid], self.costs)
        cid = f"c{self._cur_seq}"
        self._cur_seq += 1
        self._cursors[cid] = (cursor, sid, plan.fingerprint)
        rows = cursor.next_batch(page_size)
        token = None
        if cursor.exhausted:
            self._drop_cursor(cid)
        else:
            token = self._make_token(cid, cursor, sid, plan, page_size)
        return Page(rows, token, self._plan_report(plan, candidates),
                    cursor.exec_report())

    def fetch(self, token):
        payload = self._parse_token(token)
        cid = payload["cid"]
        entry = self._cursors.get(cid)
        if entry is None:
            raise TokenError("unknown cursor: finished or snapshot reclaimed")
        cursor, sid, fingerprint = entry
        if sid not in self._snapshots:
            raise TokenError("snapshot has been reclaimed")
        if payload["fp"] != fingerprint:
            raise TokenError("token does not match this plan/parameters")
        if payload["sid"] != sid:
            raise TokenError("token does not match this snapshot")
        if tuple(payload["pos"]) != cursor.position():
            raise TokenError("stale or replayed token")
        rows = cursor.next_batch(payload["ps"])
        next_token = None
        if cursor.exhausted:
            self._drop_cursor(cid)
        else:
            next_token = self._make_token(cid, cursor, sid, cursor.plan,
                                          payload["ps"])
        return Page(rows, next_token, None, cursor.exec_report())

    # -- internals -----------------------------------------------------------

    def _drop_cursor(self, cid):
        entry = self._cursors.pop(cid, None)
        if entry is not None:
            _, sid, _ = entry
            if not any(s == sid for _, s, _ in self._cursors.values()):
                self._snapshots.pop(sid, None)

    def _make_token(self, cid, cursor, sid, plan, page_size):
        payload = {
            "cid": cid,
            "sid": sid,
            "fp": plan.fingerprint,
            "pos": list(cursor.position()),
            "ps": page_size,
        }
        raw = base64.urlsafe_b64encode(
            json.dumps(payload, sort_keys=True).encode()).decode()
        sig = hmac.new(self._secret, raw.encode(),
                       hashlib.sha256).hexdigest()[:32]
        return f"{raw}.{sig}"

    def _parse_token(self, token):
        try:
            raw, sig = token.rsplit(".", 1)
        except ValueError:
            raise TokenError("malformed token") from None
        expected = hmac.new(self._secret, raw.encode(),
                            hashlib.sha256).hexdigest()[:32]
        if not hmac.compare_digest(sig, expected):
            raise TokenError("token signature mismatch")
        try:
            return json.loads(base64.urlsafe_b64decode(raw.encode()))
        except Exception as exc:
            raise TokenError("undecodable token") from exc

    def _plan_report(self, plan, candidates):
        report = plan.report()
        report["candidates"] = [
            {"kind": c.kind, "estimate": dict(c.estimate),
             "fingerprint": c.fingerprint}
            for c in candidates
        ]
        return report
