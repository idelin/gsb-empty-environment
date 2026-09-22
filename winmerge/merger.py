"""Windowed result merger (窗口结果汇合器).

Merges sharded partial results into window publications.  A window is
published only when *compatible* slices cover the full current shard
set: same shard epoch, same rule version, watermark inside the window,
latest local version, not revoked.  Missing shards, stale versions,
revocations and timeouts are distinct, separately logged outcomes.

Publications are two-phase: the publish record (binding a version
vector and a deterministic result id) is appended to the hash-chained
log and fsynced *before* downstream delivery, so a crash in between is
recovered by re-delivering the same result id; the downstream applies
deltas idempotently.  Corrections after publication emit compensation
events; history is never rewritten.

All state is derived by replaying the event log in ``state_dir`` —
the implementation is fully replayable.
"""
from __future__ import annotations

import hashlib
import os
from dataclasses import dataclass, field

from . import protocol
from .store import EventLog, canonical


@dataclass(frozen=True)
class WindowSpec:
    window: str
    wm_start: int  # inclusive input-watermark bound
    wm_end: int  # exclusive input-watermark bound
    deadline: int  # tick ``now`` beyond this expires the window


@dataclass
class WindowState:
    spec: WindowSpec
    status: str = "open"  # open | published | expired
    slices: dict[str, dict[int, dict]] = field(default_factory=dict)
    revoked: set[tuple[str, int]] = field(default_factory=set)
    publishes: list[dict] = field(default_factory=list)
    acked: set[str] = field(default_factory=set)


