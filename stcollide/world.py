"""事件驱动的分段匀速 AABB 碰撞世界。

显式规则汇总：
- 盒为闭集；碰撞区间分 "overlap"（内部相交）/ "contact"（持续贴合）/
  "touch"（瞬时相切）三类，见 core 模块。
- 只有 overlap 会产生碰撞事件；contact/touch 只在区间查询中报告。
- 事件携带双方轨迹版本号；轨迹被修改（变速/销毁）后版本递增，
  旧预测事件在出队时因版本不符被丢弃，绝不生效。
- 同一时刻的所有事件基于同一前态批处理：先应用该时刻的计划动作，
  再校验并收集全部有效碰撞，最后统一触发响应回调。
- 销毁时刻为半开区间端点：对象在 t_destroy 时刻已不存在，
  恰好落在销毁时刻的相切不计为碰撞。
- 事件预算限制已处理碰撞事件总数，零时间环（响应后仍重叠导致
  同刻反复碰撞）会被预算截断，状态置为 "budget_exceeded"。
"""

from __future__ import annotations

import heapq
from dataclasses import dataclass, field
from fractions import Fraction

from .broadphase import STGrid
from .core import F, piece_collision, vec
from .trajectory import Trajectory


@dataclass(frozen=True)
class CollisionInterval:
    """窗口内一段连续碰撞：[start, end]，enter 为内部重叠起点（事件时刻基准）。"""

    start: Fraction
    end: Fraction
    kind: str  # "overlap" | "contact" | "touch"
    enter: Fraction | None


@dataclass
class _Body:
    oid: object
    size: tuple
    traj: Trajectory
    version: int = 0
    t_destroy: Fraction | None = None


@dataclass
class _CollisionEvent:
    time: Fraction
    a: object
    b: object
    ver_a: int
    ver_b: int
    exit: Fraction


@dataclass(frozen=True)
class CollisionRecord:
    """一次已生效的碰撞事件；位置为事件时刻、响应之前的前态。"""

    time: Fraction
    a: object
    b: object
    exit: Fraction
    box_a: tuple  # (lo, hi)
    box_b: tuple


@dataclass
class SimResult:
    events: list = field(default_factory=list)
    status: str = "ok"  # "ok" | "budget_exceeded"
    stale_discarded: int = 0


