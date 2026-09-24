"""Plan executor: a real cursor over MVCC index/table data.

The executor walks a plan's arms in order.  Index arms stream (key, rid)
pairs from the index entries visible at the pinned snapshot version;
the scan arm streams table rows directly.  Row ids are de-duplicated
globally (a row reachable through several arms/intervals is read from the
table once and emitted once), while two physical rows with equal values
keep distinct rids and are both returned.  Guards (residual predicates)
are evaluated per arm with three-valued logic; only TRUE passes.
"""
from __future__ import annotations

import math

from . import predicates as P
from .compiler import IndexArm, ScanArm


class Executor:
    def __init__(self, db, plan, version, costs):
        self.db = db
        self.plan = plan
        self.version = version
        self.costs = costs
        self.arm_i = 0
        self.offset = 0
        self._streams = {}
        self.emitted = set()
        self.evaluated = set()
        self._row_cache = {}
        self.counts = {"entries": 0, "lookups": 0, "compares": 0}
        self._iv_entries = {}
        self.exhausted = False

    def position(self):
        return (self.arm_i, self.offset)

    def _stream(self, arm_i):
        cached = self._streams.get(arm_i)
        if cached is not None:
            return cached
        arm = self.plan.arms[arm_i]
        items = []
        if isinstance(arm, ScanArm):
            for rid in sorted(self.db.table.rows):
                items.append((-1, arm.guard, rid))
        elif isinstance(arm, IndexArm):
            index = self.db.indexes[arm.index_name]
            visible = index.visible(self.version)
            for iv_i, iv in enumerate(arm.intervals):
                for key, rids in visible:
                    if iv.contains(index.specs, key):
                        for rid in rids:
                            items.append((iv_i, iv.guard, rid))
        else:
            raise TypeError(arm)
        self._streams[arm_i] = items
        return items

    def _peek(self):
        while self.arm_i < len(self.plan.arms):
            stream = self._stream(self.arm_i)
            if self.offset < len(stream):
                return stream[self.offset]
            self.arm_i += 1
            self.offset = 0
        return None

    def _row(self, rid, count_lookup=True):
        if rid not in self._row_cache:
            self._row_cache[rid] = self.db.table.read(rid, self.version)
            if count_lookup:
                self.counts["lookups"] += 1
        return self._row_cache[rid]

    def next_batch(self, n):
        out = []
        while len(out) < n:
            item = self._peek()
            if item is None:
                self.exhausted = True
                break
            self.offset += 1
            iv_i, guard, rid = item
            self.counts["entries"] += 1
            key = (self.arm_i, iv_i)
            self._iv_entries[key] = self._iv_entries.get(key, 0) + 1
            if rid in self.emitted:
                continue
            eval_key = (self.arm_i, id(guard), rid)
            if eval_key in self.evaluated:
                continue
            self.evaluated.add(eval_key)
            is_scan = isinstance(self.plan.arms[self.arm_i], ScanArm)
            row = self._row(rid, count_lookup=not is_scan)
            if row is None:
                continue
            if guard is not None:
                self.counts["compares"] += 1
                if P.eval3(guard, row) is not True:
                    continue
            self.emitted.add(rid)
            out.append((rid, row))
        return out

    def exec_report(self):
        pages = 0
        for (arm_i, iv_i), entries in self._iv_entries.items():
            arm = self.plan.arms[arm_i]
            per_page = (self.costs.rows_per_page if isinstance(arm, ScanArm)
                        else self.costs.keys_per_page)
            pages += max(1, math.ceil(entries / per_page))
        return {
            "pages": pages,
            "entries": self.counts["entries"],
            "lookups": self.counts["lookups"],
            "compares": self.counts["compares"],
        }
