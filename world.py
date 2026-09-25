"""World：对象管理、快照查询与事件驱动的碰撞模拟。

关键语义：
- 每个对象持有轨迹版本号；任何轨迹修改（变速/替换/销毁）都会递增版本。
- 预测事件携带双方版本号，弹出时版本不匹配即为旧预测，直接丢弃。
- 同一时刻的全部有效事件作为一批，基于同一前态（批处理前的轨迹）
  计算碰撞信息，然后一次性交给响应回调。
- 模拟带事件预算，防止零时间环（响应在同一时刻不断制造新碰撞）。
"""

from __future__ import annotations

import itertools
from dataclasses import dataclass
from heapq import heappop, heappush

import narrowphase
from broadphase import BroadPhaseIndex, swept_bound
from trajectory import F, Segment, Trajectory, as_vector


class EventBudgetExceeded(RuntimeError):
    """同一模拟中处理的有效事件数超过预算（通常是零时间环）。"""


@dataclass(frozen=True)
class Collision:
    """一次碰撞开始事件；pos_* 为批处理前轨迹在碰撞时刻的位置。"""

    pair: tuple
    time: object
    pos_a: tuple
    pos_b: tuple


class _Object:
    __slots__ = ("traj", "size", "version", "alive_until")

    def __init__(self, traj, size):
        self.traj = traj
        self.size = size
        self.version = 0
        self.alive_until = None  # Fraction 或 None；存在区间为 [birth, alive_until)


