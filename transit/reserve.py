"""班次容量预留。

- 多段原子预留：两段式提交，任一段不可用则回滚全部暂存段，无额度泄漏。
- 代际令牌：令牌形如 "<rid>#<gen>"；预留过期被清扫或主动释放时代际递增，
  旧令牌再释放会被拒绝，不会重复增加额度。
- 持久化：状态写入 JSON 文件，重启后过期预留只回收一次，过期令牌依旧无效。
- 时间区间左闭右开：预留有效期为 [created_at, expires_at)，t == expires_at 即过期。
"""
from __future__ import annotations

import json
import os
from dataclasses import dataclass

from .model import Timetable


@dataclass
class Reservation:
    rid: str
    trip_ids: tuple[str, ...]
    token: str
    created_at: int
    expires_at: int


class ReservationManager:
    def __init__(self, timetable: Timetable, state_path: str | None = None, now: int = 0) -> None:
        self.tt = timetable
        self.state_path = state_path
        self.now = now
        self._counter = 0
        self._generations: dict[str, int] = {}
        self._active: dict[str, Reservation] = {}
        if state_path and os.path.exists(state_path):
            self._load()
        self._sweep()

    # ---- 容量 ----
    def available(self, trip_id: str) -> int:
        trip = self.tt.trips[trip_id]
        used = sum(1 for r in self._active.values() if trip_id in r.trip_ids)
        return trip.capacity - used

    # ---- 原子多段预留 ----
    def reserve(self, trip_ids, ttl: int) -> str | None:
        self._sweep()
        trip_ids = tuple(trip_ids)
        staged: dict[str, int] = {}
        for tid in trip_ids:
            trip = self.tt.trips.get(tid)
            if trip is None or trip.cancelled:
                self._rollback(staged)
                return None
            if self.available(tid) - staged.get(tid, 0) < 1:
                self._rollback(staged)
                return None
            staged[tid] = staged.get(tid, 0) + 1
        # 全部段可用：提交为一个预留
        self._counter += 1
        rid = f"R{self._counter}"
        gen = self._generations.get(rid, 0) + 1
        self._generations[rid] = gen
        token = f"{rid}#{gen}"
        self._active[rid] = Reservation(rid, trip_ids, token, self.now, self.now + ttl)
        self._persist()
        return token

    @staticmethod
    def _rollback(staged: dict) -> None:
        # 暂存段从未计入真实额度，回滚即丢弃暂存；显式置空以便审计。
        staged.clear()

    # ---- 释放与过期 ----
    def release(self, token: str) -> bool:
        self._sweep()
        rid, _, _ = token.partition("#")
        r = self._active.get(rid)
        if r is None or r.token != token:
            return False  # 过期/陈旧令牌：拒绝，不增加任何额度
        del self._active[rid]
        self._generations[rid] += 1
        self._persist()
        return True

    def is_valid(self, token: str) -> bool:
        self._sweep()
        rid, _, _ = token.partition("#")
        r = self._active.get(rid)
        return r is not None and r.token == token

    def advance(self, now: int) -> None:
        if now < self.now:
            raise ValueError("clock cannot go backwards")
        self.now = now
        self._sweep()
        self._persist()

    def _sweep(self) -> None:
        # 左闭右开：expires_at <= now 即过期
        expired = [rid for rid, r in self._active.items() if r.expires_at <= self.now]
        for rid in expired:
            del self._active[rid]
            self._generations[rid] += 1  # 代际递增，旧令牌失效
        if expired:
            self._persist()

    # ---- 持久化 ----
    def _persist(self) -> None:
        if not self.state_path:
            return
        data = {
            "now": self.now,
            "counter": self._counter,
            "generations": self._generations,
            "active": {
                rid: {
                    "trip_ids": list(r.trip_ids),
                    "token": r.token,
                    "created_at": r.created_at,
                    "expires_at": r.expires_at,
                }
                for rid, r in self._active.items()
            },
        }
        tmp = self.state_path + ".tmp"
        with open(tmp, "w", encoding="utf-8") as fh:
            json.dump(data, fh, ensure_ascii=False, indent=2)
        os.replace(tmp, self.state_path)

    def _load(self) -> None:
        with open(self.state_path, encoding="utf-8") as fh:
            data = json.load(fh)
        self.now = max(self.now, data["now"])
        self._counter = data["counter"]
        self._generations = {k: int(v) for k, v in data["generations"].items()}
        self._active = {
            rid: Reservation(rid, tuple(a["trip_ids"]), a["token"],
                             a["created_at"], a["expires_at"])
            for rid, a in data["active"].items()
        }
