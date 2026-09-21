"""计量账本:版本化查询结果、增量修正与差量发布。

- 每个查询结果带稳定 ID(``<查询>:r<版本>``)与所用采样/校准版本。
- 迟到采样、断测声明、追溯校准只重算受影响区间,不重算整段历史。
- 结果变更以"撤销旧结果 + 发布新结果"的差量写入日志;
  差量 ID 稳定(``<结果ID>:<revoke|publish>``),重复发布可被消费者去重。
"""
from bisect import bisect_right
from fractions import Fraction

from .curve import Curve, fr


class Result:
    __slots__ = ("result_id", "value", "missing", "sample_version",
                 "calibration_version")

    def __init__(self, result_id, value, missing, sample_version,
                 calibration_version):
        self.result_id = result_id
        self.value = value
        self.missing = missing
        self.sample_version = sample_version
        self.calibration_version = calibration_version

    @property
    def complete(self):
        return self.missing == 0


class QueryState:
    def __init__(self, qid, a, b):
        self.qid = qid
        self.a = a
        self.b = b
        self.result_version = 0
        self.value = Fraction(0)
        self.missing = b - a
        self.sample_version = 0
        self.calibration_version = 0

    def current_result(self):
        return Result(f"{self.qid}:r{self.result_version}", self.value,
                      self.missing, self.sample_version,
                      self.calibration_version)