class WindowMerger:
    def __init__(
        self,
        state_dir: str,
        windows: list[WindowSpec],
        initial_shards: list[str],
        downstream=None,
        crash_hook=None,
    ):
        self.state_dir = state_dir
        os.makedirs(state_dir, exist_ok=True)
        self.log = EventLog(os.path.join(state_dir, "events.jsonl"))
        self.windows = {w.window: WindowState(spec=w) for w in windows}
        self.initial_shards = tuple(initial_shards)
        self.handoffs: list[protocol.Handoff] = []
        self.downstream = downstream
        self.crash_hook = crash_hook or (lambda stage, payload: None)
        for rec in self.log.records():
            self._apply(rec["payload"])

    # ------------------------------------------------------------------
    # shard assignment / lineage
    # ------------------------------------------------------------------
    def current_epoch(self) -> int:
        return max((h.epoch for h in self.handoffs), default=1)

    def leaves(self) -> frozenset[str]:
        current = set(self.initial_shards)
        for h in sorted(self.handoffs, key=lambda x: x.epoch):
            for parent, children in h.splits.items():
                current.discard(parent)
                current.update(children)
        return frozenset(current)

    def ancestors(self, shard: str) -> set[str]:
        parent_of = {}
        for h in self.handoffs:
            for parent, children in h.splits.items():
                for child in children:
                    parent_of[child] = parent
        out = set()
        while shard in parent_of:
            shard = parent_of[shard]
            out.add(shard)
        return out

    # ------------------------------------------------------------------
    # public entry points
    # ------------------------------------------------------------------
    def handle(self, event: dict) -> list[str]:
        ev = protocol.parse(event)
        if isinstance(ev, protocol.Partial):
            return self._on_partial(ev)
        if isinstance(ev, protocol.Revoke):
            return self._on_revoke(ev)
        if isinstance(ev, protocol.Handoff):
            return self._on_handoff(ev)
        if isinstance(ev, protocol.Ack):
            return self._on_ack(ev)
        if isinstance(ev, protocol.Tick):
            return self._on_tick(ev)
        raise AssertionError(ev)

    def recover(self) -> list[str]:
        """Re-deliver published-but-unacked results after a crash."""
        redelivered = []
        for ws in self.windows.values():
            for payload in ws.publishes:
                if payload["result_id"] not in ws.acked:
                    self._deliver(payload)
                    redelivered.append(payload["result_id"])
        return redelivered

    # ------------------------------------------------------------------
    # event handlers
    # ------------------------------------------------------------------
    def _on_partial(self, p: protocol.Partial) -> list[str]:
        ws = self.windows.get(p.window)
        if ws is None:
            return self._reject(p, "unknown_window")
        if ws.status == "expired":
            return self._reject(p, "window_expired")
        spec = ws.spec
        if not (spec.wm_start <= p.watermark < spec.wm_end):
            return self._reject(p, "watermark_out_of_window")
        epoch = self.current_epoch()
        if p.epoch < epoch:
            return self._reject(p, "stale_epoch")
        if p.epoch > epoch:
            return self._reject(p, "future_epoch")
        if p.shard not in self.leaves():
            return self._reject(p, "unknown_shard")
        existing = ws.slices.get(p.shard, {})
        if p.local_version in existing:
            old = existing[p.local_version]
            if old["value"] == p.value and old["rule_version"] == p.rule_version:
                return self._reject(p, "duplicate")
            return self._reject(p, "conflicting_version")
        if existing and max(existing) > p.local_version:
            return self._reject(p, "stale_version")

        payload = {
            "rec": "slice_accepted",
            "window": p.window,
            "shard": p.shard,
            "epoch": p.epoch,
            "local_version": p.local_version,
            "watermark": p.watermark,
            "rule_version": p.rule_version,
            "value": p.value,
        }
        self._record(payload)
        actions = [f"accepted {p.shard} v{p.local_version} -> {p.window}"]
        if ws.status == "published":
            actions += self._maybe_compensate(ws)
        else:
            actions += self._maybe_publish(ws)
        return actions

    def _on_revoke(self, r: protocol.Revoke) -> list[str]:
        ws = self.windows.get(r.window)
        key = (r.shard, r.local_version)
        if (
            ws is None
            or r.local_version not in ws.slices.get(r.shard, {})
            or key in ws.revoked
        ):
            self._record({
                "rec": "revoke_ignored",
                "window": r.window,
                "shard": r.shard,
                "local_version": r.local_version,
                "reason": r.reason,
            })
            return [f"revoke ignored for {r.shard} v{r.local_version}"]
        self._record({
            "rec": "slice_revoked",
            "window": r.window,
            "shard": r.shard,
            "local_version": r.local_version,
            "reason": r.reason,
        })
        actions = [f"revoked {r.shard} v{r.local_version} in {r.window}"]
        if ws.status == "published":
            last = ws.publishes[-1]
            if last["version_vector"].get(r.shard) == r.local_version:
                actions += self._maybe_compensate(ws)
        return actions

    def _on_handoff(self, h: protocol.Handoff) -> list[str]:
        leaves = self.leaves()
        known = set(leaves)
        for past in self.handoffs:
            known.update(past.splits)
            for children in past.splits.values():
                known.update(children)
        problems = []
        if h.epoch <= self.current_epoch():
            problems.append(f"epoch {h.epoch} not after current {self.current_epoch()}")
        for parent, children in h.splits.items():
            if parent not in leaves:
                problems.append(f"{parent} is not a live shard")
            for child in children:
                if child in known:
                    problems.append(f"{child} already exists")
        if problems:
            self._record({
                "rec": "handoff_rejected",
                "manifest_id": h.manifest_id,
                "epoch": h.epoch,
                "reasons": problems,
            })
            return [f"handoff {h.manifest_id} rejected: {'; '.join(problems)}"]
        self._record({
            "rec": "handoff_registered",
            "manifest_id": h.manifest_id,
            "epoch": h.epoch,
            "splits": {k: list(v) for k, v in h.splits.items()},
        })
        actions = [
            f"handoff {h.manifest_id} @epoch{h.epoch}: "
            + ", ".join(f"{k}->{list(v)}" for k, v in h.splits.items())
        ]
        for ws in self.windows.values():
            if ws.status == "open":
                actions += self._maybe_publish(ws)
        return actions

    def _on_ack(self, a: protocol.Ack) -> list[str]:
        known = {
            p["result_id"]: ws
            for ws in self.windows.values()
            for p in ws.publishes
        }
        ws = known.get(a.result_id)
        if ws is None:
            self._record({"rec": "ack_unknown", "result_id": a.result_id})
            return [f"ack for unknown result {a.result_id}"]
        if a.result_id in ws.acked:
            self._record({"rec": "ack_duplicate", "result_id": a.result_id})
            return [f"duplicate ack for {a.result_id}"]
        self._record({"rec": "publish_acked", "result_id": a.result_id})
        return [f"acked {a.result_id}"]

    def _on_tick(self, t: protocol.Tick) -> list[str]:
        actions = []
        for ws in self.windows.values():
            if ws.status == "open" and t.now > ws.spec.deadline:
                missing = sorted(self.leaves() - set(self._best_slices(ws)))
                self._record({
                    "rec": "window_expired",
                    "window": ws.spec.window,
                    "deadline": ws.spec.deadline,
                    "now": t.now,
                    "missing_shards": missing,
                })
                actions.append(
                    f"window {ws.spec.window} expired, missing: {missing}"
                )
        return actions

    # ------------------------------------------------------------------
    # publish / compensate
    # ------------------------------------------------------------------
    def _best_slices(self, ws: WindowState) -> dict[str, tuple[int, dict]]:
        best = {}
        for shard, versions in ws.slices.items():
            live = [
                (lv, p) for lv, p in versions.items()
                if (shard, lv) not in ws.revoked
            ]
            if live:
                best[shard] = max(live, key=lambda x: x[0])
        return best

    def _complete_sets(self, ws: WindowState) -> dict[str, dict[str, int]]:
        """rule_version -> version vector, only for full-coverage sets."""
        expected = self.leaves()
        best = self._best_slices(ws)
        groups: dict[str, dict[str, int]] = {}
        for shard in expected:
            if shard not in best:
                continue
            lv, p = best[shard]
            groups.setdefault(p["rule_version"], {})[shard] = lv
        return {rv: vec for rv, vec in groups.items() if set(vec) == expected}

    def _value_of(self, ws: WindowState, vector: dict[str, int]) -> float:
        return sum(ws.slices[shard][lv]["value"] for shard, lv in vector.items())

    def _result_id(self, payload_core: dict) -> str:
        digest = hashlib.sha256(canonical(payload_core).encode()).hexdigest()
        return "res-" + digest[:16]

    def _maybe_publish(self, ws: WindowState) -> list[str]:
        complete = self._complete_sets(ws)
        if not complete:
            return [f"window {ws.spec.window} still pending"]
        rule_version = sorted(complete)[0]
        vector = complete[rule_version]
        value = self._value_of(ws, vector)
        core = {
            "kind": "result",
            "window": ws.spec.window,
            "rule_version": rule_version,
            "epoch": self.current_epoch(),
            "version_vector": dict(sorted(vector.items())),
            "value": value,
            "comp_index": 0,
        }
        payload = {
            "rec": "publish",
            **core,
            "delta": value,
            "base_result_id": None,
            "retracted": False,
            "result_id": self._result_id(core),
        }
        self._emit_publish(ws, payload)
        return [
            f"PUBLISHED {ws.spec.window} = {value} "
            f"(rules {rule_version}, vector {payload['version_vector']}, "
            f"id {payload['result_id']})"
        ]

    def _maybe_compensate(self, ws: WindowState) -> list[str]:
        last = ws.publishes[-1]
        last_value = 0 if last["retracted"] else last["value"]
        complete = self._complete_sets(ws)
        comp_index = len(ws.publishes)
        if not complete:
            if last["retracted"]:
                return []
            core = {
                "kind": "compensation",
                "window": ws.spec.window,
                "rule_version": None,
                "epoch": self.current_epoch(),
                "version_vector": {},
                "value": None,
                "comp_index": comp_index,
            }
            payload = {
                "rec": "publish",
                **core,
                "delta": -last_value,
                "base_result_id": last["result_id"],
                "retracted": True,
                "result_id": self._result_id(core),
            }
            self._emit_publish(ws, payload)
            return [
                f"COMPENSATION(retract) {ws.spec.window} delta {-last_value} "
                f"id {payload['result_id']}"
            ]
        preferred = last["rule_version"] if last["rule_version"] in complete else None
        rule_version = preferred or sorted(complete)[0]
        vector = complete[rule_version]
        value = self._value_of(ws, vector)
        if (
            not last["retracted"]
            and value == last_value
            and dict(sorted(vector.items())) == last["version_vector"]
        ):
            return []
        core = {
            "kind": "compensation",
            "window": ws.spec.window,
            "rule_version": rule_version,
            "epoch": self.current_epoch(),
            "version_vector": dict(sorted(vector.items())),
            "value": value,
            "comp_index": comp_index,
        }
        payload = {
            "rec": "publish",
            **core,
            "delta": value - last_value,
            "base_result_id": last["result_id"],
            "retracted": False,
            "result_id": self._result_id(core),
        }
        self._emit_publish(ws, payload)
        return [
            f"COMPENSATION {ws.spec.window} {last_value} -> {value} "
            f"(delta {payload['delta']}, id {payload['result_id']})"
        ]

    # ------------------------------------------------------------------
    # plumbing
    # ------------------------------------------------------------------
    def _reject(self, p: protocol.Partial, reason: str) -> list[str]:
        self._record({
            "rec": "slice_rejected",
            "window": p.window,
            "shard": p.shard,
            "epoch": p.epoch,
            "local_version": p.local_version,
            "rule_version": p.rule_version,
            "reason": reason,
        })
        return [f"rejected {p.shard} v{p.local_version} ({reason})"]

    def _emit_publish(self, ws: WindowState, payload: dict) -> None:
        self._record(payload)
        self.crash_hook("after_publish_write", payload)
        self._deliver(payload)

    def _deliver(self, payload: dict) -> None:
        if self.downstream is None:
            return
        ack = self.downstream.apply(payload)
        self.crash_hook("after_delivery", payload)
        self.handle(ack)

    def _record(self, payload: dict) -> None:
        self.log.append(payload)
        self._apply(payload)

    def _apply(self, payload: dict) -> None:
        rec = payload["rec"]
        if rec == "slice_accepted":
            ws = self.windows[payload["window"]]
            ws.slices.setdefault(payload["shard"], {})[
                payload["local_version"]
            ] = payload
        elif rec == "slice_revoked":
            ws = self.windows[payload["window"]]
            ws.revoked.add((payload["shard"], payload["local_version"]))
        elif rec == "handoff_registered":
            self.handoffs.append(protocol.Handoff(
                manifest_id=payload["manifest_id"],
                epoch=payload["epoch"],
                splits={k: tuple(v) for k, v in payload["splits"].items()},
            ))
        elif rec == "publish":
            ws = self.windows[payload["window"]]
            ws.publishes.append(payload)
            ws.status = "published"
        elif rec == "publish_acked":
            for ws in self.windows.values():
                if any(p["result_id"] == payload["result_id"] for p in ws.publishes):
                    ws.acked.add(payload["result_id"])
                    break
        elif rec == "window_expired":
            self.windows[payload["window"]].status = "expired"
