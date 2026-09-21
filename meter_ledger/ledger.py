"""计量账本:事件溯源 + 版本化查询 + 崩溃恢复。

- 采样、断测、校准、查询注册都是追加到 journal 的事件;
- 查询结果以"撤销(revoke)/替换(publish)"差量发布,每条差量有
  稳定 ID(``<tx>:<query>:<kind>``)和确认位置(journal 序号);
- 一次更正只重算与受影响区间相交的查询,不重算整段历史;
- checkpoint 保存未闭合采样段、查询索引与去重状态(已应用差量 ID);
  重启后从确认位置重放,已应用差量按 ID 去重,不会重复扣减;
- 事务在"旧结果已撤销、新结果尚未发布"处崩溃时,恢复阶段会按
  相同的稳定 ID 补发缺失的替换差量,已确认的差量自动跳过。
"""
from __future__ import annotations

import json
import os
from bisect import insort
from fractions import Fraction

from .curve import STEP, Calibration, SampleCurve


class SimulatedCrash(Exception):
    """演示专用:模拟进程在事务中途崩溃。"""


class Ledger:
    def __init__(self, journal_path: str, checkpoint_path: str, mode: str = STEP):
        self.journal_path = journal_path
        self.checkpoint_path = checkpoint_path
        self.curve = SampleCurve(mode)
        self.queries: dict[str, tuple[Fraction, Fraction]] = {}
        self.published: dict[str, dict] = {}
        self.applied_deltas: set[str] = set()
        self.position = 0
        self._tx_counter = 0
        self._open_tx = None
        self._tx_revokes: dict[str, list[str]] = {}
        self._tx_publishes: dict[str, set[str]] = {}
        self.history: list[tuple] = []
        self.recovery_log: list[str] = []
        self.delta_hook = None
        self._load_checkpoint()
        self._replay()
        self._finish_open_tx()

    # ------------------------------------------------------------ 公开操作
    def add_sample(self, time, value) -> None:
        time = Fraction(time)
        start, end = self.curve.sample_impact(time)
        self._append({"type": "sample", "time": str(time), "value": str(Fraction(value))})
        self._propagate(start, end, cause=f"sample@{time}")

    def declare_gap(self, start, end) -> None:
        start, end = Fraction(start), Fraction(end)
        self._append({"type": "gap", "start": str(start), "end": str(end)})
        self._propagate(start, end, cause=f"gap[{start},{end})")

    def add_calibration(self, effective_from, scale=1, offset=0) -> None:
        effective_from = Fraction(effective_from)
        self._append({
            "type": "calibration",
            "effective_from": str(effective_from),
            "scale": str(Fraction(scale)),
            "offset": str(Fraction(offset)),
        })
        self._propagate(effective_from, None, cause=f"calibration@{effective_from}")

    def register_query(self, qid: str, start, end) -> dict:
        start, end = Fraction(start), Fraction(end)
        self._append({"type": "query", "id": qid, "start": str(start), "end": str(end)})
        result = self._compute_result(qid)
        self._emit_delta(None, qid, "publish", result)
        return self.published[qid]

    # ------------------------------------------------------------ 事务传播
    def _propagate(self, start, end, cause) -> None:
        """只重算与 [start, end) 相交的查询,逐个事务地撤销/替换。"""
        affected = sorted(
            qid
            for qid, (qs, qe) in self.queries.items()
            if start < qe and (end is None or qs < end)
        )
        if not affected:
            return
        self._tx_counter += 1
        tx = f"tx-{self._tx_counter}"
        self._append({
            "type": "tx_begin", "tx": tx, "cause": cause,
            "from": str(start), "to": str(end) if end is not None else None,
        })
        revoked = []
        for qid in affected:
            old = self.published.get(qid)
            if old is None:
                continue
            revoked.append(qid)
            self._emit_delta(tx, qid, "revoke", old)
        for qid in revoked:
            self._emit_delta(tx, qid, "publish", self._compute_result(qid))
        self._append({"type": "tx_commit", "tx": tx})

    def _compute_result(self, qid: str) -> dict:
        start, end = self.queries[qid]
        known, missing = self.curve.integrate(start, end)
        return {
            "query": qid,
            "start": str(start),
            "end": str(end),
            "integral": str(known),
            "missing": str(missing),
            "sample_version": self.curve.sample_version,
            "calibration_version": self.curve.calibration_version,
        }

    def _emit_delta(self, tx, qid, kind, result) -> None:
        delta_id = f"{tx}:{qid}:{kind}" if tx else f"init:{qid}:{kind}"
        if kind == "publish":
            result = dict(result)
            result["delta_id"] = delta_id
            result["position"] = self.position + 1
        self._append({
            "type": "delta", "id": delta_id, "tx": tx,
            "query": qid, "kind": kind, "result": result,
        })
        if self.delta_hook is not None:
            self.delta_hook(delta_id, kind, qid)

    # ------------------------------------------------------------ 事件应用
    def _append(self, ev) -> None:
        ev = dict(ev)
        ev["seq"] = self.position + 1
        with open(self.journal_path, "a", encoding="utf-8") as fh:
            fh.write(json.dumps(ev, ensure_ascii=False) + "\n")
            fh.flush()
            os.fsync(fh.fileno())
        self._apply(ev)

    def _apply(self, ev) -> None:
        kind = ev["type"]
        if kind == "sample":
            self.curve.add_sample(Fraction(ev["time"]), Fraction(ev["value"]))
        elif kind == "gap":
            self.curve.declare_gap(Fraction(ev["start"]), Fraction(ev["end"]))
        elif kind == "calibration":
            self.curve.add_calibration(
                Fraction(ev["effective_from"]),
                Fraction(ev["scale"]),
                Fraction(ev["offset"]),
            )
        elif kind == "query":
            self.queries[ev["id"]] = (Fraction(ev["start"]), Fraction(ev["end"]))
        elif kind == "tx_begin":
            self._open_tx = ev["tx"]
            self._tx_revokes.setdefault(ev["tx"], [])
            self._tx_publishes.setdefault(ev["tx"], set())
            self._tx_counter = max(self._tx_counter, int(ev["tx"].split("-")[1]))
        elif kind == "delta":
            tx = ev.get("tx")
            if tx:
                if ev["kind"] == "revoke":
                    self._tx_revokes.setdefault(tx, []).append(ev["query"])
                else:
                    self._tx_publishes.setdefault(tx, set()).add(ev["query"])
            if ev["id"] not in self.applied_deltas:
                self.applied_deltas.add(ev["id"])
                if ev["kind"] == "revoke":
                    self.published.pop(ev["query"], None)
                else:
                    self.published[ev["query"]] = ev["result"]
                self.history.append((tx, ev["kind"], ev["query"], ev["result"]))
        elif kind == "tx_commit":
            if self._open_tx == ev["tx"]:
                self._open_tx = None
        self.position = ev["seq"]

    # ------------------------------------------------------------ 崩溃恢复
    def _replay(self) -> None:
        if not os.path.exists(self.journal_path):
            return
        with open(self.journal_path, encoding="utf-8") as fh:
            for line in fh:
                ev = json.loads(line)
                if ev["seq"] <= self.position:
                    continue
                self._apply(ev)

    def _finish_open_tx(self) -> None:
        """完成崩溃时未提交的事务:按稳定 ID 补发缺失的替换差量。"""
        tx = self._open_tx
        if tx is None:
            return
        self.recovery_log.append(f"发现未提交事务 {tx},继续完成")
        for qid in self._tx_revokes.get(tx, []):
            delta_id = f"{tx}:{qid}:publish"
            if qid in self._tx_publishes.get(tx, set()) or delta_id in self.applied_deltas:
                self.recovery_log.append(f"差量 {delta_id} 已确认,去重跳过")
                continue
            self._emit_delta(tx, qid, "publish", self._compute_result(qid))
            self.recovery_log.append(f"补发差量 {delta_id}")
        self._append({"type": "tx_commit", "tx": tx})
        self.recovery_log.append(f"事务 {tx} 已提交")

    # ------------------------------------------------------------ checkpoint
    def checkpoint(self) -> None:
        assert self._open_tx is None, "事务进行中不能 checkpoint"
        last_t = self.curve.times[-1] if self.curve.times else None
        state = {
            "position": self.position,
            "tx_counter": self._tx_counter,
            "sample_version": self.curve.sample_version,
            "calibration_version": self.curve.calibration_version,
            "samples": [[str(t), str(self.curve.values[t])] for t in self.curve.times],
            "gaps": [[str(s), str(e)] for s, e in self.curve.gaps],
            "calibrations": [
                {
                    "version": c.version,
                    "effective_from": str(c.effective_from),
                    "scale": str(c.scale),
                    "offset": str(c.offset),
                }
                for c in self.curve.calibrations
            ],
            "open_segment": None
            if last_t is None
            else {
                "time": str(last_t),
                "value": str(self.curve.values[last_t]),
                "note": f"未闭合采样段 [{last_t}, +inf)",
            },
            "queries": {q: [str(s), str(e)] for q, (s, e) in self.queries.items()},
            "published": self.published,
            "applied_delta_ids": sorted(self.applied_deltas),
        }
        tmp = self.checkpoint_path + ".tmp"
        with open(tmp, "w", encoding="utf-8") as fh:
            json.dump(state, fh, ensure_ascii=False, indent=1)
        os.replace(tmp, self.checkpoint_path)

    def _load_checkpoint(self) -> None:
        if not os.path.exists(self.checkpoint_path):
            return
        with open(self.checkpoint_path, encoding="utf-8") as fh:
            st = json.load(fh)
        self.position = st["position"]
        self._tx_counter = st["tx_counter"]
        curve = self.curve
        curve.sample_version = st["sample_version"]
        curve.calibration_version = st["calibration_version"]
        for t, v in st["samples"]:
            t, v = Fraction(t), Fraction(v)
            insort(curve.times, t)
            curve.values[t] = v
        curve.gaps = [(Fraction(s), Fraction(e)) for s, e in st["gaps"]]
        curve.calibrations = [
            Calibration(
                c["version"],
                Fraction(c["effective_from"]),
                Fraction(c["scale"]),
                Fraction(c["offset"]),
            )
            for c in st["calibrations"]
        ]
        self.queries = {q: (Fraction(s), Fraction(e)) for q, (s, e) in st["queries"].items()}
        self.published = st["published"]
        self.applied_deltas = set(st["applied_delta_ids"])