class MeterLedger:
    def __init__(self, journal, mode="step"):
        self.journal = journal
        self.mode = mode
        self.curve = Curve(mode)
        self.queries = {}
        self.sample_seq = 0
        self.gap_seq = 0
        self.calibration_version = 0
        self.published = {}  # qid -> 最新发布结果的状态
        self.deltas = []     # 本次会话发布的差量(便于演示与测试)

    # ---- 公共操作 ----

    def register_query(self, qid, a, b):
        a, b = fr(a), fr(b)
        if a >= b:
            raise ValueError("查询区间必须满足 start < end")
        self._emit({"type": "query", "query_id": qid,
                    "start": str(a), "end": str(b)})
        q = QueryState(qid, a, b)
        q.result_version = 1
        q.sample_version = self.sample_seq
        q.calibration_version = self.calibration_version
        q.value, q.missing = self.curve.integrate(
            a, b, self.sample_seq, self.calibration_version, self.gap_seq)
        self.queries[qid] = q
        self._emit_delta(q, "publish", q.current_result())

    def add_sample(self, t, v):
        t, v = fr(t), fr(v)
        old_seq = self.sample_seq
        self.sample_seq += 1
        ts = sorted({s.t for s in self.curve.samples})
        i = bisect_right(ts, t)
        t_prev = ts[i - 1] if i > 0 else None
        t_next = ts[i] if i < len(ts) else None
        self._emit({"type": "sample", "seq": self.sample_seq,
                    "t": str(t), "v": str(v)})
        self.curve.add_sample(self.sample_seq, t, v)
        lo = t if (self.mode == "step" or t_prev is None) else t_prev
        self._patch_queries(lo, t_next,
                            (old_seq, self.calibration_version, self.gap_seq),
                            (self.sample_seq, self.calibration_version, self.gap_seq))

    def declare_gap(self, a, b):
        a, b = fr(a), fr(b)
        old_seq = self.gap_seq
        self.gap_seq += 1
        self._emit({"type": "gap", "seq": self.gap_seq,
                    "start": str(a), "end": str(b)})
        self.curve.add_gap(self.gap_seq, a, b)
        self._patch_queries(a, b,
                            (self.sample_seq, self.calibration_version, old_seq),
                            (self.sample_seq, self.calibration_version, self.gap_seq))

    def add_calibration(self, effective_from, scale=1, offset=0):
        effective_from = fr(effective_from)
        old_version = self.calibration_version
        self.calibration_version += 1
        self._emit({"type": "calibration", "version": self.calibration_version,
                    "effective_from": str(effective_from),
                    "scale": str(fr(scale)), "offset": str(fr(offset))})
        self.curve.add_calibration(self.calibration_version, effective_from,
                                   fr(scale), fr(offset))
        self._patch_queries(effective_from, None,
                            (self.sample_seq, old_version, self.gap_seq),
                            (self.sample_seq, self.calibration_version, self.gap_seq))

    # ---- 恢复 ----

    @classmethod
    def recover(cls, journal, mode="step"):
        """从日志重建账本,并补发崩溃时未完成的修正差量。"""
        ledger = cls(journal, mode)
        for event in journal.read_all():
            ledger._replay_event(event)
        ledger._republish_pending()
        return ledger

    # ---- 内部 ----

    def _emit(self, event):
        return self.journal.append(event)

    def _emit_delta(self, q, kind, result):
        event = {
            "type": "delta",
            "delta_id": f"{result.result_id}:{kind}",
            "query_id": q.qid,
            "kind": kind,
            "result_id": result.result_id,
            "value": str(result.value),
            "missing": str(result.missing),
            "sample_version": result.sample_version,
            "calibration_version": result.calibration_version,
        }
        pos = self._emit(event)
        event["pos"] = pos
        self.deltas.append(event)
        pub = self.published.setdefault(q.qid, {})
        if kind == "publish":
            pub.update(result_id=result.result_id, active=True,
                       value=result.value, missing=result.missing,
                       sample_version=result.sample_version,
                       calibration_version=result.calibration_version)
        elif pub.get("result_id") == result.result_id:
            pub["active"] = False
        return event

    def _patch_queries(self, lo, hi, old_params, new_params):
        """只重算受影响区间 [lo, hi),用新旧差值修正每个相交查询。"""
        for q in self.queries.values():
            s = q.a if lo is None else max(q.a, lo)
            e = q.b if hi is None else min(q.b, hi)
            if s >= e:
                continue
            old_v, old_m = self.curve.integrate(s, e, *old_params)
            new_v, new_m = self.curve.integrate(s, e, *new_params)
            if old_v == new_v and old_m == new_m:
                continue
            self._emit_delta(q, "revoke", q.current_result())
            q.value += new_v - old_v
            q.missing += new_m - old_m
            q.sample_version = new_params[0]
            q.calibration_version = new_params[1]
            q.result_version += 1
            self._emit_delta(q, "publish", q.current_result())

    def _replay_event(self, event):
        kind = event["type"]
        if kind == "sample":
            self.sample_seq = max(self.sample_seq, event["seq"])
            self.curve.add_sample(event["seq"], fr(event["t"]), fr(event["v"]))
        elif kind == "gap":
            self.gap_seq = max(self.gap_seq, event["seq"])
            self.curve.add_gap(event["seq"], fr(event["start"]), fr(event["end"]))
        elif kind == "calibration":
            self.calibration_version = max(self.calibration_version, event["version"])
            self.curve.add_calibration(event["version"], fr(event["effective_from"]),
                                       fr(event["scale"]), fr(event["offset"]))
        elif kind == "query":
            self.queries[event["query_id"]] = QueryState(
                event["query_id"], fr(event["start"]), fr(event["end"]))
        elif kind == "delta":
            qid = event["query_id"]
            q = self.queries.get(qid)
            if q is not None:
                rv = int(event["result_id"].rsplit(":r", 1)[1])
                q.result_version = max(q.result_version, rv)
            pub = self.published.setdefault(qid, {})
            if event["kind"] == "publish":
                pub.update(result_id=event["result_id"], active=True,
                           value=fr(event["value"]), missing=fr(event["missing"]),
                           sample_version=event["sample_version"],
                           calibration_version=event["calibration_version"])
            elif pub.get("result_id") == event["result_id"]:
                pub["active"] = False

    def _republish_pending(self):
        """重放后检查:已发布结果与最新计算不一致的查询,补发撤销+替换。

        差量 ID 稳定,崩溃前已落盘的撤销会被消费者去重,不会重复扣减。
        """
        for q in self.queries.values():
            q.sample_version = self.sample_seq
            q.calibration_version = self.calibration_version
            q.value, q.missing = self.curve.integrate(
                q.a, q.b, self.sample_seq, self.calibration_version, self.gap_seq)
            pub = self.published.get(q.qid)
            if (pub and pub["active"]
                    and pub["result_id"] == q.current_result().result_id
                    and pub["value"] == q.value and pub["missing"] == q.missing):
                continue
            if pub and pub.get("result_id"):
                stale = Result(pub["result_id"], pub["value"], pub["missing"],
                               pub.get("sample_version", 0),
                               pub.get("calibration_version", 0))
                self._emit_delta(q, "revoke", stale)
            q.result_version += 1
            self._emit_delta(q, "publish", q.current_result())