class World:
    def __init__(self, dim, t0, t1, cell_size=4, event_budget=10000):
        self.dim = dim
        self.t0 = F(t0)
        self.t1 = F(t1)
        self.budget = event_budget
        self.grid = STGrid(dim, cell_size)
        self.bodies = {}
        self._queue = []
        self._seq = 0
        self._now = None
        self._simulated = False

    # ---------- 对象与轨迹修改 ----------

    def add_object(self, oid, lo, hi, velocity):
        if oid in self.bodies:
            raise ValueError(f"对象 {oid!r} 已存在")
        lo, hi, vel = vec(lo), vec(hi), vec(velocity)
        if not (len(lo) == len(hi) == len(vel) == self.dim):
            raise ValueError("维度不一致")
        if any(l > h for l, h in zip(lo, hi)):
            raise ValueError("盒的最小角不能大于最大角")
        body = _Body(oid=oid, size=tuple(h - l for l, h in zip(lo, hi)),
                     traj=Trajectory(self.t0, lo, vel))
        self.bodies[oid] = body
        self.grid.update(oid, self._st_boxes(body))

    def set_velocity(self, oid, t, velocity):
        """在时刻 t 替换 oid 的轨迹后缀（碰撞后轨迹替换也经由此接口）。"""
        t = F(t)
        self._check_mutation_time(t)
        body = self.bodies[oid]
        body.traj.set_velocity(t, velocity)
        body.version += 1
        self.grid.update(oid, self._st_boxes(body))
        self._repredict(body)

    def destroy(self, oid, t):
        """对象在时刻 t 销毁；存在区间为 [t0, t_destroy) 半开区间。"""
        t = F(t)
        self._check_mutation_time(t)
        body = self.bodies[oid]
        body.t_destroy = t if body.t_destroy is None else min(body.t_destroy, t)
        body.version += 1
        self.grid.update(oid, self._st_boxes(body))
        self._repredict(body)

    def schedule(self, t, fn):
        """安排在时刻 t 执行的外部动作 fn(world)；同刻动作先于碰撞处理。"""
        t = F(t)
        self._push(t, ("action", fn))

    def _check_mutation_time(self, t):
        if t < self.t0:
            raise ValueError("修改时刻早于窗口起点")
        if self._now is not None and t < self._now:
            raise ValueError("不能修改已流逝的时刻")

    # ---------- 宽阶段 ----------

    def _st_boxes(self, body):
        end = self.t1 if body.t_destroy is None else min(self.t1, body.t_destroy)
        boxes = []
        for s, e, pos, vel in body.traj.pieces(self.t0, end):
            pos_e = tuple(p + v * (e - s) for p, v in zip(pos, vel))
            los = tuple(min(p, q) for p, q in zip(pos, pos_e))
            his = tuple(max(p, q) + sz for p, q, sz in zip(pos, pos_e, body.size))
            boxes.append((s, e, los, his))
        return boxes

    # ---------- 窄阶段 ----------

    def _pair_window(self, ba, bb, t_from):
        lo_t = self.t0 if t_from is None else max(self.t0, t_from)
        hi_t = self.t1
        for body in (ba, bb):
            if body.t_destroy is not None:
                hi_t = min(hi_t, body.t_destroy)
        return lo_t, hi_t

    def _pair_intervals(self, a, b, t_from=None):
        """精确计算一对物体在窗口内的全部碰撞区间（合并相邻分段）。"""
        ba, bb = self.bodies[a], self.bodies[b]
        lo_t, hi_t = self._pair_window(ba, bb, t_from)
        if lo_t >= hi_t:
            return []
        bounds = {lo_t, hi_t}
        bounds.update(ba.traj.breakpoints(lo_t, hi_t))
        bounds.update(bb.traj.breakpoints(lo_t, hi_t))
        bounds = sorted(bounds)
        merged = []
        for s, e in zip(bounds, bounds[1:]):
            if s >= e:
                continue
            hit = piece_collision(
                ba.traj.position(s), ba.size, ba.traj.velocity(s),
                bb.traj.position(s), bb.size, bb.traj.velocity(s), s, e)
            if hit is None:
                continue
            if merged and hit.ta <= merged[-1][1]:
                m = merged[-1]
                m[1] = max(m[1], hit.tb)
                if m[2] is None and hit.has_interior:
                    m[2] = hit.sa
            else:
                merged.append([hit.ta, hit.tb, hit.sa if hit.has_interior else None])
        out = []
        for start, end, enter in merged:
            if enter is not None:
                kind = "overlap"
            elif start < end:
                kind = "contact"
            else:
                kind = "touch"
            out.append(CollisionInterval(start, end, kind, enter))
        if hi_t < self.t1:  # 被销毁截断：恰好落在销毁时刻的相切不计
            out = [iv for iv in out if not (iv.start == iv.end == hi_t)]
        return out

    def _first_event(self, a, b, t_from):
        ba, bb = self.bodies[a], self.bodies[b]
        for iv in self._pair_intervals(a, b, t_from):
            if iv.enter is not None:
                return _CollisionEvent(iv.enter, a, b, ba.version, bb.version, iv.end)
        return None

    # ---------- 事件队列 ----------

    def _push(self, time, payload):
        self._seq += 1
        heapq.heappush(self._queue, (time, self._seq, payload))

    def _repredict(self, body):
        if self._now is None:
            return
        for pair in self.grid.pairs_for(body.oid):
            a, b = sorted(pair, key=repr)
            ev = self._first_event(a, b, self._now)
            if ev is not None:
                self._push(ev.time, ("collision", ev))

    def _valid(self, ev):
        ba = self.bodies.get(ev.a)
        bb = self.bodies.get(ev.b)
        return (ba is not None and bb is not None
                and ba.version == ev.ver_a and bb.version == ev.ver_b)

    # ---------- 查询 ----------

    def snapshot(self, t):
        """时刻 t 的精确快照：{oid: (lo, hi)}；已销毁对象不出现。"""
        t = F(t)
        out = {}
        for oid, body in self.bodies.items():
            if body.t_destroy is not None and t >= body.t_destroy:
                continue
            pos = body.traj.position(t)
            out[oid] = (pos, tuple(p + s for p, s in zip(pos, body.size)))
        return out

    def collision_intervals(self):
        """全部候选对的碰撞区间：{pair_tuple: [CollisionInterval, ...]}。"""
        out = {}
        for pair in self.grid.all_pairs():
            a, b = sorted(pair, key=repr)
            ivs = self._pair_intervals(a, b)
            if ivs:
                out[(a, b)] = ivs
        return out

    def first_collision(self):
        """窗口内最早碰撞 (time, pair, kind)；kind 可为 touch/contact/overlap。"""
        best = None
        for pair, ivs in self.collision_intervals().items():
            for iv in ivs:
                if best is None or (iv.start, repr(pair)) < (best[0], repr(best[1])):
                    best = (iv.start, pair, iv.kind)
        return best

    # ---------- 模拟 ----------

    def simulate(self, respond=None):
        """事件驱动模拟；respond(world, records) 为碰撞响应回调（可改轨迹）。

        同一时刻的事件作为一批：先执行计划动作，再按同一前态校验并
        收集全部有效碰撞，最后一次性调用 respond。
        """
        if self._simulated:
            raise RuntimeError("simulate 只能调用一次")
        self._simulated = True
        self._now = self.t0
        for pair in self.grid.all_pairs():
            a, b = sorted(pair, key=repr)
            ev = self._first_event(a, b, self.t0)
            if ev is not None:
                self._push(ev.time, ("collision", ev))
        result = SimResult()
        while self._queue:
            if len(result.events) >= self.budget:
                result.status = "budget_exceeded"
                break
            t = self._queue[0][0]
            if t > self.t1:
                break
            batch = []
            while self._queue and self._queue[0][0] == t:
                batch.append(heapq.heappop(self._queue)[2])
            self._now = t
            for kind, payload in batch:
                if kind == "action":
                    payload(self)
            valid = []
            for kind, payload in batch:
                if kind == "collision":
                    if self._valid(payload):
                        valid.append(payload)
                    else:
                        result.stale_discarded += 1
            if valid:
                snap = self.snapshot(t)
                valid.sort(key=lambda e: (repr(e.a), repr(e.b)))
                records = [CollisionRecord(e.time, e.a, e.b, e.exit,
                                           snap[e.a], snap[e.b]) for e in valid]
                result.events.extend(records)
                if respond is not None:
                    respond(self, records)
        self._now = self.t1
        return result