class World:
    def __init__(self):
        self._objects = {}
        self._mutation_hook = None  # 模拟期间由 simulate 安装

    # ------------------------------------------------------------------ 对象管理

    def add_object(self, oid, pos, size, vel=None, start=0, trajectory=None):
        if oid in self._objects:
            raise ValueError(f"duplicate object id {oid!r}")
        size = as_vector(size)
        if any(s <= 0 for s in size):
            raise ValueError("box size must be positive on every axis")
        if trajectory is None:
            vel = as_vector(vel if vel is not None else (0,) * len(size))
            trajectory = Trajectory.constant(pos, vel, start=start)
        if trajectory.dim != len(size):
            raise ValueError("trajectory and size dimension mismatch")
        self._objects[oid] = _Object(trajectory, size)

    def _get(self, oid) -> _Object:
        try:
            return self._objects[oid]
        except KeyError:
            raise KeyError(f"unknown object {oid!r}") from None

    def _mutated(self, oid, t):
        self._get(oid).version += 1
        if self._mutation_hook is not None:
            self._mutation_hook(oid, F(t))

    def change_velocity(self, oid, vel, t):
        """t 时刻起改为速度 vel；t 之后的旧段被截断替换。"""
        obj = self._get(oid)
        t = F(t)
        if t < obj.traj.birth:
            raise ValueError("cannot change velocity before birth")
        if obj.alive_until is not None and t > obj.alive_until:
            raise ValueError("cannot change velocity after destruction")
        vel = as_vector(vel)
        pos = obj.traj.position_at(t)
        kept = [s for s in obj.traj.segments if s.start < t]
        obj.traj = Trajectory(kept + [Segment(t, pos, vel)])
        self._mutated(oid, t)

    def set_trajectory(self, oid, segments):
        """整体替换轨迹（碰撞后轨迹替换）。segments 为 Segment 或 (start, pos, vel)。"""
        obj = self._get(oid)
        segs = [
            s if isinstance(s, Segment) else Segment(F(s[0]), as_vector(s[1]), as_vector(s[2]))
            for s in segments
        ]
        obj.traj = Trajectory(segs)
        t = obj.traj.birth
        self._mutated(oid, t)

    def destroy(self, oid, t):
        """t 时刻销毁对象：t 及之后不再参与碰撞。"""
        obj = self._get(oid)
        t = F(t)
        if t <= obj.traj.birth:
            raise ValueError("destruction time must be after birth")
        obj.alive_until = t
        self._mutated(oid, t)

    # ------------------------------------------------------------------ 查询（快照，不改状态）

    def version_of(self, oid):
        return self._get(oid).version

    def position_of(self, oid, t):
        return self._get(oid).traj.position_at(F(t))

    def _window_of(self, obj, t0, t1):
        end = t1 if obj.alive_until is None else min(t1, obj.alive_until)
        return max(t0, obj.traj.birth), end

    def _candidate_pairs(self, t0, t1):
        index = BroadPhaseIndex()
        for oid, obj in self._objects.items():
            lo, hi = self._window_of(obj, t0, t1)
            bound = swept_bound(obj.traj, obj.size, lo, hi)
            if bound is not None:
                index.upsert(oid, bound)
        return index.candidates()

    def collision_intervals(self, t0, t1):
        """时间窗内全部碰撞区间：{pair: [(start, end), ...]}。快照查询，不改状态。"""
        t0, t1 = F(t0), F(t1)
        result = {}
        for a, b in self._candidate_pairs(t0, t1):
            oa, ob = self._objects[a], self._objects[b]
            lo = max(self._window_of(oa, t0, t1)[0], self._window_of(ob, t0, t1)[0])
            hi = min(self._window_of(oa, t0, t1)[1], self._window_of(ob, t0, t1)[1])
            ivs = narrowphase.collision_intervals(oa.traj, oa.size, ob.traj, ob.size, lo, hi)
            if ivs:
                result[tuple(sorted((a, b)))] = ivs
        return result

    def first_collision(self, t0, t1):
        """时间窗内首碰撞：返回 (time, [pairs])；无碰撞返回 None。"""
        best = None
        pairs = []
        for pair, ivs in self.collision_intervals(t0, t1).items():
            start = ivs[0][0]
            if best is None or start < best:
                best, pairs = start, [pair]
            elif start == best:
                pairs.append(pair)
        if best is None:
            return None
        return best, sorted(pairs)

    def snapshot(self, t):
        """t 时刻正处于正体积重叠状态的对象对集合。"""
        t = F(t)
        ids = sorted(self._objects, key=repr)
        result = set()
        for i, a in enumerate(ids):
            oa = self._objects[a]
            if t < oa.traj.birth:
                continue
            if oa.alive_until is not None and t >= oa.alive_until:
                continue
            pa = oa.traj.position_at(t)
            for b in ids[i + 1:]:
                ob = self._objects[b]
                if t < ob.traj.birth:
                    continue
                if ob.alive_until is not None and t >= ob.alive_until:
                    continue
                pb = ob.traj.position_at(t)
                if all(
                    pa[k] < pb[k] + ob.size[k] and pb[k] < pa[k] + oa.size[k]
                    for k in range(oa.traj.dim)
                ):
                    result.add((a, b))
        return result

    # ------------------------------------------------------------------ 事件驱动模拟

    def simulate(self, t0, t1, respond=None, max_events=256):
        """在 [t0, t1] 上推进碰撞事件。

        respond(world, time, collisions) 在每批同刻碰撞后调用一次，
        可在其中 change_velocity / set_trajectory / destroy。
        返回 [(time, [Collision, ...]), ...] 按时间升序。
        处理的有效事件数超过 max_events 时抛出 EventBudgetExceeded。
        """
        t0, t1 = F(t0), F(t1)
        if t1 < t0:
            raise ValueError("empty window")
        queue = []
        seq = itertools.count()

        def window_end(obj):
            return t1 if obj.alive_until is None else min(t1, obj.alive_until)

        def predict(a, b, after, include_start):
            oa, ob = self._objects[a], self._objects[b]
            end = min(window_end(oa), window_end(ob))
            if end <= after:
                return
            t_begin = narrowphase.first_begin(
                oa.traj, oa.size, ob.traj, ob.size, after, end, include_start
            )
            if t_begin is not None:
                pair = tuple(sorted((a, b)))
                heappush(queue, (t_begin, next(seq), pair[0], pair[1],
                                 oa.version, ob.version))

        def repredict_object(a, t):
            oa = self._objects[a]
            end_a = window_end(oa)
            start_a = max(t, oa.traj.birth)
            if end_a <= start_a:
                return
            bound_a = swept_bound(oa.traj, oa.size, start_a, end_a)
            if bound_a is None:
                return
            for b, ob in self._objects.items():
                if b == a:
                    continue
                end_b = window_end(ob)
                start_b = max(t, ob.traj.birth)
                if end_b <= start_b:
                    continue
                bound_b = swept_bound(ob.traj, ob.size, start_b, end_b)
                if bound_b is not None and bound_a.intersects(bound_b):
                    predict(a, b, t, include_start=True)

        # 初始预测：宽阶段筛候选，窄阶段精算
        for a, b in self._candidate_pairs(t0, t1):
            predict(a, b, t0, include_start=True)

        log = []
        processed = 0
        self._mutation_hook = repredict_object
        try:
            while queue:
                t = queue[0][0]
                batch = []
                while queue and queue[0][0] == t:
                    batch.append(heappop(queue))
                # 校验版本与存活：旧预测在此被丢弃
                valid = {}
                for _, _, a, b, ver_a, ver_b in batch:
                    oa = self._objects.get(a)
                    ob = self._objects.get(b)
                    if oa is None or ob is None:
                        continue
                    if oa.version != ver_a or ob.version != ver_b:
                        continue
                    if oa.alive_until is not None and t >= oa.alive_until:
                        continue
                    if ob.alive_until is not None and t >= ob.alive_until:
                        continue
                    valid[(a, b)] = (a, b)
                if not valid:
                    continue
                processed += len(valid)
                if processed > max_events:
                    raise EventBudgetExceeded(
                        f"event budget {max_events} exceeded at t={t} "
                        "(possible zero-time loop)"
                    )
                # 同刻批处理：全部基于批处理前的前态计算碰撞信息
                collisions = [
                    Collision(
                        pair=(a, b),
                        time=t,
                        pos_a=self._objects[a].traj.position_at(t),
                        pos_b=self._objects[b].traj.position_at(t),
                    )
                    for a, b in sorted(valid.values())
                ]
                log.append((t, collisions))
                pre_versions = {oid: o.version for oid, o in self._objects.items()}
                if respond is not None:
                    respond(self, t, collisions)
                # 轨迹未被响应修改的碰撞对，从 t 之后再预测其下一次碰撞
                for a, b in (c.pair for c in collisions):
                    oa, ob = self._objects[a], self._objects[b]
                    if oa.version == pre_versions[a] and ob.version == pre_versions[b]:
                        predict(a, b, t, include_start=False)
        finally:
            self._mutation_hook = None
        return log
